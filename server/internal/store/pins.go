package store

import (
	"context"
	"database/sql"
	"errors"
	"time"
)

type Pin struct {
	ID             string
	PinterestID    string
	AuthorID       string
	Title          string
	Description    string
	Link           string
	ImageSourceURL string
	ImageFile      string
	Width          int
	Height         int
	DominantColor  string
	CreatedAt      time.Time
	SavedAt        time.Time // only set when listed for a user
}

const pinColumns = `p.id, p.pinterest_id, p.author_id, p.title, p.description, p.link, p.image_source_url, p.image_file, p.width, p.height, p.dominant_color, p.created_at`

func scanPin(row interface{ Scan(...any) error }, extra ...any) (Pin, error) {
	var p Pin
	var created int64
	dest := append([]any{&p.ID, &p.PinterestID, &p.AuthorID, &p.Title, &p.Description, &p.Link,
		&p.ImageSourceURL, &p.ImageFile, &p.Width, &p.Height, &p.DominantColor, &created}, extra...)
	err := row.Scan(dest...)
	if errors.Is(err, sql.ErrNoRows) {
		return Pin{}, ErrNotFound
	}
	p.CreatedAt = fromMS(created)
	return p, err
}

func (s *Store) PinByPinterestID(ctx context.Context, pinterestID string) (Pin, error) {
	return scanPin(s.db.QueryRowContext(ctx, `SELECT `+pinColumns+` FROM pins p WHERE p.pinterest_id = ?`, pinterestID))
}

func (s *Store) PinByID(ctx context.Context, id string) (Pin, error) {
	return scanPin(s.db.QueryRowContext(ctx, `SELECT `+pinColumns+` FROM pins p WHERE p.id = ?`, id))
}

// InsertPin stores a new pin and queues its embedding job. It returns
// ErrConflict when another import already stored the same Pinterest pin.
func (s *Store) InsertPin(ctx context.Context, p *Pin) error {
	if p.ID == "" {
		p.ID = NewID()
	}
	if p.AuthorID == "" {
		p.AuthorID = PinterestAuthorID
	}
	p.CreatedAt = s.now().UTC()
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback()
	res, err := tx.ExecContext(ctx, `INSERT OR IGNORE INTO pins
		(id, pinterest_id, author_id, title, description, link, image_source_url, image_file, width, height, dominant_color, created_at)
		VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)`,
		p.ID, p.PinterestID, p.AuthorID, p.Title, p.Description, p.Link, p.ImageSourceURL, p.ImageFile,
		p.Width, p.Height, p.DominantColor, ms(p.CreatedAt))
	if err != nil {
		return err
	}
	if n, _ := res.RowsAffected(); n == 0 {
		return ErrConflict
	}
	if _, err := tx.ExecContext(ctx,
		`INSERT OR IGNORE INTO embedding_jobs (pin_id, status, created_at) VALUES (?, 'queued', ?)`,
		p.ID, ms(p.CreatedAt)); err != nil {
		return err
	}
	return tx.Commit()
}

// SavePinForUser marks a pin saved. Existing saves keep their original time.
func (s *Store) SavePinForUser(ctx context.Context, userID, pinID string, savedAt time.Time) error {
	_, err := s.db.ExecContext(ctx,
		`INSERT OR IGNORE INTO saved_pins (user_id, pin_id, saved_at) VALUES (?, ?, ?)`,
		userID, pinID, ms(savedAt))
	return err
}

func (s *Store) IsPinSaved(ctx context.Context, userID, pinID string) (bool, error) {
	var one int
	err := s.db.QueryRowContext(ctx, `SELECT 1 FROM saved_pins WHERE user_id = ? AND pin_id = ?`, userID, pinID).Scan(&one)
	if errors.Is(err, sql.ErrNoRows) {
		return false, nil
	}
	return err == nil, err
}

// SetPinHidden hides a saved pin from the user's grid, or shows it again.
// The pin and the save stay, so re-imports don't bring it back.
func (s *Store) SetPinHidden(ctx context.Context, userID, pinID string, hidden bool) error {
	var hiddenAt any
	if hidden {
		hiddenAt = ms(s.now())
	}
	res, err := s.db.ExecContext(ctx,
		`UPDATE saved_pins SET hidden_at = ? WHERE user_id = ? AND pin_id = ?`, hiddenAt, userID, pinID)
	if err != nil {
		return err
	}
	if n, _ := res.RowsAffected(); n == 0 {
		return ErrNotFound
	}
	return nil
}

// SavedPins lists a user's visible saved pins newest first. before is an exclusive
// saved_at cursor in unix milliseconds (0 for the first page).
func (s *Store) SavedPins(ctx context.Context, userID string, before int64, limit int) ([]Pin, error) {
	if before <= 0 {
		before = 1<<62 - 1
	}
	rows, err := s.db.QueryContext(ctx, `SELECT `+pinColumns+`, sp.saved_at FROM saved_pins sp
		JOIN pins p ON p.id = sp.pin_id
		WHERE sp.user_id = ? AND sp.hidden_at IS NULL AND sp.saved_at < ?
		ORDER BY sp.saved_at DESC, p.id LIMIT ?`, userID, before, limit)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	pins := []Pin{}
	for rows.Next() {
		var saved int64
		p, err := scanPin(rows, &saved)
		if err != nil {
			return nil, err
		}
		p.SavedAt = fromMS(saved)
		pins = append(pins, p)
	}
	return pins, rows.Err()
}

// AddToCart puts a saved pin in the user's cart, keeping the first add time.
func (s *Store) AddToCart(ctx context.Context, userID, pinID string) error {
	saved, err := s.IsPinSaved(ctx, userID, pinID)
	if err != nil {
		return err
	}
	if !saved {
		return ErrNotFound
	}
	_, err = s.db.ExecContext(ctx,
		`INSERT OR IGNORE INTO cart_items (user_id, pin_id, added_at) VALUES (?, ?, ?)`,
		userID, pinID, ms(s.now()))
	return err
}

// RemoveFromCart drops a pin from the cart. Removing a pin that isn't in the
// cart is not an error, so a double tap can't fail.
func (s *Store) RemoveFromCart(ctx context.Context, userID, pinID string) error {
	_, err := s.db.ExecContext(ctx, `DELETE FROM cart_items WHERE user_id = ? AND pin_id = ?`, userID, pinID)
	return err
}

// CartPins lists the pins in a user's cart, most recently added first. Pins
// hidden from the grid stay in the cart, since adding one is a buying intent.
func (s *Store) CartPins(ctx context.Context, userID string) ([]Pin, error) {
	rows, err := s.db.QueryContext(ctx, `SELECT `+pinColumns+`, c.added_at FROM cart_items c
		JOIN pins p ON p.id = c.pin_id
		WHERE c.user_id = ?
		ORDER BY c.added_at DESC, p.id`, userID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	pins := []Pin{}
	for rows.Next() {
		var added int64
		p, err := scanPin(rows, &added)
		if err != nil {
			return nil, err
		}
		p.SavedAt = fromMS(added)
		pins = append(pins, p)
	}
	return pins, rows.Err()
}

func (s *Store) CountEmbeddingJobs(ctx context.Context) (int, error) {
	var n int
	err := s.db.QueryRowContext(ctx, `SELECT COUNT(*) FROM embedding_jobs`).Scan(&n)
	return n, err
}
