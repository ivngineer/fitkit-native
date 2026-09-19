// Package store persists Fitkit data in SQLite.
package store

import (
	"context"
	"crypto/rand"
	"database/sql"
	"encoding/hex"
	"errors"
	"fmt"
	"time"

	_ "modernc.org/sqlite"
)

// PinterestAuthorID is the synthetic account that authors every imported pin.
const PinterestAuthorID = "pinterest"

var (
	ErrNotFound = errors.New("not found")
	ErrConflict = errors.New("conflict")
)

type Store struct {
	db  *sql.DB
	now func() time.Time
}

func Open(path string) (*Store, error) {
	dsn := fmt.Sprintf("file:%s?_pragma=foreign_keys(1)&_pragma=journal_mode(WAL)&_pragma=busy_timeout(5000)", path)
	db, err := sql.Open("sqlite", dsn)
	if err != nil {
		return nil, err
	}
	// SQLite allows a single writer; one connection avoids SQLITE_BUSY churn.
	db.SetMaxOpenConns(1)
	s := &Store{db: db, now: time.Now}
	if err := s.migrate(context.Background()); err != nil {
		db.Close()
		return nil, err
	}
	return s, nil
}

func (s *Store) Close() error { return s.db.Close() }

const schema = `
CREATE TABLE IF NOT EXISTS users (
	id                 TEXT PRIMARY KEY,
	email              TEXT UNIQUE COLLATE NOCASE,
	password_hash      TEXT,
	display_name       TEXT NOT NULL DEFAULT '',
	is_synthetic       INTEGER NOT NULL DEFAULT 0,
	referral_source    TEXT,
	pinterest_username TEXT,
	created_at         INTEGER NOT NULL
);

CREATE TABLE IF NOT EXISTS sessions (
	token_hash TEXT PRIMARY KEY,
	user_id    TEXT NOT NULL REFERENCES users(id) ON DELETE CASCADE,
	created_at INTEGER NOT NULL,
	expires_at INTEGER NOT NULL
);

CREATE TABLE IF NOT EXISTS pins (
	id               TEXT PRIMARY KEY,
	pinterest_id     TEXT NOT NULL UNIQUE,
	author_id        TEXT NOT NULL REFERENCES users(id),
	title            TEXT NOT NULL,
	description      TEXT NOT NULL DEFAULT '',
	link             TEXT NOT NULL DEFAULT '',
	image_source_url TEXT NOT NULL,
	image_file       TEXT NOT NULL,
	width            INTEGER NOT NULL,
	height           INTEGER NOT NULL,
	dominant_color   TEXT NOT NULL DEFAULT '',
	created_at       INTEGER NOT NULL
);

CREATE TABLE IF NOT EXISTS saved_pins (
	user_id  TEXT NOT NULL REFERENCES users(id) ON DELETE CASCADE,
	pin_id   TEXT NOT NULL REFERENCES pins(id) ON DELETE CASCADE,
	saved_at INTEGER NOT NULL,
	hidden_at INTEGER,
	PRIMARY KEY (user_id, pin_id)
);
CREATE INDEX IF NOT EXISTS saved_pins_by_user ON saved_pins(user_id, saved_at DESC);

CREATE TABLE IF NOT EXISTS cart_items (
	user_id  TEXT NOT NULL REFERENCES users(id) ON DELETE CASCADE,
	pin_id   TEXT NOT NULL REFERENCES pins(id) ON DELETE CASCADE,
	added_at INTEGER NOT NULL,
	PRIMARY KEY (user_id, pin_id)
);
CREATE INDEX IF NOT EXISTS cart_items_by_user ON cart_items(user_id, added_at DESC);

CREATE TABLE IF NOT EXISTS embedding_jobs (
	pin_id     TEXT PRIMARY KEY REFERENCES pins(id) ON DELETE CASCADE,
	status     TEXT NOT NULL,
	created_at INTEGER NOT NULL
);

CREATE TABLE IF NOT EXISTS import_jobs (
	id                 TEXT PRIMARY KEY,
	user_id            TEXT NOT NULL REFERENCES users(id) ON DELETE CASCADE,
	pinterest_username TEXT NOT NULL,
	status             TEXT NOT NULL,
	discovered         INTEGER NOT NULL DEFAULT 0,
	imported           INTEGER NOT NULL DEFAULT 0,
	reused             INTEGER NOT NULL DEFAULT 0,
	failed             INTEGER NOT NULL DEFAULT 0,
	error_code         TEXT NOT NULL DEFAULT '',
	error_message      TEXT NOT NULL DEFAULT '',
	retryable          INTEGER NOT NULL DEFAULT 0,
	created_at         INTEGER NOT NULL,
	updated_at         INTEGER NOT NULL,
	finished_at        INTEGER
);
CREATE INDEX IF NOT EXISTS import_jobs_by_user ON import_jobs(user_id, created_at DESC);

CREATE TABLE IF NOT EXISTS pin_analyses (
	pin_id        TEXT PRIMARY KEY REFERENCES pins(id) ON DELETE CASCADE,
	status        TEXT NOT NULL,
	result_json   TEXT NOT NULL DEFAULT '',
	error_code    TEXT NOT NULL DEFAULT '',
	error_message TEXT NOT NULL DEFAULT '',
	created_at    INTEGER NOT NULL,
	updated_at    INTEGER NOT NULL
);

CREATE TABLE IF NOT EXISTS addresses (
	id            TEXT PRIMARY KEY,
	user_id       TEXT NOT NULL REFERENCES users(id) ON DELETE CASCADE,
	kind          TEXT NOT NULL,
	label         TEXT NOT NULL DEFAULT '',
	country       TEXT NOT NULL,
	forwarder     TEXT NOT NULL DEFAULT '',
	final_country TEXT NOT NULL,
	fields_json   TEXT NOT NULL,
	is_default    INTEGER NOT NULL DEFAULT 0,
	created_at    INTEGER NOT NULL
);
CREATE INDEX IF NOT EXISTS addresses_by_user ON addresses(user_id, created_at);

CREATE TABLE IF NOT EXISTS size_profiles (
	user_id  TEXT NOT NULL REFERENCES users(id) ON DELETE CASCADE,
	category TEXT NOT NULL,
	size     TEXT NOT NULL,
	PRIMARY KEY (user_id, category)
);

CREATE TABLE IF NOT EXISTS looks (
	id         TEXT PRIMARY KEY,
	user_id    TEXT NOT NULL REFERENCES users(id) ON DELETE CASCADE,
	pin_id     TEXT NOT NULL REFERENCES pins(id) ON DELETE CASCADE,
	plan_json  TEXT NOT NULL,
	created_at INTEGER NOT NULL
);
CREATE INDEX IF NOT EXISTS looks_by_user ON looks(user_id, created_at DESC);

CREATE TABLE IF NOT EXISTS look_stores (
	look_id     TEXT NOT NULL REFERENCES looks(id) ON DELETE CASCADE,
	merchant    TEXT NOT NULL,
	position    INTEGER NOT NULL DEFAULT 0,
	status      TEXT NOT NULL,
	order_ref   TEXT NOT NULL DEFAULT '',
	tracking_no TEXT NOT NULL DEFAULT '',
	carrier     TEXT NOT NULL DEFAULT '',
	updated_at  INTEGER NOT NULL,
	PRIMARY KEY (look_id, merchant)
);

-- Cache of fetched store data (e.g. Shopify product JSON), keyed by URL.
CREATE TABLE IF NOT EXISTS shop_cache (
	key        TEXT PRIMARY KEY,
	body       TEXT NOT NULL,
	fetched_at INTEGER NOT NULL
);
`

func (s *Store) migrate(ctx context.Context) error {
	if _, err := s.db.ExecContext(ctx, schema); err != nil {
		return fmt.Errorf("migrate: %w", err)
	}
	if err := s.addColumn(ctx, "saved_pins", "hidden_at", "INTEGER"); err != nil {
		return fmt.Errorf("migrate: %w", err)
	}
	_, err := s.db.ExecContext(ctx,
		`INSERT OR IGNORE INTO users (id, display_name, is_synthetic, created_at) VALUES (?, 'Pinterest', 1, ?)`,
		PinterestAuthorID, s.now().UnixMilli())
	return err
}

// addColumn adds a column to a table created by an older schema.
func (s *Store) addColumn(ctx context.Context, table, column, definition string) error {
	rows, err := s.db.QueryContext(ctx, `SELECT name FROM pragma_table_info(?)`, table)
	if err != nil {
		return err
	}
	defer rows.Close()
	for rows.Next() {
		var name string
		if err := rows.Scan(&name); err != nil {
			return err
		}
		if name == column {
			return nil
		}
	}
	if err := rows.Err(); err != nil {
		return err
	}
	rows.Close()
	_, err = s.db.ExecContext(ctx, `ALTER TABLE `+table+` ADD COLUMN `+column+` `+definition)
	return err
}

// NewID returns a random 128-bit hex identifier.
func NewID() string {
	var b [16]byte
	if _, err := rand.Read(b[:]); err != nil {
		panic(err)
	}
	return hex.EncodeToString(b[:])
}

func ms(t time.Time) int64 { return t.UnixMilli() }

func fromMS(v int64) time.Time { return time.UnixMilli(v).UTC() }
