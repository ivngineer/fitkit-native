// Package store persists the employee workspace (orders, parcels, support tickets,
// employees) in the same SQLite database as the rest of Fitkit.
package store

import (
	"context"
	"crypto/rand"
	"database/sql"
	"encoding/hex"
	"errors"
	"fmt"
	"strings"
	"time"
)

var (
	ErrNotFound = errors.New("not found")
	ErrConflict = errors.New("conflict")
)

// DBTX is satisfied by *sql.DB and *sql.Tx.
type DBTX interface {
	ExecContext(ctx context.Context, query string, args ...any) (sql.Result, error)
	QueryContext(ctx context.Context, query string, args ...any) (*sql.Rows, error)
	QueryRowContext(ctx context.Context, query string, args ...any) *sql.Row
}

// Store wraps the shared Fitkit database. It does not own the connection.
//
// The database is opened with a single connection (SQLite has one writer), so
// a transaction holds it exclusively: never use Q() while inside Tx().
type Store struct {
	DB *sql.DB
}

// Q is a set of queries bound to the database or to a transaction.
type Q struct{ db DBTX }

func New(db *sql.DB) *Store { return &Store{DB: db} }

// Q returns queries outside a transaction.
func (s *Store) Q() *Q { return &Q{db: s.DB} }

// Tx runs fn in a transaction; it commits when fn returns nil.
func (s *Store) Tx(ctx context.Context, fn func(q *Q) error) error {
	tx, err := s.DB.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	if err := fn(&Q{db: tx}); err != nil {
		_ = tx.Rollback()
		return err
	}
	return tx.Commit()
}

const schema = `
CREATE TABLE IF NOT EXISTS employees (
	id            TEXT PRIMARY KEY,
	name          TEXT NOT NULL,
	email         TEXT NOT NULL UNIQUE COLLATE NOCASE,
	role          TEXT NOT NULL CHECK (role IN ('buyer', 'lead', 'warehouse', 'support')),
	password_hash TEXT NOT NULL,
	created_at    INTEGER NOT NULL
);

CREATE TABLE IF NOT EXISTS employee_sessions (
	token_hash  TEXT PRIMARY KEY,
	employee_id TEXT NOT NULL REFERENCES employees(id) ON DELETE CASCADE,
	expires_at  INTEGER NOT NULL,
	created_at  INTEGER NOT NULL
);
CREATE INDEX IF NOT EXISTS employee_sessions_by_employee ON employee_sessions(employee_id);

-- Shoppers as the workspace sees them. external_id links to the source:
-- "user:<id>" for Fitkit app accounts, or an id sent through the webhook.
CREATE TABLE IF NOT EXISTS clients (
	id          TEXT PRIMARY KEY,
	external_id TEXT UNIQUE,
	name        TEXT NOT NULL,
	phone       TEXT NOT NULL DEFAULT '',
	telegram    TEXT,
	city        TEXT NOT NULL DEFAULT '',
	branch      TEXT NOT NULL DEFAULT '',
	sizes       TEXT NOT NULL DEFAULT '',
	stores      TEXT NOT NULL DEFAULT '',
	dislikes    TEXT,
	note        TEXT,
	created_at  INTEGER NOT NULL
);

CREATE TABLE IF NOT EXISTS counters (
	name  TEXT PRIMARY KEY,
	value INTEGER NOT NULL
);
INSERT OR IGNORE INTO counters (name, value) VALUES ('order_number', 999);

-- Money is in kopiyky (1 UAH = 100).
CREATE TABLE IF NOT EXISTS orders (
	id          TEXT PRIMARY KEY,
	number      TEXT NOT NULL UNIQUE,
	external_id TEXT UNIQUE,
	client_id   TEXT NOT NULL REFERENCES clients(id),
	owner_id    TEXT REFERENCES employees(id),
	status      TEXT NOT NULL CHECK (status IN ('new', 'in_progress', 'awaiting', 'purchased', 'shipped', 'delivered', 'cancelled')),
	paid        INTEGER,
	fee         INTEGER NOT NULL DEFAULT 0,
	problem     TEXT,
	location    TEXT,
	photo       TEXT,
	created_at  INTEGER NOT NULL,
	due_at      INTEGER NOT NULL,
	updated_at  INTEGER NOT NULL
);
CREATE INDEX IF NOT EXISTS orders_by_status ON orders(status, due_at);
CREATE INDEX IF NOT EXISTS orders_by_owner ON orders(owner_id, status);
CREATE INDEX IF NOT EXISTS orders_by_client ON orders(client_id, created_at DESC);

CREATE TABLE IF NOT EXISTS order_items (
	id              TEXT PRIMARY KEY,
	order_id        TEXT NOT NULL REFERENCES orders(id) ON DELETE CASCADE,
	position        INTEGER NOT NULL,
	photo           TEXT NOT NULL DEFAULT '',
	name            TEXT NOT NULL,
	store           TEXT NOT NULL,
	url             TEXT NOT NULL,
	size            TEXT NOT NULL,
	qty             INTEGER NOT NULL DEFAULT 1 CHECK (qty > 0),
	price           INTEGER,
	store_price     INTEGER,
	listing_price   TEXT,
	comment         TEXT,
	status          TEXT NOT NULL CHECK (status IN ('pending', 'checked', 'sold_out', 'price_up', 'awaiting', 'bought', 'cancelled')),
	confirm_sent_at INTEGER,
	UNIQUE (order_id, position)
);

CREATE TABLE IF NOT EXISTS order_notes (
	id         TEXT PRIMARY KEY,
	order_id   TEXT NOT NULL REFERENCES orders(id) ON DELETE CASCADE,
	author_id  TEXT REFERENCES employees(id) ON DELETE SET NULL,
	author     TEXT NOT NULL,
	text       TEXT NOT NULL,
	created_at INTEGER NOT NULL
);
CREATE INDEX IF NOT EXISTS order_notes_by_order ON order_notes(order_id, created_at);

CREATE TABLE IF NOT EXISTS order_history (
	id       INTEGER PRIMARY KEY AUTOINCREMENT,
	order_id TEXT NOT NULL REFERENCES orders(id) ON DELETE CASCADE,
	who      TEXT NOT NULL,
	what     TEXT NOT NULL,
	at       INTEGER NOT NULL
);
CREATE INDEX IF NOT EXISTS order_history_by_order ON order_history(order_id, at);

CREATE TABLE IF NOT EXISTS shipments (
	id           TEXT PRIMARY KEY,
	order_id     TEXT NOT NULL REFERENCES orders(id) ON DELETE CASCADE,
	leg          TEXT NOT NULL CHECK (leg IN ('in', 'out')),
	store        TEXT,
	carrier      TEXT,
	tracking     TEXT,
	source       TEXT CHECK (source IN ('email', 'manual', 'api')),
	problem      TEXT,
	at_warehouse INTEGER NOT NULL DEFAULT 0,
	delivered    INTEGER NOT NULL DEFAULT 0,
	created_at   INTEGER NOT NULL
);
CREATE INDEX IF NOT EXISTS shipments_by_order ON shipments(order_id);
CREATE UNIQUE INDEX IF NOT EXISTS shipments_by_tracking ON shipments(tracking) WHERE tracking IS NOT NULL;

CREATE TABLE IF NOT EXISTS tracking_events (
	id          INTEGER PRIMARY KEY AUTOINCREMENT,
	shipment_id TEXT NOT NULL REFERENCES shipments(id) ON DELETE CASCADE,
	at          INTEGER NOT NULL,
	title       TEXT NOT NULL,
	detail      TEXT
);
CREATE INDEX IF NOT EXISTS tracking_events_by_shipment ON tracking_events(shipment_id, at);

CREATE TABLE IF NOT EXISTS tickets (
	id         TEXT PRIMARY KEY,
	client_id  TEXT NOT NULL REFERENCES clients(id),
	order_id   TEXT REFERENCES orders(id),
	status     TEXT NOT NULL CHECK (status IN ('open', 'resolved')),
	summary    TEXT NOT NULL,
	updated_at INTEGER NOT NULL
);
CREATE INDEX IF NOT EXISTS tickets_by_status ON tickets(status, updated_at DESC);

CREATE TABLE IF NOT EXISTS ticket_messages (
	id        TEXT PRIMARY KEY,
	ticket_id TEXT NOT NULL REFERENCES tickets(id) ON DELETE CASCADE,
	sender    TEXT NOT NULL CHECK (sender IN ('client', 'support', 'system')),
	author_id TEXT REFERENCES employees(id) ON DELETE SET NULL,
	text      TEXT NOT NULL,
	at        INTEGER NOT NULL
);
CREATE INDEX IF NOT EXISTS ticket_messages_by_ticket ON ticket_messages(ticket_id, at);

-- A client's complaint about an order, sent from the app. Counted against the
-- employee who had the order at that moment.
CREATE TABLE IF NOT EXISTS complaints (
	id          TEXT PRIMARY KEY,
	order_id    TEXT NOT NULL REFERENCES orders(id) ON DELETE CASCADE,
	employee_id TEXT REFERENCES employees(id),
	client_id   TEXT NOT NULL REFERENCES clients(id),
	text        TEXT NOT NULL,
	created_at  INTEGER NOT NULL
);
CREATE INDEX IF NOT EXISTS complaints_by_employee ON complaints(employee_id, created_at DESC);

-- Admin panel sign-ins. Admins themselves are a fixed list in the environment.
CREATE TABLE IF NOT EXISTS admin_sessions (
	token_hash TEXT PRIMARY KEY,
	login      TEXT NOT NULL,
	expires_at INTEGER NOT NULL,
	created_at INTEGER NOT NULL
);
`

// Migrate creates the workspace tables. Safe to run on every start.
func (s *Store) Migrate(ctx context.Context) error {
	if _, err := s.DB.ExecContext(ctx, schema); err != nil {
		return fmt.Errorf("workspace migrate: %w", err)
	}
	for _, c := range []struct{ table, column, def string }{
		{"employees", "is_team_lead", "INTEGER NOT NULL DEFAULT 0"},
		{"employees", "deleted_at", "INTEGER"},
	} {
		if err := s.addColumn(ctx, c.table, c.column, c.def); err != nil {
			return fmt.Errorf("workspace migrate: %w", err)
		}
	}
	// The old "lead" role is now a buyer who is a team lead.
	if _, err := s.DB.ExecContext(ctx, `UPDATE employees SET role = 'buyer', is_team_lead = 1 WHERE role = 'lead'`); err != nil {
		return fmt.Errorf("workspace migrate: %w", err)
	}
	return nil
}

// addColumn adds a column to a table created by an older schema.
func (s *Store) addColumn(ctx context.Context, table, column, definition string) error {
	var n int
	if err := s.DB.QueryRowContext(ctx, `SELECT count(*) FROM pragma_table_info(?) WHERE name = ?`, table, column).Scan(&n); err != nil {
		return err
	}
	if n > 0 {
		return nil
	}
	_, err := s.DB.ExecContext(ctx, `ALTER TABLE `+table+` ADD COLUMN `+column+` `+definition)
	return err
}

// Reset deletes all workspace data (tests and demo seeding only). App accounts, pins
// and looks are not touched.
func (s *Store) Reset(ctx context.Context) error {
	return s.Tx(ctx, func(q *Q) error {
		for _, t := range []string{"complaints", "admin_sessions", "ticket_messages", "tickets", "tracking_events", "shipments", "order_history", "order_notes", "order_items", "orders", "clients", "employee_sessions", "employees"} {
			if _, err := q.db.ExecContext(ctx, `DELETE FROM `+t); err != nil {
				return err
			}
		}
		_, err := q.db.ExecContext(ctx, `UPDATE counters SET value = 999 WHERE name = 'order_number'`)
		return err
	})
}

func notFound(err error) error {
	if errors.Is(err, sql.ErrNoRows) {
		return ErrNotFound
	}
	return err
}

// NewID returns a random 128-bit hex identifier (same format as the rest of Fitkit).
func NewID() string {
	var b [16]byte
	if _, err := rand.Read(b[:]); err != nil {
		panic(err)
	}
	return hex.EncodeToString(b[:])
}

func ms(t time.Time) int64 { return t.UnixMilli() }

func msPtr(t *time.Time) any {
	if t == nil {
		return nil
	}
	return t.UnixMilli()
}

// tm scans a millisecond timestamp into a time.Time.
type tm struct{ t *time.Time }

func (x tm) Scan(v any) error {
	n, ok := v.(int64)
	if !ok {
		return fmt.Errorf("timestamp: unexpected %T", v)
	}
	*x.t = time.UnixMilli(n).UTC()
	return nil
}

// tmPtr scans a nullable millisecond timestamp.
type tmPtr struct{ t **time.Time }

func (x tmPtr) Scan(v any) error {
	if v == nil {
		*x.t = nil
		return nil
	}
	n, ok := v.(int64)
	if !ok {
		return fmt.Errorf("timestamp: unexpected %T", v)
	}
	t := time.UnixMilli(n).UTC()
	*x.t = &t
	return nil
}

// in returns "(?, ?, …)" for n placeholders and the args as []any.
func in(ids []string) (string, []any) {
	args := make([]any, len(ids))
	for i, id := range ids {
		args[i] = id
	}
	return "(" + strings.TrimSuffix(strings.Repeat("?, ", len(ids)), ", ") + ")", args
}
