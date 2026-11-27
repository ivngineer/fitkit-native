package service

import (
	"context"
	"errors"
	"net/http"
	"net/mail"
	"strings"
	"unicode/utf8"

	"golang.org/x/crypto/bcrypt"

	"fitkit/server/internal/workspace/domain"
	"fitkit/server/internal/workspace/store"
)

// MinPasswordLength for employee passwords set in the admin panel.
const MinPasswordLength = 10

// Transfer hands an order to another employee. Only team leads may do it.
func (s *Service) Transfer(ctx context.Context, actor domain.Employee, orderID, toID string) error {
	if !actor.IsTeamLead {
		return errf(http.StatusForbidden, "Передавати замовлення може лише тімлід")
	}
	return s.run(ctx, func(q *store.Q, op *op) error {
		o, err := q.Order(ctx, orderID, true)
		if err != nil {
			return err
		}
		to, err := q.EmployeeByID(ctx, toID)
		if errors.Is(err, store.ErrNotFound) {
			return errf(http.StatusBadRequest, "Такого співробітника немає")
		}
		if err != nil {
			return err
		}
		if o.Status == domain.StatusCancelled || o.Status == domain.StatusDelivered {
			return errf(http.StatusConflict, "Завершене замовлення не передають")
		}
		if o.OwnerID != nil && *o.OwnerID == to.ID {
			return nil
		}
		o.OwnerID = &to.ID
		if o.Status == domain.StatusNew {
			o.Status = domain.StatusInProgress
			o.DueAt = s.now().Add(domain.DueAfter[domain.StatusInProgress])
		}
		if err := q.SaveOrder(ctx, o); err != nil {
			return err
		}
		op.emit("order", o.ID)
		return q.AddHistory(ctx, o.ID, actor.Name, "Передано: "+to.Name)
	})
}

// Complain records a client's complaint about their order from the app. It counts
// against the employee who has the order now.
func (s *Service) Complain(ctx context.Context, orderID, text string) (string, error) {
	text = strings.TrimSpace(text)
	if text == "" {
		return "", errf(http.StatusBadRequest, "Опишіть проблему")
	}
	var id string
	err := s.run(ctx, func(q *store.Q, _ *op) error {
		o, err := q.Order(ctx, orderID, false)
		if err != nil {
			return err
		}
		id, err = q.AddComplaint(ctx, o.ID, o.OwnerID, o.ClientID, text, s.now())
		return err
	})
	return id, err
}

// ---------- employee management (admin panel)

type EmployeeInput struct {
	Name       string      `json:"name"`
	Email      string      `json:"email"`
	Role       domain.Role `json:"role"`
	IsTeamLead bool        `json:"isTeamLead"`
	Password   string      `json:"password,omitempty"`
}

func (in *EmployeeInput) clean() error {
	in.Name = strings.TrimSpace(in.Name)
	in.Email = strings.ToLower(strings.TrimSpace(in.Email))
	if in.Name == "" || utf8.RuneCountInString(in.Name) > 80 {
		return errf(http.StatusBadRequest, "Вкажіть ім'я (до 80 символів)")
	}
	if a, err := mail.ParseAddress(in.Email); err != nil || a.Address != in.Email {
		return errf(http.StatusBadRequest, "Некоректна пошта")
	}
	if in.Role == "" {
		in.Role = domain.RoleBuyer
	}
	if !in.Role.Valid() {
		return errf(http.StatusBadRequest, "Невідома роль")
	}
	return nil
}

func hashPassword(p string) (string, error) {
	if utf8.RuneCountInString(p) < MinPasswordLength {
		return "", errf(http.StatusBadRequest, "Пароль має бути не коротший за 10 символів")
	}
	if len(p) > 72 {
		return "", errf(http.StatusBadRequest, "Пароль задовгий (до 72 байт)")
	}
	h, err := bcrypt.GenerateFromPassword([]byte(p), bcrypt.DefaultCost)
	return string(h), err
}

func (s *Service) CreateEmployee(ctx context.Context, in EmployeeInput) (domain.Employee, error) {
	if err := in.clean(); err != nil {
		return domain.Employee{}, err
	}
	hash, err := hashPassword(in.Password)
	if err != nil {
		return domain.Employee{}, err
	}
	var e domain.Employee
	err = s.Store.Tx(ctx, func(q *store.Q) error {
		var err error
		if e, err = q.CreateEmployee(ctx, in.Name, in.Email, in.Role, hash); err != nil {
			return err
		}
		e.IsTeamLead = in.IsTeamLead
		return q.SetTeamLead(ctx, e.ID, in.IsTeamLead)
	})
	if errors.Is(err, store.ErrConflict) {
		return e, errf(http.StatusConflict, "Співробітник з такою поштою вже є")
	}
	return e, err
}

func (s *Service) UpdateEmployee(ctx context.Context, id string, in EmployeeInput) (domain.Employee, error) {
	if err := in.clean(); err != nil {
		return domain.Employee{}, err
	}
	e := domain.Employee{ID: id, Name: in.Name, Email: in.Email, Role: in.Role, IsTeamLead: in.IsTeamLead}
	err := s.Store.Q().UpdateEmployee(ctx, e)
	switch {
	case errors.Is(err, store.ErrConflict):
		return e, errf(http.StatusConflict, "Співробітник з такою поштою вже є")
	case errors.Is(err, store.ErrNotFound):
		return e, ErrNotFound
	}
	return e, err
}

// SetEmployeePassword sets a new password and signs the employee out everywhere.
func (s *Service) SetEmployeePassword(ctx context.Context, id, password string) error {
	hash, err := hashPassword(password)
	if err != nil {
		return err
	}
	if err := s.Store.Q().SetPassword(ctx, id, hash); errors.Is(err, store.ErrNotFound) {
		return ErrNotFound
	} else if err != nil {
		return err
	}
	return nil
}

// DeleteEmployee removes an employee's access. Orders they were still working on
// (before purchase) go back to the queue; everything else keeps them as owner for history.
func (s *Service) DeleteEmployee(ctx context.Context, admin, id string) error {
	return s.run(ctx, func(q *store.Q, op *op) error {
		e, err := q.EmployeeByID(ctx, id)
		if err != nil {
			return err
		}
		active, err := q.ActiveOrdersOf(ctx, id)
		if err != nil {
			return err
		}
		for _, oid := range active {
			o, err := q.Order(ctx, oid, true)
			if err != nil {
				return err
			}
			o.OwnerID = nil
			o.Status = domain.StatusNew
			o.DueAt = s.now().Add(domain.DueAfter[domain.StatusNew])
			if err := q.SaveOrder(ctx, o); err != nil {
				return err
			}
			if err := q.AddHistory(ctx, o.ID, "Адміністратор "+admin, "Повернуто в чергу: доступ "+e.Name+" закрито"); err != nil {
				return err
			}
			op.emit("order", o.ID)
		}
		return q.DeleteEmployee(ctx, id)
	})
}

// EmployeeView is an employee with counters for the admin panel.
type EmployeeView struct {
	domain.Employee
	Stats store.EmployeeStats `json:"stats"`
}

func (s *Service) EmployeesWithStats(ctx context.Context) ([]EmployeeView, error) {
	q := s.Store.Q()
	list, err := q.Employees(ctx)
	if err != nil {
		return nil, err
	}
	stats, err := q.AllEmployeeStats(ctx)
	if err != nil {
		return nil, err
	}
	out := make([]EmployeeView, len(list))
	for i, e := range list {
		out[i] = EmployeeView{Employee: e, Stats: stats[e.ID]}
	}
	return out, nil
}

type EmployeeDetail struct {
	EmployeeView
	Orders     []store.OrderBrief `json:"orders"`
	Complaints []store.Complaint  `json:"complaints"`
}

func (s *Service) EmployeeDetail(ctx context.Context, id string) (EmployeeDetail, error) {
	q := s.Store.Q()
	e, err := q.EmployeeByID(ctx, id)
	if errors.Is(err, store.ErrNotFound) {
		return EmployeeDetail{}, ErrNotFound
	}
	if err != nil {
		return EmployeeDetail{}, err
	}
	stats, err := q.AllEmployeeStats(ctx)
	if err != nil {
		return EmployeeDetail{}, err
	}
	d := EmployeeDetail{EmployeeView: EmployeeView{Employee: e, Stats: stats[e.ID]}}
	if d.Orders, err = q.EmployeeOrders(ctx, id, 20); err != nil {
		return d, err
	}
	d.Complaints, err = q.EmployeeComplaints(ctx, id, 50)
	return d, err
}

type LeaderboardRow struct {
	Employee domain.Employee `json:"employee"`
	Count    int             `json:"count"`
}

type Analytics struct {
	Totals struct {
		Employees  int `json:"employees"`
		Orders     int `json:"orders"`
		Delivered  int `json:"delivered"`
		InProgress int `json:"inProgress"`
		Complaints int `json:"complaints"`
	} `json:"totals"`
	// Leaderboard ranks employees by delivered orders (most first).
	Leaderboard []LeaderboardRow `json:"leaderboard"`
	// MostComplaints ranks employees with at least one complaint (most first).
	MostComplaints []LeaderboardRow `json:"mostComplaints"`
}

func (s *Service) Analytics(ctx context.Context) (Analytics, error) {
	var a Analytics
	list, err := s.EmployeesWithStats(ctx)
	if err != nil {
		return a, err
	}
	totals, err := s.Store.Q().OrderTotals(ctx)
	if err != nil {
		return a, err
	}
	a.Totals.Employees = len(list)
	for k, n := range totals {
		switch k {
		case "complaints":
			a.Totals.Complaints = n
		case string(domain.StatusDelivered):
			a.Totals.Delivered = n
			a.Totals.Orders += n
		case string(domain.StatusInProgress), string(domain.StatusAwaiting):
			a.Totals.InProgress += n
			a.Totals.Orders += n
		default:
			a.Totals.Orders += n
		}
	}
	a.Leaderboard = []LeaderboardRow{}
	a.MostComplaints = []LeaderboardRow{}
	for _, e := range list {
		a.Leaderboard = append(a.Leaderboard, LeaderboardRow{e.Employee, e.Stats.Completed})
		if e.Stats.Complaints > 0 {
			a.MostComplaints = append(a.MostComplaints, LeaderboardRow{e.Employee, e.Stats.Complaints})
		}
	}
	byCount := func(rows []LeaderboardRow) {
		for i := 1; i < len(rows); i++ {
			for j := i; j > 0 && (rows[j].Count > rows[j-1].Count || (rows[j].Count == rows[j-1].Count && rows[j].Employee.Name < rows[j-1].Employee.Name)); j-- {
				rows[j], rows[j-1] = rows[j-1], rows[j]
			}
		}
	}
	byCount(a.Leaderboard)
	byCount(a.MostComplaints)
	return a, nil
}
