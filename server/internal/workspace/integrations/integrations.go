// Package integrations defines the outside services the workspace talks to.
// Each has a Fake used in development and tests; real clients plug in through config.
package integrations

import (
	"context"
	"fmt"
	"log/slog"
	"math/rand/v2"
	"sync"

	"fitkit/server/internal/workspace/domain"
)

// Shipping creates outbound Nova Poshta shipments (warehouse → client).
type Shipping interface {
	CreateShipment(ctx context.Context, req ShipmentRequest) (tracking string, err error)
}

type ShipmentRequest struct {
	OrderNumber string
	Recipient   string
	Phone       string
	City        string
	Branch      string
	Description string
	Declared    domain.Money // declared value, kopiyky
	Items       int
}

// Tracker registers store→warehouse tracking numbers with an aggregator
// (17TRACK, AfterShip, Ship24, TrackingMore). Updates come back via POST /webhooks/tracking.
type Tracker interface {
	Register(ctx context.Context, tracking, carrier string) error
}

// Payments refunds money to the client's card.
type Payments interface {
	Refund(ctx context.Context, orderExternalID string, amount domain.Money, reason string) error
}

// ClientApp sends notices and actions to the Fitkit client app.
type ClientApp interface {
	Notify(ctx context.Context, n ClientNotice) error
}

type ClientNotice struct {
	Kind     string // order_status | price_confirmation | refund | support_reply | shipped
	ClientID string
	OrderID  string
	ItemID   string
	Text     string
	Amount   domain.Money
}

type Set struct {
	Shipping  Shipping
	Tracker   Tracker
	Payments  Payments
	ClientApp ClientApp
}

// ---------- fakes (log + remember calls)

type Fake struct {
	Log *slog.Logger
	mu  sync.Mutex
	// Calls records every call for tests: "refund:#0001:399900", "notify:price_confirmation", …
	Calls []string
}

func NewFakes(log *slog.Logger) (*Fake, Set) {
	f := &Fake{Log: log}
	return f, Set{Shipping: f, Tracker: f, Payments: f, ClientApp: f}
}

func (f *Fake) record(s string) {
	f.mu.Lock()
	f.Calls = append(f.Calls, s)
	f.mu.Unlock()
	f.Log.Info("integration (fake)", "call", s)
}

func (f *Fake) CreateShipment(_ context.Context, r ShipmentRequest) (string, error) {
	t := fmt.Sprintf("2045 %04d %04d %02d", rand.IntN(10000), rand.IntN(10000), rand.IntN(100))
	f.record("np:create:" + r.OrderNumber)
	return t, nil
}

func (f *Fake) Register(_ context.Context, tracking, carrier string) error {
	f.record("track:register:" + tracking)
	return nil
}

func (f *Fake) Refund(_ context.Context, order string, amount domain.Money, _ string) error {
	f.record(fmt.Sprintf("refund:%s:%d", order, amount))
	return nil
}

func (f *Fake) Notify(_ context.Context, n ClientNotice) error {
	f.record("notify:" + n.Kind)
	return nil
}

func (f *Fake) Snapshot() []string {
	f.mu.Lock()
	defer f.mu.Unlock()
	return append([]string(nil), f.Calls...)
}
