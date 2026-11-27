package store

import (
	"context"
	"encoding/hex"
	"time"
)

// ---------- complaints

func (q *Q) AddComplaint(ctx context.Context, orderID string, employeeID *string, clientID, text string, at time.Time) (string, error) {
	id := NewID()
	_, err := q.db.ExecContext(ctx, `INSERT INTO complaints (id, order_id, employee_id, client_id, text, created_at) VALUES (?, ?, ?, ?, ?, ?)`,
		id, orderID, employeeID, clientID, text, ms(at))
	return id, err
}

type Complaint struct {
	ID          string    `json:"id"`
	OrderID     string    `json:"orderId"`
	OrderNumber string    `json:"orderNumber"`
	ClientName  string    `json:"clientName"`
	Text        string    `json:"text"`
	At          time.Time `json:"at"`
}

// EmployeeComplaints lists complaints counted against an employee, newest first.
func (q *Q) EmployeeComplaints(ctx context.Context, employeeID string, limit int) ([]Complaint, error) {
	rows, err := q.db.QueryContext(ctx, `
		SELECT c.id, c.order_id, o.number, cl.name, c.text, c.created_at
		FROM complaints c JOIN orders o ON o.id = c.order_id JOIN clients cl ON cl.id = c.client_id
		WHERE c.employee_id = ? ORDER BY c.created_at DESC LIMIT ?`, employeeID, limit)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []Complaint{}
	for rows.Next() {
		var c Complaint
		if err := rows.Scan(&c.ID, &c.OrderID, &c.OrderNumber, &c.ClientName, &c.Text, tm{&c.At}); err != nil {
			return nil, err
		}
		out = append(out, c)
	}
	return out, rows.Err()
}

// ---------- statistics

// EmployeeStats are an employee's counters for the admin panel.
type EmployeeStats struct {
	Completed  int `json:"completed"`  // delivered orders they owned
	InProgress int `json:"inProgress"` // in progress or waiting for the client
	OnTheWay   int `json:"onTheWay"`   // purchased or shipped, not delivered yet
	Cancelled  int `json:"cancelled"`
	Complaints int `json:"complaints"`
}

// AllEmployeeStats returns counters for every active employee, keyed by id.
func (q *Q) AllEmployeeStats(ctx context.Context) (map[string]EmployeeStats, error) {
	rows, err := q.db.QueryContext(ctx, `
		SELECT e.id,
			(SELECT count(*) FROM orders o WHERE o.owner_id = e.id AND o.status = 'delivered'),
			(SELECT count(*) FROM orders o WHERE o.owner_id = e.id AND o.status IN ('in_progress', 'awaiting')),
			(SELECT count(*) FROM orders o WHERE o.owner_id = e.id AND o.status IN ('purchased', 'shipped')),
			(SELECT count(*) FROM orders o WHERE o.owner_id = e.id AND o.status = 'cancelled'),
			(SELECT count(*) FROM complaints c WHERE c.employee_id = e.id)
		FROM employees e WHERE e.deleted_at IS NULL`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := map[string]EmployeeStats{}
	for rows.Next() {
		var id string
		var st EmployeeStats
		if err := rows.Scan(&id, &st.Completed, &st.InProgress, &st.OnTheWay, &st.Cancelled, &st.Complaints); err != nil {
			return nil, err
		}
		out[id] = st
	}
	return out, rows.Err()
}

type OrderBrief struct {
	ID         string    `json:"id"`
	Number     string    `json:"number"`
	Status     string    `json:"status"`
	ClientName string    `json:"clientName"`
	CreatedAt  time.Time `json:"createdAt"`
}

// EmployeeOrders lists the newest orders an employee owns.
func (q *Q) EmployeeOrders(ctx context.Context, employeeID string, limit int) ([]OrderBrief, error) {
	rows, err := q.db.QueryContext(ctx, `
		SELECT o.id, o.number, o.status, cl.name, o.created_at FROM orders o JOIN clients cl ON cl.id = o.client_id
		WHERE o.owner_id = ? ORDER BY o.created_at DESC LIMIT ?`, employeeID, limit)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []OrderBrief{}
	for rows.Next() {
		var o OrderBrief
		if err := rows.Scan(&o.ID, &o.Number, &o.Status, &o.ClientName, tm{&o.CreatedAt}); err != nil {
			return nil, err
		}
		out = append(out, o)
	}
	return out, rows.Err()
}

// OrderTotals counts all orders by status, plus complaints.
func (q *Q) OrderTotals(ctx context.Context) (map[string]int, error) {
	rows, err := q.db.QueryContext(ctx, `SELECT status, count(*) FROM orders GROUP BY status UNION ALL SELECT 'complaints', count(*) FROM complaints`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := map[string]int{}
	for rows.Next() {
		var k string
		var n int
		if err := rows.Scan(&k, &n); err != nil {
			return nil, err
		}
		out[k] = n
	}
	return out, rows.Err()
}

// ActiveOrdersOf lists the ids of orders an employee is still working on (before purchase).
func (q *Q) ActiveOrdersOf(ctx context.Context, employeeID string) ([]string, error) {
	rows, err := q.db.QueryContext(ctx, `SELECT id FROM orders WHERE owner_id = ? AND status IN ('in_progress', 'awaiting')`, employeeID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []string
	for rows.Next() {
		var id string
		if err := rows.Scan(&id); err != nil {
			return nil, err
		}
		out = append(out, id)
	}
	return out, rows.Err()
}

// ---------- admin sessions

func (q *Q) CreateAdminSession(ctx context.Context, tokenHash []byte, login string, expires time.Time) error {
	_, err := q.db.ExecContext(ctx, `INSERT INTO admin_sessions (token_hash, login, expires_at, created_at) VALUES (?, ?, ?, ?)`,
		hex.EncodeToString(tokenHash), login, ms(expires), ms(time.Now()))
	return err
}

func (q *Q) AdminSession(ctx context.Context, tokenHash []byte) (string, error) {
	var login string
	err := q.db.QueryRowContext(ctx, `SELECT login FROM admin_sessions WHERE token_hash = ? AND expires_at > ?`, hex.EncodeToString(tokenHash), ms(time.Now())).Scan(&login)
	return login, notFound(err)
}

func (q *Q) DeleteAdminSession(ctx context.Context, tokenHash []byte) error {
	_, err := q.db.ExecContext(ctx, `DELETE FROM admin_sessions WHERE token_hash = ?`, hex.EncodeToString(tokenHash))
	return err
}
