// Package service implements the workspace operations. Every operation runs in one
// DB transaction, re-checks the rules on the server, writes order history, and only
// after commit calls outside services (refunds, client notices) and publishes events.
package service

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"net/http"
	"strings"
	"time"

	"fitkit/server/internal/workspace/domain"
	"fitkit/server/internal/workspace/integrations"
	"fitkit/server/internal/workspace/store"
)

// Error is an expected failure with an HTTP status and a message for the UI (Ukrainian).
type Error struct {
	Code int
	Msg  string
}

func (e *Error) Error() string { return e.Msg }

func errf(code int, format string, a ...any) error {
	return &Error{Code: code, Msg: fmt.Sprintf(format, a...)}
}

var (
	ErrNotFound  = &Error{http.StatusNotFound, "Не знайдено"}
	ErrForbidden = &Error{http.StatusForbidden, "Недостатньо прав для цієї дії"}
)

// Event is pushed to connected clients (SSE) after a change is committed.
type Event struct {
	Type string `json:"type"` // order | shipment | ticket
	ID   string `json:"id"`
}

type Publisher interface{ Publish(Event) }

type Service struct {
	Store  *store.Store
	Int    integrations.Set
	Events Publisher
	Log    *slog.Logger
	Now    func() time.Time
}

// op collects work to do after a successful commit.
type op struct {
	events []Event
	after  []func(ctx context.Context)
}

func (o *op) emit(t, id string)                 { o.events = append(o.events, Event{t, id}) }
func (o *op) then(fn func(ctx context.Context)) { o.after = append(o.after, fn) }

func (s *Service) run(ctx context.Context, fn func(q *store.Q, o *op) error) error {
	o := &op{}
	if err := s.Store.Tx(ctx, func(q *store.Q) error { return fn(q, o) }); err != nil {
		if errors.Is(err, store.ErrNotFound) {
			return ErrNotFound
		}
		return err
	}
	for _, f := range o.after {
		f(context.WithoutCancel(ctx))
	}
	if s.Events != nil {
		for _, e := range o.events {
			s.Events.Publish(e)
		}
	}
	return nil
}

func (s *Service) now() time.Time {
	if s.Now != nil {
		return s.Now()
	}
	return time.Now()
}

func canEdit(actor domain.Employee, o domain.Order) bool {
	return actor.Role == domain.RoleLead || (o.OwnerID != nil && *o.OwnerID == actor.ID)
}

func ptr[T any](v T) *T { return &v }

// ---------- reads

type Snapshot struct {
	Me        domain.Employee   `json:"me"`
	Employees []domain.Employee `json:"employees"`
	Clients   []domain.Client   `json:"clients"`
	Orders    []domain.Order    `json:"orders"`
	Shipments []domain.Shipment `json:"shipments"`
	Tickets   []domain.Ticket   `json:"tickets"`
}

// Bootstrap returns everything the workspace shows: active orders, all shipments and tickets.
func (s *Service) Bootstrap(ctx context.Context, me domain.Employee) (Snapshot, error) {
	q := s.Store.Q()
	snap := Snapshot{Me: me}
	var err error
	if snap.Employees, err = q.Employees(ctx); err != nil {
		return snap, err
	}
	if snap.Clients, err = q.Clients(ctx); err != nil {
		return snap, err
	}
	if snap.Orders, err = q.Orders(ctx, store.OrderFilter{}); err != nil {
		return snap, err
	}
	if snap.Shipments, err = q.Shipments(ctx, ""); err != nil {
		return snap, err
	}
	snap.Tickets, err = q.Tickets(ctx, "")
	return snap, err
}

type OrderView struct {
	domain.Order
	Money       domain.MoneySummary `json:"money"`
	ClientSees  string              `json:"clientSees"`
	CanPurchase bool                `json:"canPurchase"`
}

func View(o domain.Order) OrderView {
	return OrderView{Order: o, Money: domain.Summarize(o), ClientSees: domain.ClientStatus(o), CanPurchase: domain.CanMarkPurchased(o)}
}

func (s *Service) Order(ctx context.Context, id string) (OrderView, error) {
	o, err := s.Store.Q().Order(ctx, id, false)
	if errors.Is(err, store.ErrNotFound) {
		return OrderView{}, ErrNotFound
	}
	return View(o), err
}

// ---------- orders

func (s *Service) Take(ctx context.Context, actor domain.Employee, orderID string) error {
	return s.run(ctx, func(q *store.Q, o *op) error {
		ok, err := q.TakeOrder(ctx, orderID, actor.ID, s.now().Add(domain.DueAfter[domain.StatusInProgress]))
		if err != nil {
			return err
		}
		if !ok {
			return errf(http.StatusConflict, "Заявку вже взяв інший співробітник")
		}
		o.emit("order", orderID)
		return q.AddHistory(ctx, orderID, actor.Name, "Заявку взято в роботу")
	})
}

// TakeNext assigns the oldest unassigned request to the actor.
func (s *Service) TakeNext(ctx context.Context, actor domain.Employee) (string, error) {
	var id string
	err := s.run(ctx, func(q *store.Q, o *op) error {
		var err error
		if id, err = q.OldestNewOrder(ctx); err != nil {
			if errors.Is(err, store.ErrNotFound) {
				return errf(http.StatusNotFound, "Черга порожня")
			}
			return err
		}
		if _, err := q.TakeOrder(ctx, id, actor.ID, s.now().Add(domain.DueAfter[domain.StatusInProgress])); err != nil {
			return err
		}
		o.emit("order", id)
		return q.AddHistory(ctx, id, actor.Name, "Заявку взято в роботу")
	})
	return id, err
}

// Move changes the status with the same guards as the UI.
func (s *Service) Move(ctx context.Context, actor domain.Employee, orderID string, to domain.OrderStatus) error {
	if !to.Valid() {
		return errf(http.StatusBadRequest, "Невідомий статус")
	}
	if to == domain.StatusShipped {
		return s.Ship(ctx, actor, orderID)
	}
	return s.run(ctx, func(q *store.Q, op *op) error {
		o, err := q.Order(ctx, orderID, true)
		if err != nil {
			return err
		}
		if !canEdit(actor, o) {
			return ErrForbidden
		}
		if o.Status == to {
			return nil
		}
		switch to {
		case domain.StatusPurchased:
			if o.Status != domain.StatusInProgress && o.Status != domain.StatusAwaiting {
				return errf(http.StatusConflict, "Викупити можна лише замовлення в роботі")
			}
			if !domain.CanMarkPurchased(o) {
				return errf(http.StatusConflict, "Спершу розберіть кожен товар: перевірте, застосуйте правило або скасуйте.")
			}
			stores := []string{}
			seen := map[string]bool{}
			for i := range o.Items {
				it := &o.Items[i]
				if it.Status == domain.ItemCancelled {
					continue
				}
				it.Status = domain.ItemBought
				if err := q.SaveItem(ctx, *it); err != nil {
					return err
				}
				if !seen[it.Store] {
					seen[it.Store] = true
					stores = append(stores, it.Store)
				}
			}
			existing, err := q.Shipments(ctx, o.ID)
			if err != nil {
				return err
			}
			for _, st := range stores {
				dup := false
				for _, sh := range existing {
					if sh.Leg == domain.LegIn && sh.Store != nil && *sh.Store == st {
						dup = true
					}
				}
				if dup {
					continue
				}
				id, err := q.InsertShipment(ctx, domain.Shipment{OrderID: o.ID, Leg: domain.LegIn, Store: ptr(st)})
				if err != nil {
					return err
				}
				if err := q.AddTrackingEvent(ctx, id, domain.TrackingEvent{At: s.now(), Title: "Викуплено в " + st}); err != nil {
					return err
				}
				op.emit("shipment", id)
			}
			o.Location = ptr("Чекаємо ТТН від магазину")
		case domain.StatusInProgress:
			if o.Status != domain.StatusAwaiting && o.Status != domain.StatusPurchased {
				return errf(http.StatusConflict, "Цей перехід недоступний")
			}
		case domain.StatusAwaiting:
			return errf(http.StatusConflict, "Спершу надішліть запит на підтвердження ціни з картки заявки.")
		case domain.StatusDelivered:
			if o.Status != domain.StatusShipped {
				return errf(http.StatusConflict, "Доставленим можна позначити лише відправлене замовлення")
			}
		case domain.StatusCancelled:
			return errf(http.StatusBadRequest, "Для скасування використовуйте POST /orders/{id}/cancel")
		case domain.StatusNew:
			return errf(http.StatusConflict, "Повернути заявку в чергу не можна")
		}
		o.Status = to
		if d, ok := domain.DueAfter[to]; ok {
			o.DueAt = s.now().Add(d)
		}
		if err := q.SaveOrder(ctx, o); err != nil {
			return err
		}
		op.emit("order", o.ID)
		if to == domain.StatusDelivered {
			op.then(func(ctx context.Context) {
				s.notify(ctx, integrations.ClientNotice{Kind: "order_status", ClientID: o.ClientID, OrderID: o.ID, Text: "Доставлено"})
			})
		}
		return q.AddHistory(ctx, o.ID, actor.Name, "Статус → "+domain.StatusLabel[to])
	})
}

// ItemAction values for PATCH /orders/{id}/items/{itemId}.
const (
	ActCheck         = "check"
	ActSoldOut       = "sold_out"
	ActStorePrice    = "store_price"
	ActSendConfirm   = "send_confirmation"
	ActClientConfirm = "client_confirmed"
	ActCancel        = "cancel"
)

func (s *Service) Item(ctx context.Context, actor domain.Employee, orderID, itemID, action string, price *domain.Money) error {
	return s.run(ctx, func(q *store.Q, op *op) error {
		o, err := q.Order(ctx, orderID, true)
		if err != nil {
			return err
		}
		if o.Status == domain.StatusNew {
			return errf(http.StatusConflict, "Спершу візьміть заявку")
		}
		if !canEdit(actor, o) {
			return ErrForbidden
		}
		if o.Status != domain.StatusInProgress && o.Status != domain.StatusAwaiting {
			return errf(http.StatusConflict, "Товари можна змінювати лише до викупу")
		}
		return s.itemAction(ctx, q, op, &o, itemID, action, price, actor.Name)
	})
}

func (s *Service) itemAction(ctx context.Context, q *store.Q, op *op, o *domain.Order, itemID, action string, price *domain.Money, who string) error {
	var it *domain.Item
	for i := range o.Items {
		if o.Items[i].ID == itemID {
			it = &o.Items[i]
		}
	}
	if it == nil {
		return ErrNotFound
	}
	if it.Status == domain.ItemCancelled {
		return errf(http.StatusConflict, "Товар уже скасовано")
	}
	var what string
	switch action {
	case ActCheck:
		it.Status = domain.ItemChecked
		what = it.Name + ": перевірено"
	case ActSoldOut:
		it.Status = domain.ItemSoldOut
		what = it.Name + ": позначено «немає в наявності»"
	case ActStorePrice:
		if price == nil || *price <= 0 {
			return errf(http.StatusBadRequest, "Вкажіть ціну в магазині")
		}
		it.StorePrice = price
		if it.Status == domain.ItemPending {
			it.Status = domain.ItemChecked
		}
		what = it.Name + ": ціна в магазині " + domain.UAH(*price)
	case ActSendConfirm:
		r := domain.RuleFor(*it)
		if r == nil || r.Kind != domain.RuleConfirm {
			return errf(http.StatusConflict, "Підтвердження потрібне лише коли ціна зросла більше ніж на 5%%")
		}
		it.Status = domain.ItemAwaiting
		it.ConfirmSentAt = ptr(s.now())
		what = it.Name + ": клієнту надіслано запит на підтвердження ціни"
		item := *it
		op.then(func(ctx context.Context) {
			s.notify(ctx, integrations.ClientNotice{Kind: "price_confirmation", ClientID: o.ClientID, OrderID: o.ID, ItemID: item.ID, Amount: *item.StorePrice,
				Text: fmt.Sprintf("Ціна змінилась: %s %s → %s", item.Name, domain.UAH(*item.Price), domain.UAH(*item.StorePrice))})
		})
	case ActClientConfirm:
		if it.Status != domain.ItemAwaiting {
			return errf(http.StatusConflict, "Запит на підтвердження не надсилався")
		}
		it.Price = it.StorePrice
		it.Status = domain.ItemChecked
		it.ConfirmSentAt = nil
		what = it.Name + ": клієнт підтвердив нову ціну"
	case ActCancel:
		refund := domain.Money(0)
		if it.Price != nil {
			refund = *it.Price * domain.Money(it.Qty)
		}
		it.Status = domain.ItemCancelled
		it.ConfirmSentAt = nil
		what = it.Name + ": скасовано, " + domain.UAH(refund) + " повернуто"
		s.refundLater(op, *o, refund, it.Name+": товару немає", "Товару більше немає: "+it.Name+", кошти повернуто")
	default:
		return errf(http.StatusBadRequest, "Невідома дія з товаром")
	}
	if err := q.SaveItem(ctx, *it); err != nil {
		return err
	}
	s.syncOrder(o)
	if err := q.SaveOrder(ctx, *o); err != nil {
		return err
	}
	op.emit("order", o.ID)
	return q.AddHistory(ctx, o.ID, who, what)
}

const soldOutSuffix = ": немає в наявності — застосуйте правило"

// syncOrder keeps the status and the automatic red flag in line with the items.
func (s *Service) syncOrder(o *domain.Order) {
	awaiting := false
	var soldOut *domain.Item
	for i := range o.Items {
		switch o.Items[i].Status {
		case domain.ItemAwaiting:
			awaiting = true
		case domain.ItemSoldOut:
			if soldOut == nil {
				soldOut = &o.Items[i]
			}
		}
	}
	if awaiting && o.Status == domain.StatusInProgress {
		o.Status = domain.StatusAwaiting
		o.DueAt = s.now().Add(domain.ConfirmWindow)
	}
	if !awaiting && o.Status == domain.StatusAwaiting {
		o.Status = domain.StatusInProgress
		o.DueAt = s.now().Add(domain.DueAfter[domain.StatusInProgress])
	}
	switch {
	case soldOut != nil:
		o.Problem = ptr(soldOut.Name + soldOutSuffix)
	case o.Problem != nil && strings.HasSuffix(*o.Problem, soldOutSuffix):
		o.Problem = nil
	}
}

func (s *Service) SetProblem(ctx context.Context, actor domain.Employee, orderID string, problem *string) error {
	return s.run(ctx, func(q *store.Q, op *op) error {
		o, err := q.Order(ctx, orderID, true)
		if err != nil {
			return err
		}
		if !canEdit(actor, o) && actor.Role != domain.RoleWarehouse {
			return ErrForbidden
		}
		what := "Проблему знято"
		if problem != nil && strings.TrimSpace(*problem) != "" {
			p := strings.TrimSpace(*problem)
			problem = &p
			what = "Проблема: " + p
		} else {
			problem = nil
		}
		o.Problem = problem
		if err := q.SaveOrder(ctx, o); err != nil {
			return err
		}
		op.emit("order", o.ID)
		return q.AddHistory(ctx, o.ID, actor.Name, what)
	})
}

func (s *Service) AddNote(ctx context.Context, actor domain.Employee, orderID, text string) (domain.Note, error) {
	var n domain.Note
	text = strings.TrimSpace(text)
	if text == "" {
		return n, errf(http.StatusBadRequest, "Порожня нотатка")
	}
	err := s.run(ctx, func(q *store.Q, op *op) error {
		if _, err := q.Order(ctx, orderID, false); err != nil {
			return err
		}
		var err error
		n, err = q.AddNote(ctx, orderID, &actor.ID, actor.Name, text, s.now())
		op.emit("order", orderID)
		return err
	})
	return n, err
}

func (s *Service) Cancel(ctx context.Context, actor domain.Employee, orderID string) error {
	return s.run(ctx, func(q *store.Q, op *op) error {
		o, err := q.Order(ctx, orderID, true)
		if err != nil {
			return err
		}
		if !canEdit(actor, o) {
			return ErrForbidden
		}
		if o.Status != domain.StatusInProgress && o.Status != domain.StatusAwaiting && o.Status != domain.StatusNew {
			return errf(http.StatusConflict, "Після викупу замовлення не скасовують — позначте проблему")
		}
		// Refund what is left: the amount paid minus items already refunded one by one.
		var remaining, refunded domain.Money
		for i := range o.Items {
			line := domain.Money(0)
			if o.Items[i].Price != nil {
				line = *o.Items[i].Price * domain.Money(o.Items[i].Qty)
			}
			if o.Items[i].Status == domain.ItemCancelled {
				refunded += line
				continue
			}
			remaining += line
			o.Items[i].Status = domain.ItemCancelled
			o.Items[i].ConfirmSentAt = nil
			if err := q.SaveItem(ctx, o.Items[i]); err != nil {
				return err
			}
		}
		refund := remaining
		if o.Paid != nil {
			refund = *o.Paid - refunded
		}
		o.Status = domain.StatusCancelled
		o.Problem = nil
		if err := q.SaveOrder(ctx, o); err != nil {
			return err
		}
		s.refundLater(op, o, refund, "замовлення скасовано", "Замовлення скасовано, кошти повернуто")
		op.emit("order", o.ID)
		return q.AddHistory(ctx, o.ID, actor.Name, "Замовлення скасовано, повне повернення коштів")
	})
}

// refundLater refunds after commit and tells the client; a failed refund flags the order.
func (s *Service) refundLater(op *op, o domain.Order, amount domain.Money, reason, notice string) {
	op.then(func(ctx context.Context) {
		if amount > 0 {
			if err := s.Int.Payments.Refund(ctx, o.Number, amount, reason); err != nil {
				s.Log.Error("refund failed", "order", o.Number, "err", err)
				_ = s.Store.Tx(ctx, func(q *store.Q) error {
					cur, err := q.Order(ctx, o.ID, true)
					if err != nil {
						return err
					}
					cur.Problem = ptr("Повернення коштів не пройшло: " + domain.UAH(amount))
					if err := q.SaveOrder(ctx, cur); err != nil {
						return err
					}
					return q.AddHistory(ctx, o.ID, "Система", "Помилка повернення коштів: "+err.Error())
				})
				return
			}
		}
		s.notify(ctx, integrations.ClientNotice{Kind: "refund", ClientID: o.ClientID, OrderID: o.ID, Amount: amount, Text: notice})
	})
}

func (s *Service) notify(ctx context.Context, n integrations.ClientNotice) {
	if err := s.Int.ClientApp.Notify(ctx, n); err != nil {
		s.Log.Error("client notice failed", "kind", n.Kind, "order", n.OrderID, "err", err)
	}
}

// Ship creates the Nova Poshta shipment (warehouse → client) once every parcel is at the warehouse.
func (s *Service) Ship(ctx context.Context, actor domain.Employee, orderID string) error {
	check := func(q *store.Q) (domain.Order, error) {
		o, err := q.Order(ctx, orderID, true)
		if err != nil {
			return o, err
		}
		if !canEdit(actor, o) && actor.Role != domain.RoleWarehouse {
			return o, ErrForbidden
		}
		if o.Status != domain.StatusPurchased {
			return o, errf(http.StatusConflict, "Відправити можна лише викуплене замовлення.")
		}
		sh, err := q.Shipments(ctx, o.ID)
		if err != nil {
			return o, err
		}
		in := 0
		for _, x := range sh {
			if x.Leg == domain.LegIn {
				in++
				if !x.AtWarehouse {
					return o, errf(http.StatusConflict, "Спершу всі посилки мають прибути на склад.")
				}
			}
			if x.Leg == domain.LegOut {
				return o, errf(http.StatusConflict, "ТТН для клієнта вже створено")
			}
		}
		if in == 0 {
			return o, errf(http.StatusConflict, "Спершу всі посилки мають прибути на склад.")
		}
		return o, nil
	}
	// 1. validate
	var o domain.Order
	var c domain.Client
	if err := s.run(ctx, func(q *store.Q, _ *op) error {
		var err error
		if o, err = check(q); err != nil {
			return err
		}
		c, err = q.Client(ctx, o.ClientID)
		return err
	}); err != nil {
		return err
	}
	// 2. create the ТТН outside the transaction
	var names []string
	var declared domain.Money
	n := 0
	for _, it := range o.Items {
		if it.Status == domain.ItemBought {
			names = append(names, it.Name)
			n += it.Qty
			if it.Price != nil {
				declared += *it.Price * domain.Money(it.Qty)
			}
		}
	}
	tracking, err := s.Int.Shipping.CreateShipment(ctx, integrations.ShipmentRequest{
		OrderNumber: o.Number, Recipient: c.Name, Phone: c.Phone, City: c.City, Branch: c.Branch,
		Description: strings.Join(names, ", "), Declared: declared, Items: n,
	})
	if err != nil {
		s.Log.Error("nova poshta", "order", o.Number, "err", err)
		return errf(http.StatusBadGateway, "Нова Пошта не створила ТТН: %v", err)
	}
	// 3. save
	return s.run(ctx, func(q *store.Q, op *op) error {
		o, err := check(q)
		if err != nil {
			return err
		}
		id, err := q.InsertShipment(ctx, domain.Shipment{OrderID: o.ID, Leg: domain.LegOut, Carrier: ptr("Нова Пошта"), Tracking: ptr(tracking), Source: ptr("api")})
		if err != nil {
			return err
		}
		if err := q.AddTrackingEvent(ctx, id, domain.TrackingEvent{At: s.now(), Title: "ТТН створено", Detail: ptr("API Нової Пошти")}); err != nil {
			return err
		}
		o.Status = domain.StatusShipped
		o.Location = ptr("Нова Пошта · " + tracking)
		o.DueAt = s.now().Add(domain.DueAfter[domain.StatusShipped])
		if err := q.SaveOrder(ctx, o); err != nil {
			return err
		}
		op.emit("order", o.ID)
		op.emit("shipment", id)
		op.then(func(ctx context.Context) {
			if err := s.Int.Tracker.Register(ctx, tracking, "Нова Пошта"); err != nil {
				s.Log.Error("tracker register", "tracking", tracking, "err", err)
			}
			s.notify(ctx, integrations.ClientNotice{Kind: "shipped", ClientID: o.ClientID, OrderID: o.ID, Text: "Відправлено · Нова Пошта " + tracking})
		})
		return q.AddHistory(ctx, o.ID, actor.Name, "Відправлено · Нова Пошта "+tracking)
	})
}

// ---------- shipments

func (s *Service) SetTracking(ctx context.Context, actor domain.Employee, shipmentID, carrier, tracking string) error {
	tracking = strings.TrimSpace(tracking)
	if tracking == "" {
		return errf(http.StatusBadRequest, "Вкажіть номер ТТН")
	}
	carrier = strings.TrimSpace(carrier)
	if carrier == "" {
		carrier = "Автовизначення"
	}
	return s.run(ctx, func(q *store.Q, op *op) error {
		sh, err := q.Shipment(ctx, shipmentID)
		if err != nil {
			return err
		}
		if sh.Leg != domain.LegIn {
			return errf(http.StatusConflict, "ТТН для клієнта створює програма")
		}
		sh.Tracking, sh.Carrier, sh.Source, sh.Problem = &tracking, &carrier, ptr("manual"), nil
		if err := q.SaveShipment(ctx, sh); err != nil {
			return err
		}
		if err := q.AddTrackingEvent(ctx, sh.ID, domain.TrackingEvent{At: s.now(), Title: "ТТН додано · " + carrier, Detail: ptr("введено закупником")}); err != nil {
			return err
		}
		if err := s.clearShipmentProblem(ctx, q, sh.OrderID); err != nil {
			return err
		}
		op.emit("shipment", sh.ID)
		op.emit("order", sh.OrderID)
		op.then(func(ctx context.Context) {
			if err := s.Int.Tracker.Register(ctx, tracking, carrier); err != nil {
				s.Log.Error("tracker register", "tracking", tracking, "err", err)
			}
		})
		return q.AddHistory(ctx, sh.OrderID, actor.Name, "ТТН від "+deref(sh.Store)+": "+tracking)
	})
}

// clearShipmentProblem removes an order flag set by the stalled-parcel job when no parcel is stalled any more.
func (s *Service) clearShipmentProblem(ctx context.Context, q *store.Q, orderID string) error {
	o, err := q.Order(ctx, orderID, true)
	if err != nil {
		return err
	}
	if o.Problem == nil || !(strings.HasPrefix(*o.Problem, "Немає ТТН") || strings.HasPrefix(*o.Problem, "Немає руху")) {
		return nil
	}
	list, err := q.Shipments(ctx, orderID)
	if err != nil {
		return err
	}
	for _, sh := range list {
		if sh.Problem != nil {
			return nil
		}
	}
	o.Problem = nil
	return q.SaveOrder(ctx, o)
}

func deref(p *string) string {
	if p == nil {
		return ""
	}
	return *p
}

// ---------- tickets

func (s *Service) Reply(ctx context.Context, actor domain.Employee, ticketID, text string) (domain.Message, error) {
	var m domain.Message
	text = strings.TrimSpace(text)
	if text == "" {
		return m, errf(http.StatusBadRequest, "Порожнє повідомлення")
	}
	err := s.run(ctx, func(q *store.Q, op *op) error {
		t, err := q.Ticket(ctx, ticketID)
		if err != nil {
			return err
		}
		if t.Status != "open" {
			return errf(http.StatusConflict, "Звернення вже закрите")
		}
		m, err = q.AddMessage(ctx, t.ID, "support", &actor.ID, text, s.now())
		if err != nil {
			return err
		}
		op.emit("ticket", t.ID)
		op.then(func(ctx context.Context) {
			s.notify(ctx, integrations.ClientNotice{Kind: "support_reply", ClientID: t.ClientID, OrderID: deref(t.OrderID), Text: text})
		})
		return nil
	})
	return m, err
}

func (s *Service) Resolve(ctx context.Context, actor domain.Employee, ticketID string) error {
	return s.run(ctx, func(q *store.Q, op *op) error {
		t, err := q.Ticket(ctx, ticketID)
		if err != nil {
			return err
		}
		if t.Status == "resolved" {
			return nil
		}
		op.emit("ticket", t.ID)
		return q.SetTicket(ctx, t.ID, "resolved", "Вирішено · "+t.Summary)
	})
}
