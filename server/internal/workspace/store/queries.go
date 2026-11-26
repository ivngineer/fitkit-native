package store

import (
	"context"
	"encoding/hex"
	"fmt"
	"strings"
	"time"

	"fitkit/server/internal/workspace/domain"
)

// ---------- employees & sessions

type EmployeeAuth struct {
	domain.Employee
	PasswordHash string
}

func (q *Q) CreateEmployee(ctx context.Context, name, email string, role domain.Role, hash string) (domain.Employee, error) {
	e := domain.Employee{ID: NewID(), Name: name, Email: strings.ToLower(email), Role: role}
	_, err := q.db.ExecContext(ctx, `INSERT INTO employees (id, name, email, role, password_hash, created_at) VALUES (?, ?, ?, ?, ?, ?)`,
		e.ID, e.Name, e.Email, e.Role, hash, ms(time.Now()))
	return e, err
}

func (q *Q) EmployeeByEmail(ctx context.Context, email string) (EmployeeAuth, error) {
	var e EmployeeAuth
	err := q.db.QueryRowContext(ctx, `SELECT id, name, email, role, password_hash FROM employees WHERE email = ?`, strings.ToLower(email)).
		Scan(&e.ID, &e.Name, &e.Email, &e.Role, &e.PasswordHash)
	return e, notFound(err)
}

func (q *Q) CountEmployees(ctx context.Context) (int, error) {
	var n int
	err := q.db.QueryRowContext(ctx, `SELECT count(*) FROM employees`).Scan(&n)
	return n, err
}

func (q *Q) Employees(ctx context.Context) ([]domain.Employee, error) {
	rows, err := q.db.QueryContext(ctx, `SELECT id, name, email, role FROM employees ORDER BY name`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []domain.Employee{}
	for rows.Next() {
		var e domain.Employee
		if err := rows.Scan(&e.ID, &e.Name, &e.Email, &e.Role); err != nil {
			return nil, err
		}
		out = append(out, e)
	}
	return out, rows.Err()
}

func (q *Q) CreateSession(ctx context.Context, tokenHash []byte, employeeID string, expires time.Time) error {
	_, err := q.db.ExecContext(ctx, `INSERT INTO employee_sessions (token_hash, employee_id, expires_at, created_at) VALUES (?, ?, ?, ?)`,
		hex.EncodeToString(tokenHash), employeeID, ms(expires), ms(time.Now()))
	return err
}

func (q *Q) SessionEmployee(ctx context.Context, tokenHash []byte) (domain.Employee, error) {
	var e domain.Employee
	err := q.db.QueryRowContext(ctx, `SELECT e.id, e.name, e.email, e.role FROM employee_sessions s JOIN employees e ON e.id = s.employee_id WHERE s.token_hash = ? AND s.expires_at > ?`,
		hex.EncodeToString(tokenHash), ms(time.Now())).Scan(&e.ID, &e.Name, &e.Email, &e.Role)
	return e, notFound(err)
}

func (q *Q) DeleteSession(ctx context.Context, tokenHash []byte) error {
	_, err := q.db.ExecContext(ctx, `DELETE FROM employee_sessions WHERE token_hash = ?`, hex.EncodeToString(tokenHash))
	return err
}

// ---------- clients

const clientCols = `id, name, phone, telegram, city, branch, sizes, stores, dislikes, note`

func scanClient(r interface{ Scan(...any) error }) (domain.Client, error) {
	var c domain.Client
	err := r.Scan(&c.ID, &c.Name, &c.Phone, &c.Telegram, &c.City, &c.Branch, &c.Sizes, &c.Stores, &c.Dislikes, &c.Note)
	return c, err
}

func (q *Q) Clients(ctx context.Context) ([]domain.Client, error) {
	rows, err := q.db.QueryContext(ctx, `SELECT `+clientCols+` FROM clients ORDER BY name`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []domain.Client{}
	for rows.Next() {
		c, err := scanClient(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, c)
	}
	return out, rows.Err()
}

func (q *Q) Client(ctx context.Context, id string) (domain.Client, error) {
	c, err := scanClient(q.db.QueryRowContext(ctx, `SELECT `+clientCols+` FROM clients WHERE id = ?`, id))
	return c, notFound(err)
}

// UpsertClient creates or updates a client by its external id (the Fitkit app account or a webhook id).
// Empty contact fields never overwrite ones already known.
func (q *Q) UpsertClient(ctx context.Context, externalID string, c domain.Client) (string, error) {
	var ext any
	if externalID != "" {
		ext = externalID
	}
	var id string
	err := q.db.QueryRowContext(ctx, `
		INSERT INTO clients (id, external_id, name, phone, telegram, city, branch, sizes, stores, dislikes, note, created_at)
		VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)
		ON CONFLICT (external_id) DO UPDATE SET
			name     = excluded.name,
			phone    = CASE WHEN excluded.phone  <> '' THEN excluded.phone  ELSE clients.phone  END,
			telegram = COALESCE(excluded.telegram, clients.telegram),
			city     = CASE WHEN excluded.city   <> '' THEN excluded.city   ELSE clients.city   END,
			branch   = CASE WHEN excluded.branch <> '' THEN excluded.branch ELSE clients.branch END,
			sizes    = CASE WHEN excluded.sizes  <> '' THEN excluded.sizes  ELSE clients.sizes  END,
			stores   = CASE WHEN excluded.stores <> '' THEN excluded.stores ELSE clients.stores END
		RETURNING id`,
		NewID(), ext, c.Name, c.Phone, c.Telegram, c.City, c.Branch, c.Sizes, c.Stores, c.Dislikes, c.Note, ms(time.Now())).Scan(&id)
	return id, err
}

func (q *Q) ClientIDByExternal(ctx context.Context, externalID string) (string, error) {
	var id string
	err := q.db.QueryRowContext(ctx, `SELECT id FROM clients WHERE external_id = ?`, externalID).Scan(&id)
	return id, notFound(err)
}

// ---------- orders

type NewOrder struct {
	Number     string // empty → next number from the counter
	ExternalID string
	ClientID   string
	OwnerID    *string
	Status     domain.OrderStatus
	Paid       *domain.Money
	Fee        domain.Money
	Problem    *string
	Location   *string
	Photo      *string
	CreatedAt  time.Time
	DueAt      time.Time
	Items      []domain.Item
}

func (q *Q) nextOrderNumber(ctx context.Context) (string, error) {
	var n int64
	if err := q.db.QueryRowContext(ctx, `UPDATE counters SET value = value + 1 WHERE name = 'order_number' RETURNING value`).Scan(&n); err != nil {
		return "", err
	}
	return fmt.Sprintf("#%04d", n), nil
}

func (q *Q) InsertOrder(ctx context.Context, o NewOrder) (string, string, error) {
	number := o.Number
	if number == "" {
		var err error
		if number, err = q.nextOrderNumber(ctx); err != nil {
			return "", "", err
		}
	}
	var ext any
	if o.ExternalID != "" {
		ext = o.ExternalID
	}
	id := NewID()
	_, err := q.db.ExecContext(ctx, `
		INSERT INTO orders (id, number, external_id, client_id, owner_id, status, paid, fee, problem, location, photo, created_at, due_at, updated_at)
		VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)`,
		id, number, ext, o.ClientID, o.OwnerID, o.Status, o.Paid, o.Fee, o.Problem, o.Location, o.Photo, ms(o.CreatedAt), ms(o.DueAt), ms(o.CreatedAt))
	if err != nil {
		return "", "", err
	}
	for i, it := range o.Items {
		if _, err := q.db.ExecContext(ctx, `
			INSERT INTO order_items (id, order_id, position, photo, name, store, url, size, qty, price, store_price, listing_price, comment, status, confirm_sent_at)
			VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)`,
			NewID(), id, i, it.Photo, it.Name, it.Store, it.URL, it.Size, it.Qty, it.Price, it.StorePrice, it.ListingPrice, it.Comment, it.Status, msPtr(it.ConfirmSentAt)); err != nil {
			return "", "", err
		}
	}
	return id, number, nil
}

const orderCols = `id, number, client_id, owner_id, status, paid, fee, problem, location, photo, created_at, due_at`

func scanOrder(r interface{ Scan(...any) error }) (domain.Order, error) {
	var o domain.Order
	err := r.Scan(&o.ID, &o.Number, &o.ClientID, &o.OwnerID, &o.Status, &o.Paid, &o.Fee, &o.Problem, &o.Location, &o.Photo, tm{&o.CreatedAt}, tm{&o.DueAt})
	return o, err
}

type OrderFilter struct {
	Statuses []domain.OrderStatus
	OwnerID  string
	ClientID string
	IDs      []string
}

// Orders lists orders with items, notes and history.
func (q *Q) Orders(ctx context.Context, f OrderFilter) ([]domain.Order, error) {
	where := []string{"1 = 1"}
	var args []any
	if len(f.Statuses) > 0 {
		ss := make([]string, len(f.Statuses))
		for i, s := range f.Statuses {
			ss[i] = string(s)
		}
		ph, a := in(ss)
		where = append(where, "status IN "+ph)
		args = append(args, a...)
	}
	if f.OwnerID != "" {
		where = append(where, "owner_id = ?")
		args = append(args, f.OwnerID)
	}
	if f.ClientID != "" {
		where = append(where, "client_id = ?")
		args = append(args, f.ClientID)
	}
	if len(f.IDs) > 0 {
		ph, a := in(f.IDs)
		where = append(where, "id IN "+ph)
		args = append(args, a...)
	}
	rows, err := q.db.QueryContext(ctx, `SELECT `+orderCols+` FROM orders WHERE `+strings.Join(where, " AND ")+` ORDER BY due_at`, args...)
	if err != nil {
		return nil, err
	}
	orders := []domain.Order{}
	for rows.Next() {
		o, err := scanOrder(rows)
		if err != nil {
			rows.Close()
			return nil, err
		}
		orders = append(orders, o)
	}
	rows.Close()
	if err := rows.Err(); err != nil {
		return nil, err
	}
	return orders, q.loadChildren(ctx, orders)
}

// Order loads one order. lock is kept for API compatibility: SQLite transactions
// already hold the only connection, so reads inside Tx are serialized.
func (q *Q) Order(ctx context.Context, id string, lock bool) (domain.Order, error) {
	_ = lock
	o, err := scanOrder(q.db.QueryRowContext(ctx, `SELECT `+orderCols+` FROM orders WHERE id = ?`, id))
	if err != nil {
		return o, notFound(err)
	}
	list := []domain.Order{o}
	err = q.loadChildren(ctx, list)
	return list[0], err
}

func (q *Q) OrderIDByExternal(ctx context.Context, externalID string) (string, error) {
	var id string
	err := q.db.QueryRowContext(ctx, `SELECT id FROM orders WHERE external_id = ?`, externalID).Scan(&id)
	return id, notFound(err)
}

func (q *Q) loadChildren(ctx context.Context, orders []domain.Order) error {
	if len(orders) == 0 {
		return nil
	}
	ids := make([]string, len(orders))
	idx := make(map[string]int, len(orders))
	for i, o := range orders {
		ids[i] = o.ID
		idx[o.ID] = i
		orders[i].Items, orders[i].Notes, orders[i].History = []domain.Item{}, []domain.Note{}, []domain.HistoryEvent{}
	}
	ph, args := in(ids)

	rows, err := q.db.QueryContext(ctx, `SELECT order_id, id, photo, name, store, url, size, qty, price, store_price, listing_price, comment, status, confirm_sent_at FROM order_items WHERE order_id IN `+ph+` ORDER BY position`, args...)
	if err != nil {
		return err
	}
	for rows.Next() {
		var oid string
		var it domain.Item
		if err := rows.Scan(&oid, &it.ID, &it.Photo, &it.Name, &it.Store, &it.URL, &it.Size, &it.Qty, &it.Price, &it.StorePrice, &it.ListingPrice, &it.Comment, &it.Status, tmPtr{&it.ConfirmSentAt}); err != nil {
			rows.Close()
			return err
		}
		orders[idx[oid]].Items = append(orders[idx[oid]].Items, it)
	}
	rows.Close()
	if err := rows.Err(); err != nil {
		return err
	}

	rows, err = q.db.QueryContext(ctx, `SELECT order_id, id, author, created_at, text FROM order_notes WHERE order_id IN `+ph+` ORDER BY created_at`, args...)
	if err != nil {
		return err
	}
	for rows.Next() {
		var oid string
		var n domain.Note
		if err := rows.Scan(&oid, &n.ID, &n.Author, tm{&n.At}, &n.Text); err != nil {
			rows.Close()
			return err
		}
		orders[idx[oid]].Notes = append(orders[idx[oid]].Notes, n)
	}
	rows.Close()
	if err := rows.Err(); err != nil {
		return err
	}

	rows, err = q.db.QueryContext(ctx, `SELECT order_id, at, who, what FROM order_history WHERE order_id IN `+ph+` ORDER BY at, id`, args...)
	if err != nil {
		return err
	}
	for rows.Next() {
		var oid string
		var h domain.HistoryEvent
		if err := rows.Scan(&oid, tm{&h.At}, &h.Who, &h.What); err != nil {
			rows.Close()
			return err
		}
		orders[idx[oid]].History = append(orders[idx[oid]].History, h)
	}
	rows.Close()
	return rows.Err()
}

// TakeOrder assigns a new, unassigned order atomically. ok=false if someone was faster.
func (q *Q) TakeOrder(ctx context.Context, id, employeeID string, due time.Time) (bool, error) {
	res, err := q.db.ExecContext(ctx, `UPDATE orders SET owner_id = ?, status = 'in_progress', due_at = ?, updated_at = ? WHERE id = ? AND status = 'new' AND owner_id IS NULL`,
		employeeID, ms(due), ms(time.Now()), id)
	if err != nil {
		return false, err
	}
	n, err := res.RowsAffected()
	return n == 1, err
}

// OldestNewOrder returns the id of the oldest unassigned request.
func (q *Q) OldestNewOrder(ctx context.Context) (string, error) {
	var id string
	err := q.db.QueryRowContext(ctx, `SELECT id FROM orders WHERE status = 'new' AND owner_id IS NULL ORDER BY created_at LIMIT 1`).Scan(&id)
	return id, notFound(err)
}

// SaveOrder writes the mutable order fields.
func (q *Q) SaveOrder(ctx context.Context, o domain.Order) error {
	_, err := q.db.ExecContext(ctx, `UPDATE orders SET owner_id = ?, status = ?, problem = ?, location = ?, due_at = ?, updated_at = ? WHERE id = ?`,
		o.OwnerID, o.Status, o.Problem, o.Location, ms(o.DueAt), ms(time.Now()), o.ID)
	return err
}

func (q *Q) SaveItem(ctx context.Context, it domain.Item) error {
	_, err := q.db.ExecContext(ctx, `UPDATE order_items SET price = ?, store_price = ?, status = ?, confirm_sent_at = ? WHERE id = ?`,
		it.Price, it.StorePrice, it.Status, msPtr(it.ConfirmSentAt), it.ID)
	return err
}

func (q *Q) AddHistory(ctx context.Context, orderID, who, what string) error {
	return q.AddHistoryAt(ctx, orderID, who, what, time.Now())
}

func (q *Q) AddHistoryAt(ctx context.Context, orderID, who, what string, at time.Time) error {
	_, err := q.db.ExecContext(ctx, `INSERT INTO order_history (order_id, who, what, at) VALUES (?, ?, ?, ?)`, orderID, who, what, ms(at))
	return err
}

func (q *Q) AddNote(ctx context.Context, orderID string, authorID *string, author, text string, at time.Time) (domain.Note, error) {
	n := domain.Note{ID: NewID(), Author: author, Text: text, At: at}
	_, err := q.db.ExecContext(ctx, `INSERT INTO order_notes (id, order_id, author_id, author, text, created_at) VALUES (?, ?, ?, ?, ?, ?)`,
		n.ID, orderID, authorID, author, text, ms(at))
	return n, err
}

// AwaitingExpired lists (order id, item id) of price confirmations older than cutoff.
func (q *Q) AwaitingExpired(ctx context.Context, cutoff time.Time) ([][2]string, error) {
	rows, err := q.db.QueryContext(ctx, `SELECT order_id, id FROM order_items WHERE status = 'awaiting' AND confirm_sent_at < ?`, ms(cutoff))
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out [][2]string
	for rows.Next() {
		var p [2]string
		if err := rows.Scan(&p[0], &p[1]); err != nil {
			return nil, err
		}
		out = append(out, p)
	}
	return out, rows.Err()
}

// ---------- shipments

const shipmentCols = `id, order_id, leg, store, carrier, tracking, source, problem, at_warehouse, delivered, created_at`

func scanShipment(r interface{ Scan(...any) error }) (domain.Shipment, error) {
	var s domain.Shipment
	err := r.Scan(&s.ID, &s.OrderID, &s.Leg, &s.Store, &s.Carrier, &s.Tracking, &s.Source, &s.Problem, &s.AtWarehouse, &s.Delivered, tm{&s.CreatedAt})
	return s, err
}

func (q *Q) Shipments(ctx context.Context, orderID string) ([]domain.Shipment, error) {
	query := `SELECT ` + shipmentCols + ` FROM shipments`
	var args []any
	if orderID != "" {
		query += ` WHERE order_id = ?`
		args = append(args, orderID)
	}
	rows, err := q.db.QueryContext(ctx, query+` ORDER BY created_at`, args...)
	if err != nil {
		return nil, err
	}
	list := []domain.Shipment{}
	for rows.Next() {
		s, err := scanShipment(rows)
		if err != nil {
			rows.Close()
			return nil, err
		}
		s.Events = []domain.TrackingEvent{}
		list = append(list, s)
	}
	rows.Close()
	if err := rows.Err(); err != nil || len(list) == 0 {
		return list, err
	}
	ids := make([]string, len(list))
	idx := map[string]int{}
	for i, s := range list {
		ids[i] = s.ID
		idx[s.ID] = i
	}
	ph, args := in(ids)
	rows, err = q.db.QueryContext(ctx, `SELECT shipment_id, at, title, detail FROM tracking_events WHERE shipment_id IN `+ph+` ORDER BY at, id`, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	for rows.Next() {
		var sid string
		var e domain.TrackingEvent
		if err := rows.Scan(&sid, tm{&e.At}, &e.Title, &e.Detail); err != nil {
			return nil, err
		}
		list[idx[sid]].Events = append(list[idx[sid]].Events, e)
	}
	return list, rows.Err()
}

func (q *Q) Shipment(ctx context.Context, id string) (domain.Shipment, error) {
	s, err := scanShipment(q.db.QueryRowContext(ctx, `SELECT `+shipmentCols+` FROM shipments WHERE id = ?`, id))
	return s, notFound(err)
}

func (q *Q) ShipmentByTracking(ctx context.Context, tracking string) (domain.Shipment, error) {
	s, err := scanShipment(q.db.QueryRowContext(ctx, `SELECT `+shipmentCols+` FROM shipments WHERE replace(tracking, ' ', '') = replace(?, ' ', '')`, tracking))
	return s, notFound(err)
}

func (q *Q) InsertShipment(ctx context.Context, s domain.Shipment) (string, error) {
	created := s.CreatedAt
	if created.IsZero() {
		created = time.Now()
	}
	id := NewID()
	_, err := q.db.ExecContext(ctx, `INSERT INTO shipments (id, order_id, leg, store, carrier, tracking, source, problem, at_warehouse, delivered, created_at) VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)`,
		id, s.OrderID, s.Leg, s.Store, s.Carrier, s.Tracking, s.Source, s.Problem, s.AtWarehouse, s.Delivered, ms(created))
	return id, err
}

func (q *Q) SaveShipment(ctx context.Context, s domain.Shipment) error {
	_, err := q.db.ExecContext(ctx, `UPDATE shipments SET carrier = ?, tracking = ?, source = ?, problem = ?, at_warehouse = ?, delivered = ? WHERE id = ?`,
		s.Carrier, s.Tracking, s.Source, s.Problem, s.AtWarehouse, s.Delivered, s.ID)
	return err
}

func (q *Q) AddTrackingEvent(ctx context.Context, shipmentID string, e domain.TrackingEvent) error {
	if e.At.IsZero() {
		e.At = time.Now()
	}
	_, err := q.db.ExecContext(ctx, `INSERT INTO tracking_events (shipment_id, at, title, detail) VALUES (?, ?, ?, ?)`, shipmentID, ms(e.At), e.Title, e.Detail)
	return err
}

// StalledInbound returns store→warehouse parcels to flag: no tracking after noTrackingCutoff,
// or no tracking event since noMoveCutoff. Already-flagged parcels are skipped.
func (q *Q) StalledInbound(ctx context.Context, noTrackingCutoff, noMoveCutoff time.Time) (noTracking, noMove []string, err error) {
	rows, err := q.db.QueryContext(ctx, `
		SELECT s.id, s.tracking IS NULL
		FROM shipments s
		WHERE s.leg = 'in' AND s.at_warehouse = 0 AND s.problem IS NULL AND (
			(s.tracking IS NULL AND s.created_at < ?) OR
			(s.tracking IS NOT NULL AND COALESCE((SELECT max(at) FROM tracking_events e WHERE e.shipment_id = s.id), s.created_at) < ?))`,
		ms(noTrackingCutoff), ms(noMoveCutoff))
	if err != nil {
		return nil, nil, err
	}
	defer rows.Close()
	for rows.Next() {
		var id string
		var missing bool
		if err := rows.Scan(&id, &missing); err != nil {
			return nil, nil, err
		}
		if missing {
			noTracking = append(noTracking, id)
		} else {
			noMove = append(noMove, id)
		}
	}
	return noTracking, noMove, rows.Err()
}

// ---------- tickets

func (q *Q) Tickets(ctx context.Context, status string) ([]domain.Ticket, error) {
	query := `SELECT id, client_id, order_id, status, summary, updated_at FROM tickets`
	var args []any
	if status != "" {
		query += ` WHERE status = ?`
		args = append(args, status)
	}
	rows, err := q.db.QueryContext(ctx, query+` ORDER BY updated_at DESC`, args...)
	if err != nil {
		return nil, err
	}
	list := []domain.Ticket{}
	for rows.Next() {
		var t domain.Ticket
		if err := rows.Scan(&t.ID, &t.ClientID, &t.OrderID, &t.Status, &t.Summary, tm{&t.UpdatedAt}); err != nil {
			rows.Close()
			return nil, err
		}
		t.Messages = []domain.Message{}
		list = append(list, t)
	}
	rows.Close()
	if err := rows.Err(); err != nil || len(list) == 0 {
		return list, err
	}
	ids := make([]string, len(list))
	idx := map[string]int{}
	for i, t := range list {
		ids[i] = t.ID
		idx[t.ID] = i
	}
	ph, args := in(ids)
	rows, err = q.db.QueryContext(ctx, `SELECT ticket_id, id, sender, text, at FROM ticket_messages WHERE ticket_id IN `+ph+` ORDER BY at, rowid`, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	for rows.Next() {
		var tid string
		var m domain.Message
		if err := rows.Scan(&tid, &m.ID, &m.From, &m.Text, tm{&m.At}); err != nil {
			return nil, err
		}
		list[idx[tid]].Messages = append(list[idx[tid]].Messages, m)
	}
	return list, rows.Err()
}

func (q *Q) Ticket(ctx context.Context, id string) (domain.Ticket, error) {
	var t domain.Ticket
	err := q.db.QueryRowContext(ctx, `SELECT id, client_id, order_id, status, summary, updated_at FROM tickets WHERE id = ?`, id).
		Scan(&t.ID, &t.ClientID, &t.OrderID, &t.Status, &t.Summary, tm{&t.UpdatedAt})
	return t, notFound(err)
}

// OpenTicketFor returns the open ticket of a client for an order, if any.
func (q *Q) OpenTicketFor(ctx context.Context, clientID string, orderID *string) (string, error) {
	var id string
	err := q.db.QueryRowContext(ctx, `SELECT id FROM tickets WHERE client_id = ? AND status = 'open' AND order_id IS ? ORDER BY updated_at DESC LIMIT 1`, clientID, orderID).Scan(&id)
	return id, notFound(err)
}

func (q *Q) InsertTicket(ctx context.Context, t domain.Ticket) (string, error) {
	at := t.UpdatedAt
	if at.IsZero() {
		at = time.Now()
	}
	id := NewID()
	_, err := q.db.ExecContext(ctx, `INSERT INTO tickets (id, client_id, order_id, status, summary, updated_at) VALUES (?, ?, ?, ?, ?, ?)`,
		id, t.ClientID, t.OrderID, t.Status, t.Summary, ms(at))
	return id, err
}

func (q *Q) AddMessage(ctx context.Context, ticketID, sender string, authorID *string, text string, at time.Time) (domain.Message, error) {
	m := domain.Message{ID: NewID(), From: sender, Text: text, At: at}
	if _, err := q.db.ExecContext(ctx, `INSERT INTO ticket_messages (id, ticket_id, sender, author_id, text, at) VALUES (?, ?, ?, ?, ?, ?)`,
		m.ID, ticketID, sender, authorID, text, ms(at)); err != nil {
		return m, err
	}
	_, err := q.db.ExecContext(ctx, `UPDATE tickets SET updated_at = max(updated_at, ?) WHERE id = ?`, ms(at), ticketID)
	return m, err
}

func (q *Q) SetTicket(ctx context.Context, id, status, summary string) error {
	_, err := q.db.ExecContext(ctx, `UPDATE tickets SET status = ?, summary = ?, updated_at = ? WHERE id = ?`, status, summary, ms(time.Now()), id)
	return err
}
