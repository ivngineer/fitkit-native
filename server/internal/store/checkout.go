package store

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"time"
)

// AddressFields holds the shipping-form fields for an address. It is stored
// as JSON in addresses.fields_json since forwarders and countries each need
// a slightly different subset.
type AddressFields struct {
	FirstName string `json:"firstName"`
	LastName  string `json:"lastName"`
	Line1     string `json:"line1"`
	Line2     string `json:"line2"`
	City      string `json:"city"`
	Region    string `json:"region"`
	Zip       string `json:"zip"`
	Phone     string `json:"phone"`
	SuiteID   string `json:"suiteId"`  // forwarder personal suite / customer ID
	NPBranch  string `json:"npBranch"` // Nova Poshta branch number or name
}

type Address struct {
	ID, UserID, Kind, Label, Country, Forwarder, FinalCountry string
	Fields                                                    AddressFields
	IsDefault                                                 bool
	CreatedAt                                                 time.Time
}

const addressColumns = `id, user_id, kind, label, country, forwarder, final_country, fields_json, is_default, created_at`

func scanAddress(row interface{ Scan(...any) error }) (Address, error) {
	var a Address
	var fieldsJSON string
	var isDefault int
	var created int64
	err := row.Scan(&a.ID, &a.UserID, &a.Kind, &a.Label, &a.Country, &a.Forwarder, &a.FinalCountry,
		&fieldsJSON, &isDefault, &created)
	if errors.Is(err, sql.ErrNoRows) {
		return Address{}, ErrNotFound
	}
	if err != nil {
		return Address{}, err
	}
	if err := json.Unmarshal([]byte(fieldsJSON), &a.Fields); err != nil {
		return Address{}, err
	}
	a.IsDefault = isDefault != 0
	a.CreatedAt = fromMS(created)
	return a, nil
}

// Addresses lists a user's addresses, default first then oldest first.
func (s *Store) Addresses(ctx context.Context, userID string) ([]Address, error) {
	rows, err := s.db.QueryContext(ctx,
		`SELECT `+addressColumns+` FROM addresses WHERE user_id = ? ORDER BY is_default DESC, created_at`, userID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	addrs := []Address{}
	for rows.Next() {
		a, err := scanAddress(rows)
		if err != nil {
			return nil, err
		}
		addrs = append(addrs, a)
	}
	return addrs, rows.Err()
}

// AddressByID returns ErrNotFound when the address is missing or owned by another user.
func (s *Store) AddressByID(ctx context.Context, userID, id string) (Address, error) {
	return scanAddress(s.db.QueryRowContext(ctx,
		`SELECT `+addressColumns+` FROM addresses WHERE id = ? AND user_id = ?`, id, userID))
}

// CreateAddress sets a.ID and a.CreatedAt. A user's first address always
// becomes the default; setting a.IsDefault on any other address clears it
// on the rest.
func (s *Store) CreateAddress(ctx context.Context, a *Address) error {
	a.ID = NewID()
	a.CreatedAt = s.now().UTC()
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback()
	var count int
	if err := tx.QueryRowContext(ctx, `SELECT COUNT(*) FROM addresses WHERE user_id = ?`, a.UserID).Scan(&count); err != nil {
		return err
	}
	if count == 0 {
		a.IsDefault = true
	}
	if a.IsDefault {
		if _, err := tx.ExecContext(ctx, `UPDATE addresses SET is_default = 0 WHERE user_id = ?`, a.UserID); err != nil {
			return err
		}
	}
	fieldsJSON, err := json.Marshal(a.Fields)
	if err != nil {
		return err
	}
	if _, err := tx.ExecContext(ctx, `INSERT INTO addresses
		(id, user_id, kind, label, country, forwarder, final_country, fields_json, is_default, created_at)
		VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?)`,
		a.ID, a.UserID, a.Kind, a.Label, a.Country, a.Forwarder, a.FinalCountry, string(fieldsJSON), a.IsDefault, ms(a.CreatedAt)); err != nil {
		return err
	}
	return tx.Commit()
}

// UpdateAddress updates every mutable column for (a.ID, a.UserID), keeping
// created_at. A user with addresses always has exactly one default: clearing
// a.IsDefault on the current default is a no-op that keeps it default.
func (s *Store) UpdateAddress(ctx context.Context, a *Address) error {
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback()
	var wasDefault int
	err = tx.QueryRowContext(ctx, `SELECT is_default FROM addresses WHERE id = ? AND user_id = ?`, a.ID, a.UserID).Scan(&wasDefault)
	if errors.Is(err, sql.ErrNoRows) {
		return ErrNotFound
	}
	if err != nil {
		return err
	}
	if !a.IsDefault && wasDefault != 0 {
		a.IsDefault = true
	}
	if a.IsDefault {
		if _, err := tx.ExecContext(ctx, `UPDATE addresses SET is_default = 0 WHERE user_id = ? AND id != ?`, a.UserID, a.ID); err != nil {
			return err
		}
	}
	fieldsJSON, err := json.Marshal(a.Fields)
	if err != nil {
		return err
	}
	res, err := tx.ExecContext(ctx, `UPDATE addresses SET kind = ?, label = ?, country = ?, forwarder = ?,
		final_country = ?, fields_json = ?, is_default = ? WHERE id = ? AND user_id = ?`,
		a.Kind, a.Label, a.Country, a.Forwarder, a.FinalCountry, string(fieldsJSON), a.IsDefault, a.ID, a.UserID)
	if err != nil {
		return err
	}
	if n, _ := res.RowsAffected(); n == 0 {
		return ErrNotFound
	}
	return tx.Commit()
}

// DeleteAddress removes an address, promoting the oldest remaining one to
// default when the deleted address was the default.
func (s *Store) DeleteAddress(ctx context.Context, userID, id string) error {
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback()
	var wasDefault int
	err = tx.QueryRowContext(ctx, `SELECT is_default FROM addresses WHERE id = ? AND user_id = ?`, id, userID).Scan(&wasDefault)
	if errors.Is(err, sql.ErrNoRows) {
		return ErrNotFound
	}
	if err != nil {
		return err
	}
	if _, err := tx.ExecContext(ctx, `DELETE FROM addresses WHERE id = ? AND user_id = ?`, id, userID); err != nil {
		return err
	}
	if wasDefault != 0 {
		if _, err := tx.ExecContext(ctx, `UPDATE addresses SET is_default = 1 WHERE id = (
			SELECT id FROM addresses WHERE user_id = ? ORDER BY created_at LIMIT 1)`, userID); err != nil {
			return err
		}
	}
	return tx.Commit()
}

// Sizes returns a user's size profile as category -> size, an empty map when none.
func (s *Store) Sizes(ctx context.Context, userID string) (map[string]string, error) {
	rows, err := s.db.QueryContext(ctx, `SELECT category, size FROM size_profiles WHERE user_id = ?`, userID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	sizes := map[string]string{}
	for rows.Next() {
		var category, size string
		if err := rows.Scan(&category, &size); err != nil {
			return nil, err
		}
		sizes[category] = size
	}
	return sizes, rows.Err()
}

// SetSizes replaces a user's whole size profile. Entries with an empty size are skipped.
func (s *Store) SetSizes(ctx context.Context, userID string, sizes map[string]string) error {
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback()
	if _, err := tx.ExecContext(ctx, `DELETE FROM size_profiles WHERE user_id = ?`, userID); err != nil {
		return err
	}
	for category, size := range sizes {
		if size == "" {
			continue
		}
		if _, err := tx.ExecContext(ctx, `INSERT INTO size_profiles (user_id, category, size) VALUES (?, ?, ?)`,
			userID, category, size); err != nil {
			return err
		}
	}
	return tx.Commit()
}

type LookStore struct {
	LookID, Merchant, Status, OrderRef, TrackingNo, Carrier string
	Position                                                int
	UpdatedAt                                               time.Time
}

const lookStoreColumns = `look_id, merchant, position, status, order_ref, tracking_no, carrier, updated_at`

func scanLookStore(row interface{ Scan(...any) error }) (LookStore, error) {
	var ls LookStore
	var updated int64
	err := row.Scan(&ls.LookID, &ls.Merchant, &ls.Position, &ls.Status, &ls.OrderRef, &ls.TrackingNo, &ls.Carrier, &updated)
	if errors.Is(err, sql.ErrNoRows) {
		return LookStore{}, ErrNotFound
	}
	if err != nil {
		return LookStore{}, err
	}
	ls.UpdatedAt = fromMS(updated)
	return ls, nil
}

type Look struct {
	ID, UserID, PinID, PlanJSON string
	CreatedAt                   time.Time
	Stores                      []LookStore
}

// CreateLook sets l.ID (if empty), l.CreatedAt, and each store's LookID/UpdatedAt/Position.
func (s *Store) CreateLook(ctx context.Context, l *Look) error {
	if l.ID == "" {
		l.ID = NewID()
	}
	l.CreatedAt = s.now().UTC()
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback()
	if _, err := tx.ExecContext(ctx, `INSERT INTO looks (id, user_id, pin_id, plan_json, created_at) VALUES (?, ?, ?, ?, ?)`,
		l.ID, l.UserID, l.PinID, l.PlanJSON, ms(l.CreatedAt)); err != nil {
		return err
	}
	for i := range l.Stores {
		l.Stores[i].LookID = l.ID
		l.Stores[i].Position = i
		l.Stores[i].UpdatedAt = l.CreatedAt
		st := l.Stores[i]
		if _, err := tx.ExecContext(ctx, `INSERT INTO look_stores
			(look_id, merchant, position, status, order_ref, tracking_no, carrier, updated_at)
			VALUES (?, ?, ?, ?, ?, ?, ?, ?)`,
			st.LookID, st.Merchant, st.Position, st.Status, st.OrderRef, st.TrackingNo, st.Carrier, ms(st.UpdatedAt)); err != nil {
			return err
		}
	}
	return tx.Commit()
}

// Looks lists a user's looks newest first, each with its Stores loaded.
// limit <= 0 means 100.
func (s *Store) Looks(ctx context.Context, userID string, limit int) ([]Look, error) {
	if limit <= 0 {
		limit = 100
	}
	rows, err := s.db.QueryContext(ctx,
		`SELECT id, user_id, pin_id, plan_json, created_at FROM looks WHERE user_id = ? ORDER BY created_at DESC LIMIT ?`,
		userID, limit)
	if err != nil {
		return nil, err
	}
	looks := []Look{}
	index := map[string]int{}
	for rows.Next() {
		var l Look
		var created int64
		if err := rows.Scan(&l.ID, &l.UserID, &l.PinID, &l.PlanJSON, &created); err != nil {
			rows.Close()
			return nil, err
		}
		l.CreatedAt = fromMS(created)
		index[l.ID] = len(looks)
		looks = append(looks, l)
	}
	if err := rows.Err(); err != nil {
		rows.Close()
		return nil, err
	}
	rows.Close()
	if len(looks) == 0 {
		return looks, nil
	}
	// One query for all stores, keyed back onto their look by look_id.
	srows, err := s.db.QueryContext(ctx,
		`SELECT `+lookStoreColumns+` FROM look_stores WHERE look_id IN (SELECT id FROM looks WHERE user_id = ?) ORDER BY look_id, position`,
		userID)
	if err != nil {
		return nil, err
	}
	defer srows.Close()
	for srows.Next() {
		st, err := scanLookStore(srows)
		if err != nil {
			return nil, err
		}
		if i, ok := index[st.LookID]; ok {
			looks[i].Stores = append(looks[i].Stores, st)
		}
	}
	return looks, srows.Err()
}

// LookByID returns ErrNotFound when the look is missing or not owned by userID.
func (s *Store) LookByID(ctx context.Context, userID, id string) (Look, error) {
	var l Look
	var created int64
	err := s.db.QueryRowContext(ctx, `SELECT id, user_id, pin_id, plan_json, created_at FROM looks WHERE id = ? AND user_id = ?`,
		id, userID).Scan(&l.ID, &l.UserID, &l.PinID, &l.PlanJSON, &created)
	if errors.Is(err, sql.ErrNoRows) {
		return Look{}, ErrNotFound
	}
	if err != nil {
		return Look{}, err
	}
	l.CreatedAt = fromMS(created)
	rows, err := s.db.QueryContext(ctx, `SELECT `+lookStoreColumns+` FROM look_stores WHERE look_id = ? ORDER BY position`, l.ID)
	if err != nil {
		return Look{}, err
	}
	defer rows.Close()
	for rows.Next() {
		st, err := scanLookStore(rows)
		if err != nil {
			return Look{}, err
		}
		l.Stores = append(l.Stores, st)
	}
	return l, rows.Err()
}

// LookStoreUpdate patches a look_stores row. nil pointer fields leave the column unchanged.
type LookStoreUpdate struct {
	Status     string
	OrderRef   *string
	TrackingNo *string
	Carrier    *string
}

// UpdateLookStore updates a store row within a look owned by userID.
// Returns ErrNotFound when the look isn't owned by userID or the merchant row is missing.
func (s *Store) UpdateLookStore(ctx context.Context, userID, lookID, merchant string, u LookStoreUpdate) (LookStore, error) {
	now := ms(s.now())
	res, err := s.db.ExecContext(ctx, `UPDATE look_stores SET
		status = ?,
		order_ref = COALESCE(?, order_ref),
		tracking_no = COALESCE(?, tracking_no),
		carrier = COALESCE(?, carrier),
		updated_at = ?
		WHERE look_id = ? AND merchant = ? AND look_id IN (SELECT id FROM looks WHERE user_id = ?)`,
		u.Status, u.OrderRef, u.TrackingNo, u.Carrier, now, lookID, merchant, userID)
	if err != nil {
		return LookStore{}, err
	}
	if n, _ := res.RowsAffected(); n == 0 {
		return LookStore{}, ErrNotFound
	}
	return scanLookStore(s.db.QueryRowContext(ctx,
		`SELECT `+lookStoreColumns+` FROM look_stores WHERE look_id = ? AND merchant = ?`, lookID, merchant))
}

// CachedShopData returns a cache entry's body and true when it exists and is younger than maxAge.
func (s *Store) CachedShopData(ctx context.Context, key string, maxAge time.Duration) (string, bool, error) {
	var body string
	var fetched int64
	err := s.db.QueryRowContext(ctx, `SELECT body, fetched_at FROM shop_cache WHERE key = ?`, key).Scan(&body, &fetched)
	if errors.Is(err, sql.ErrNoRows) {
		return "", false, nil
	}
	if err != nil {
		return "", false, err
	}
	if s.now().Sub(fromMS(fetched)) > maxAge {
		return "", false, nil
	}
	return body, true, nil
}

// PutShopData upserts a cache entry.
func (s *Store) PutShopData(ctx context.Context, key, body string) error {
	_, err := s.db.ExecContext(ctx, `INSERT INTO shop_cache (key, body, fetched_at) VALUES (?, ?, ?)
		ON CONFLICT(key) DO UPDATE SET body = excluded.body, fetched_at = excluded.fetched_at`,
		key, body, ms(s.now()))
	return err
}
