package service

import (
	"context"
	"errors"
	"net/http"
	"strings"
	"time"

	"fitkit/server/internal/workspace/domain"
	"fitkit/server/internal/workspace/integrations"
	"fitkit/server/internal/workspace/store"
)

// ---------- from the Fitkit client app (webhooks)

type IncomingClient struct {
	ExternalID string  `json:"externalId"`
	Name       string  `json:"name"`
	Phone      string  `json:"phone"`
	Telegram   *string `json:"telegram"`
	City       string  `json:"city"`
	Branch     string  `json:"branch"`
	Sizes      string  `json:"sizes"`
	Stores     string  `json:"stores"`
}

type IncomingItem struct {
	Photo   string        `json:"photo"`
	Name    string        `json:"name"`
	Store   string        `json:"store"`
	URL     string        `json:"url"`
	Size    string        `json:"size"`
	Qty     int           `json:"qty"`
	Price   *domain.Money `json:"price"`
	Comment *string       `json:"comment"`
	// ListingPrice is the store's own price text (any currency) when Price is unknown.
	ListingPrice *string `json:"listingPrice"`
}

type IncomingOrder struct {
	ExternalID string         `json:"externalId"`
	Client     IncomingClient `json:"client"`
	Paid       *domain.Money  `json:"paid"`
	Fee        domain.Money   `json:"fee"`
	Photo      *string        `json:"photo"` // the look the client bought from
	Items      []IncomingItem `json:"items"`
}

// IngestOrder stores a new request from the client app. Idempotent by externalId.
func (s *Service) IngestOrder(ctx context.Context, in IncomingOrder) (id, number string, err error) {
	if in.ExternalID == "" || in.Client.ExternalID == "" || in.Client.Name == "" || len(in.Items) == 0 {
		return "", "", errf(http.StatusBadRequest, "externalId, client.externalId, client.name and items are required")
	}
	for _, it := range in.Items {
		if it.Name == "" || it.Store == "" || it.URL == "" || it.Size == "" {
			return "", "", errf(http.StatusBadRequest, "each item needs name, store, url and size")
		}
	}
	err = s.run(ctx, func(q *store.Q, op *op) error {
		if existing, err := q.OrderIDByExternal(ctx, in.ExternalID); err == nil {
			o, err := q.Order(ctx, existing, false)
			id, number = o.ID, o.Number
			return err
		} else if !errors.Is(err, store.ErrNotFound) {
			return err
		}
		cid, err := q.UpsertClient(ctx, in.Client.ExternalID, domain.Client{
			Name: in.Client.Name, Phone: in.Client.Phone, Telegram: in.Client.Telegram,
			City: in.Client.City, Branch: in.Client.Branch, Sizes: in.Client.Sizes, Stores: in.Client.Stores,
		})
		if err != nil {
			return err
		}
		items := make([]domain.Item, len(in.Items))
		for i, it := range in.Items {
			qty := it.Qty
			if qty <= 0 {
				qty = 1
			}
			items[i] = domain.Item{Photo: it.Photo, Name: it.Name, Store: it.Store, URL: it.URL, Size: it.Size, Qty: qty, Price: it.Price, Comment: it.Comment, ListingPrice: it.ListingPrice, Status: domain.ItemPending}
		}
		now := s.now()
		id, number, err = q.InsertOrder(ctx, store.NewOrder{
			ExternalID: in.ExternalID, ClientID: cid, Status: domain.StatusNew, Paid: in.Paid, Fee: in.Fee, Photo: in.Photo,
			CreatedAt: now, DueAt: now.Add(domain.DueAfter[domain.StatusNew]), Items: items,
		})
		if err != nil {
			return err
		}
		op.emit("order", id)
		return q.AddHistory(ctx, id, "Система", "Заявка надійшла")
	})
	return id, number, err
}

type IncomingConfirmation struct {
	OrderExternalID string `json:"orderExternalId"`
	ItemID          string `json:"itemId"`
	Accept          bool   `json:"accept"`
}

// ClientConfirmation applies the client's answer to a price confirmation.
func (s *Service) ClientConfirmation(ctx context.Context, in IncomingConfirmation) error {
	return s.run(ctx, func(q *store.Q, op *op) error {
		oid, err := q.OrderIDByExternal(ctx, in.OrderExternalID)
		if err != nil {
			return err
		}
		o, err := q.Order(ctx, oid, true)
		if err != nil {
			return err
		}
		action := ActCancel
		if in.Accept {
			action = ActClientConfirm
		}
		return s.itemAction(ctx, q, op, &o, in.ItemID, action, nil, "Клієнт")
	})
}

type IncomingMessage struct {
	ClientExternalID string `json:"clientExternalId"`
	OrderExternalID  string `json:"orderExternalId"`
	Text             string `json:"text"`
}

// ClientMessage adds a client's support message, opening a ticket if needed.
func (s *Service) ClientMessage(ctx context.Context, in IncomingMessage) (string, error) {
	text := strings.TrimSpace(in.Text)
	if text == "" || in.ClientExternalID == "" {
		return "", errf(http.StatusBadRequest, "clientExternalId and text are required")
	}
	var ticketID string
	err := s.run(ctx, func(q *store.Q, op *op) error {
		cid, err := q.ClientIDByExternal(ctx, in.ClientExternalID)
		if err != nil {
			return err
		}
		var oid *string
		if in.OrderExternalID != "" {
			id, err := q.OrderIDByExternal(ctx, in.OrderExternalID)
			if err != nil {
				return err
			}
			oid = &id
		}
		ticketID, err = q.OpenTicketFor(ctx, cid, oid)
		if errors.Is(err, store.ErrNotFound) {
			summary := text
			if r := []rune(summary); len(r) > 60 {
				summary = string(r[:60]) + "…"
			}
			ticketID, err = q.InsertTicket(ctx, domain.Ticket{ClientID: cid, OrderID: oid, Status: "open", Summary: summary, UpdatedAt: s.now()})
		}
		if err != nil {
			return err
		}
		if _, err := q.AddMessage(ctx, ticketID, "client", nil, text, s.now()); err != nil {
			return err
		}
		op.emit("ticket", ticketID)
		return nil
	})
	return ticketID, err
}

// ---------- from the tracking aggregator and the warehouse

type TrackingUpdate struct {
	Tracking string     `json:"tracking"`
	Status   string     `json:"status"` // in_transit | arrived_warehouse | delivered | exception
	Title    string     `json:"title"`
	Detail   *string    `json:"detail"`
	At       *time.Time `json:"at"`
}

func (s *Service) Tracking(ctx context.Context, u TrackingUpdate) error {
	if u.Tracking == "" || u.Title == "" {
		return errf(http.StatusBadRequest, "tracking and title are required")
	}
	return s.run(ctx, func(q *store.Q, op *op) error {
		sh, err := q.ShipmentByTracking(ctx, u.Tracking)
		if err != nil {
			return err
		}
		at := s.now()
		if u.At != nil {
			at = *u.At
		}
		if err := q.AddTrackingEvent(ctx, sh.ID, domain.TrackingEvent{At: at, Title: u.Title, Detail: u.Detail}); err != nil {
			return err
		}
		op.emit("shipment", sh.ID)
		if sh.Problem != nil && u.Status != "exception" {
			sh.Problem = nil
		}
		if u.Status == "exception" {
			sh.Problem = &u.Title
		}
		switch {
		case sh.Leg == domain.LegOut && u.Status == "delivered":
			sh.Delivered = true
			o, err := q.Order(ctx, sh.OrderID, true)
			if err != nil {
				return err
			}
			if o.Status == domain.StatusShipped {
				o.Status = domain.StatusDelivered
				o.Location = ptr("Доставлено")
				if err := q.SaveOrder(ctx, o); err != nil {
					return err
				}
				if err := q.AddHistory(ctx, o.ID, "Нова Пошта", "Статус → "+domain.StatusLabel[domain.StatusDelivered]); err != nil {
					return err
				}
				op.emit("order", o.ID)
				op.then(func(ctx context.Context) {
					s.notify(ctx, integrations.ClientNotice{Kind: "order_status", ClientID: o.ClientID, OrderID: o.ID, Text: "Доставлено"})
				})
			}
		case sh.Leg == domain.LegIn && u.Status == "arrived_warehouse":
			if err := s.markArrived(ctx, q, op, &sh, "Агрегатор трекінгу"); err != nil {
				return err
			}
		default:
			if err := s.setOrderLocation(ctx, q, op, sh.OrderID, "В дорозі · "+deref(sh.Carrier)); err != nil {
				return err
			}
		}
		if err := q.SaveShipment(ctx, sh); err != nil {
			return err
		}
		return s.clearShipmentProblem(ctx, q, sh.OrderID)
	})
}

// Arrived is the warehouse confirming that a store parcel was received and checked.
func (s *Service) Arrived(ctx context.Context, actor domain.Employee, shipmentID string) error {
	if actor.Role != domain.RoleWarehouse && actor.Role != domain.RoleLead && actor.Role != domain.RoleBuyer {
		return ErrForbidden
	}
	return s.run(ctx, func(q *store.Q, op *op) error {
		sh, err := q.Shipment(ctx, shipmentID)
		if err != nil {
			return err
		}
		if sh.Leg != domain.LegIn {
			return errf(http.StatusConflict, "Це відправлення клієнту")
		}
		if sh.AtWarehouse {
			return nil
		}
		if err := q.AddTrackingEvent(ctx, sh.ID, domain.TrackingEvent{At: s.now(), Title: "Прибуло на склад", Detail: ptr("прийняв " + actor.Name)}); err != nil {
			return err
		}
		if err := s.markArrived(ctx, q, op, &sh, actor.Name); err != nil {
			return err
		}
		if err := q.SaveShipment(ctx, sh); err != nil {
			return err
		}
		op.emit("shipment", sh.ID)
		return s.clearShipmentProblem(ctx, q, sh.OrderID)
	})
}

func (s *Service) markArrived(ctx context.Context, q *store.Q, op *op, sh *domain.Shipment, who string) error {
	sh.AtWarehouse = true
	sh.Problem = nil
	if err := q.AddHistory(ctx, sh.OrderID, who, "Посилка від "+deref(sh.Store)+" прибула на склад"); err != nil {
		return err
	}
	return s.setOrderLocation(ctx, q, op, sh.OrderID, "На складі")
}

func (s *Service) setOrderLocation(ctx context.Context, q *store.Q, op *op, orderID, loc string) error {
	o, err := q.Order(ctx, orderID, true)
	if err != nil {
		return err
	}
	if o.Status != domain.StatusPurchased {
		return nil
	}
	o.Location = &loc
	op.emit("order", o.ID)
	return q.SaveOrder(ctx, o)
}

// ---------- background jobs

// ExpireConfirmations cancels and refunds items whose price confirmation went unanswered for 24 h.
func (s *Service) ExpireConfirmations(ctx context.Context) (int, error) {
	pairs, err := s.Store.Q().AwaitingExpired(ctx, s.now().Add(-domain.ConfirmWindow))
	if err != nil {
		return 0, err
	}
	n := 0
	for _, p := range pairs {
		err := s.run(ctx, func(q *store.Q, op *op) error {
			o, err := q.Order(ctx, p[0], true)
			if err != nil {
				return err
			}
			if err := q.AddHistory(ctx, o.ID, "Система", "Клієнт не відповів 24 год — автоповернення"); err != nil {
				return err
			}
			return s.itemAction(ctx, q, op, &o, p[1], ActCancel, nil, "Система")
		})
		if err != nil {
			s.Log.Error("expire confirmation", "order", p[0], "item", p[1], "err", err)
			continue
		}
		n++
	}
	return n, nil
}

// FlagStalled marks store parcels with no tracking after 3 days or no movement for 5 days.
func (s *Service) FlagStalled(ctx context.Context) (int, error) {
	now := s.now()
	noTrack, noMove, err := s.Store.Q().StalledInbound(ctx, now.Add(-domain.NoTrackingAfter), now.Add(-domain.NoMovementAfter))
	if err != nil {
		return 0, err
	}
	flag := func(id, problem string) error {
		return s.run(ctx, func(q *store.Q, op *op) error {
			sh, err := q.Shipment(ctx, id)
			if err != nil {
				return err
			}
			sh.Problem = &problem
			if err := q.SaveShipment(ctx, sh); err != nil {
				return err
			}
			o, err := q.Order(ctx, sh.OrderID, true)
			if err != nil {
				return err
			}
			if o.Problem == nil {
				o.Problem = &problem
				if err := q.SaveOrder(ctx, o); err != nil {
					return err
				}
				if err := q.AddHistory(ctx, o.ID, "Система", "Проблема: "+problem+" ("+deref(sh.Store)+")"); err != nil {
					return err
				}
			}
			op.emit("shipment", sh.ID)
			op.emit("order", o.ID)
			return nil
		})
	}
	n := 0
	for _, id := range noTrack {
		if err := flag(id, "Немає ТТН 3 дні"); err == nil {
			n++
		}
	}
	for _, id := range noMove {
		if err := flag(id, "Немає руху 5 днів"); err == nil {
			n++
		}
	}
	return n, nil
}
