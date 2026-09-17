package store

import (
	"context"
	"database/sql"
	"errors"
	"strings"
	"time"
)

type User struct {
	ID                string
	Email             string
	PasswordHash      string
	ReferralSource    string
	PinterestUsername string
	CreatedAt         time.Time
}

var userColumns = userColumnsFor("users")

func userColumnsFor(t string) string {
	return strings.NewReplacer("$", t).Replace(
		`$.id, COALESCE($.email, ''), COALESCE($.password_hash, ''), COALESCE($.referral_source, ''), COALESCE($.pinterest_username, ''), $.created_at`)
}

func scanUser(row interface{ Scan(...any) error }) (User, error) {
	var u User
	var created int64
	err := row.Scan(&u.ID, &u.Email, &u.PasswordHash, &u.ReferralSource, &u.PinterestUsername, &created)
	if errors.Is(err, sql.ErrNoRows) {
		return User{}, ErrNotFound
	}
	u.CreatedAt = fromMS(created)
	return u, err
}

func (s *Store) CreateUser(ctx context.Context, email, passwordHash string) (User, error) {
	u := User{ID: NewID(), Email: email, PasswordHash: passwordHash, CreatedAt: s.now().UTC()}
	_, err := s.db.ExecContext(ctx,
		`INSERT INTO users (id, email, password_hash, created_at) VALUES (?, ?, ?, ?)`,
		u.ID, u.Email, u.PasswordHash, ms(u.CreatedAt))
	if err != nil && strings.Contains(err.Error(), "UNIQUE") {
		return User{}, ErrConflict
	}
	return u, err
}

func (s *Store) UserByEmail(ctx context.Context, email string) (User, error) {
	return scanUser(s.db.QueryRowContext(ctx,
		`SELECT `+userColumns+` FROM users WHERE email = ? AND is_synthetic = 0`, email))
}

func (s *Store) UserByID(ctx context.Context, id string) (User, error) {
	return scanUser(s.db.QueryRowContext(ctx,
		`SELECT `+userColumns+` FROM users WHERE id = ? AND is_synthetic = 0`, id))
}

func (s *Store) SetReferralSource(ctx context.Context, userID, source string) error {
	return s.updateUserField(ctx, `UPDATE users SET referral_source = ? WHERE id = ?`, source, userID)
}

func (s *Store) SetPinterestUsername(ctx context.Context, userID, username string) error {
	return s.updateUserField(ctx, `UPDATE users SET pinterest_username = ? WHERE id = ?`, username, userID)
}

func (s *Store) updateUserField(ctx context.Context, query, value, userID string) error {
	res, err := s.db.ExecContext(ctx, query, value, userID)
	if err != nil {
		return err
	}
	if n, _ := res.RowsAffected(); n == 0 {
		return ErrNotFound
	}
	return nil
}

func (s *Store) DeleteUser(ctx context.Context, id string) error {
	res, err := s.db.ExecContext(ctx, `DELETE FROM users WHERE id = ? AND is_synthetic = 0`, id)
	if err != nil {
		return err
	}
	if n, _ := res.RowsAffected(); n == 0 {
		return ErrNotFound
	}
	return nil
}

func (s *Store) CreateSession(ctx context.Context, tokenHash, userID string, ttl time.Duration) error {
	now := s.now()
	_, err := s.db.ExecContext(ctx,
		`INSERT INTO sessions (token_hash, user_id, created_at, expires_at) VALUES (?, ?, ?, ?)`,
		tokenHash, userID, ms(now), ms(now.Add(ttl)))
	return err
}

// UserBySession resolves a live session to its user.
func (s *Store) UserBySession(ctx context.Context, tokenHash string) (User, error) {
	return scanUser(s.db.QueryRowContext(ctx,
		`SELECT `+userColumnsFor("u")+` FROM sessions s JOIN users u ON u.id = s.user_id
		 WHERE s.token_hash = ? AND s.expires_at > ?`, tokenHash, ms(s.now())))
}

func (s *Store) DeleteSession(ctx context.Context, tokenHash string) error {
	_, err := s.db.ExecContext(ctx, `DELETE FROM sessions WHERE token_hash = ?`, tokenHash)
	return err
}
