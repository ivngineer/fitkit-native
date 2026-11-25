package httpapi_test

import (
	"bytes"
	"context"
	"crypto/hmac"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"reflect"
	"strings"
	"sync"
	"testing"
	"time"

	appstore "fitkit/server/internal/store"
	"fitkit/server/internal/workspace/domain"
	"fitkit/server/internal/workspace/httpapi"
	"fitkit/server/internal/workspace/integrations"
	"fitkit/server/internal/workspace/seed"
	"fitkit/server/internal/workspace/service"
	"fitkit/server/internal/workspace/store"
)

// The tests run the whole workspace API against a fresh SQLite file seeded with the demo data.

const clientSecret, trackerSecret = "test-client-secret", "test-tracker-secret"

type env struct {
	t     *testing.T
	srv   *httptest.Server
	svc   *service.Service
	fakes *integrations.Fake
	st    *store.Store
}

func setup(t *testing.T) *env {
	t.Helper()
	ctx := context.Background()
	app, err := appstore.Open(filepath.Join(t.TempDir(), "fitkit.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { app.Close() })
	st := store.New(app.DB())
	if err := st.Migrate(ctx); err != nil {
		t.Fatal(err)
	}
	if err := seed.Load(ctx, st); err != nil {
		t.Fatal(err)
	}
	log := slog.New(slog.NewTextHandler(io.Discard, nil))
	fakes, ints := integrations.NewFakes(log)
	hub := httpapi.NewHub()
	svc := &service.Service{Store: st, Int: ints, Events: hub, Log: log}
	srv := httptest.NewServer(httpapi.New(httpapi.Config{Service: svc, Hub: hub, Log: log, ClientAppSecret: clientSecret, TrackerSecret: trackerSecret}))
	t.Cleanup(srv.Close)
	return &env{t: t, srv: srv, svc: svc, fakes: fakes, st: st}
}

type res struct {
	code int
	body []byte
}

// json decodes into a zeroed v (fields absent in the body must not keep old values).
func (r res) json(v any) {
	rv := reflect.ValueOf(v).Elem()
	rv.Set(reflect.Zero(rv.Type()))
	if err := json.Unmarshal(r.body, v); err != nil {
		panic(string(r.body))
	}
}

func (e *env) do(method, path, token string, body any) res {
	var rd io.Reader
	if body != nil {
		b, _ := json.Marshal(body)
		rd = bytes.NewReader(b)
	}
	req, _ := http.NewRequest(method, e.srv.URL+path, rd)
	req.Header.Set("Content-Type", "application/json")
	if token != "" {
		req.Header.Set("Authorization", "Bearer "+token)
	}
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		e.t.Fatal(err)
	}
	defer resp.Body.Close()
	b, _ := io.ReadAll(resp.Body)
	return res{resp.StatusCode, b}
}

func (e *env) hook(path, secret string, body any) res {
	b, _ := json.Marshal(body)
	mac := hmac.New(sha256.New, []byte(secret))
	mac.Write(b)
	req, _ := http.NewRequest("POST", e.srv.URL+path, bytes.NewReader(b))
	req.Header.Set("X-Fitkit-Signature", "sha256="+hex.EncodeToString(mac.Sum(nil)))
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		e.t.Fatal(err)
	}
	defer resp.Body.Close()
	rb, _ := io.ReadAll(resp.Body)
	return res{resp.StatusCode, rb}
}

func (e *env) login(email string) string {
	r := e.do("POST", "/api/auth/login", "", map[string]string{"email": email, "password": seed.DemoPassword})
	if r.code != 200 {
		e.t.Fatalf("login %s: %d %s", email, r.code, r.body)
	}
	var out struct{ Token string }
	r.json(&out)
	return out.Token
}

func (e *env) want(r res, code int, what string) {
	e.t.Helper()
	if r.code != code {
		e.t.Fatalf("%s: want %d, got %d: %s", what, code, r.code, r.body)
	}
}

type orderView struct {
	ID       string
	Number   string
	Status   string
	OwnerID  *string
	Problem  *string
	Location *string
	Items    []struct {
		ID, Name, Status, Store string
		Price, StorePrice       *int64
	}
	Money struct {
		Paid, Refunds, Covered, ToBuy int64
	}
	ClientSees  string
	CanPurchase bool
	History     []struct{ Who, What string }
}

func (e *env) orderByNumber(token, number string) orderView {
	var list []orderView
	e.do("GET", "/api/orders", token, nil).json(&list)
	for _, o := range list {
		if o.Number == number {
			return o
		}
	}
	e.t.Fatalf("order %s not found", number)
	return orderView{}
}

func (e *env) order(token, id string) orderView {
	var o orderView
	r := e.do("GET", "/api/orders/"+id, token, nil)
	e.want(r, 200, "get order")
	r.json(&o)
	return o
}

func (e *env) calls(prefix string) int {
	n := 0
	for _, c := range e.fakes.Snapshot() {
		if strings.HasPrefix(c, prefix) {
			n++
		}
	}
	return n
}

// ---------------------------------------------------------------------------

func TestAuth(t *testing.T) {
	e := setup(t)
	e.want(e.do("GET", "/api/bootstrap", "", nil), 401, "no token")
	e.want(e.do("POST", "/api/auth/login", "", map[string]string{"email": "anna@fitkit.space", "password": "wrong"}), 401, "bad password")
	tok := e.login("ANNA@fitkit.space")
	var snap struct {
		Me        domain.Employee
		Orders    []json.RawMessage
		Tickets   []json.RawMessage
		Shipments []json.RawMessage
	}
	e.do("GET", "/api/bootstrap", tok, nil).json(&snap)
	if snap.Me.Name != "Анна Коваль" || len(snap.Orders) != 21 || len(snap.Shipments) != 6 || len(snap.Tickets) != 5 {
		t.Fatalf("bootstrap: %+v orders=%d", snap.Me, len(snap.Orders))
	}
	e.want(e.do("POST", "/api/auth/logout", tok, nil), 204, "logout")
	e.want(e.do("GET", "/api/me", tok, nil), 401, "after logout")
}

func TestLoginThrottle(t *testing.T) {
	e := setup(t)
	for i := 0; i < 10; i++ {
		e.want(e.do("POST", "/api/auth/login", "", map[string]string{"email": "taras@fitkit.space", "password": "nope"}), 401, "wrong password")
	}
	r := e.do("POST", "/api/auth/login", "", map[string]string{"email": "taras@fitkit.space", "password": seed.DemoPassword})
	e.want(r, 429, "11th attempt, even with the right password")
	e.login("anna@fitkit.space") // other accounts are not affected
}

func TestRequestCardFlow(t *testing.T) {
	e := setup(t)
	anna, taras := e.login("anna@fitkit.space"), e.login("taras@fitkit.space")
	o := e.orderByNumber(anna, "#001") // Maria, sold-out jacket
	if o.Status != "in_progress" || o.Money.ToBuy != 324800 || o.Money.Covered != 5000 || o.CanPurchase {
		t.Fatalf("seed #001: %+v", o)
	}

	// another buyer cannot touch Anna's order
	e.want(e.do("POST", "/api/orders/"+o.ID+"/status", taras, map[string]string{"status": "purchased"}), 403, "taras on anna's order")
	// blocked until the sold-out jacket is handled
	e.want(e.do("POST", "/api/orders/"+o.ID+"/status", anna, map[string]string{"status": "purchased"}), 409, "purchase blocked")

	jacket := o.Items[1]
	r := e.do("PATCH", "/api/orders/"+o.ID+"/items/"+jacket.ID, anna, map[string]string{"action": "cancel"})
	e.want(r, 200, "cancel jacket")
	r.json(&o)
	if o.Money.Refunds != 399900 || !o.CanPurchase || o.Problem != nil {
		t.Fatalf("after cancel: %+v", o)
	}
	if e.calls("refund:#001:399900") != 1 || e.calls("notify:refund") != 1 {
		t.Fatalf("refund not sent: %v", e.fakes.Snapshot())
	}

	r = e.do("POST", "/api/orders/"+o.ID+"/status", anna, map[string]string{"status": "purchased"})
	e.want(r, 200, "purchase")
	r.json(&o)
	if o.Status != "purchased" || o.Items[0].Status != "bought" || o.Items[1].Status != "cancelled" {
		t.Fatalf("after purchase: %+v", o)
	}
	var sh []domain.Shipment
	e.do("GET", "/api/shipments?order="+o.ID, anna, nil).json(&sh)
	if len(sh) != 2 || *sh[0].Store != "Zara" || *sh[1].Store != "H&M" {
		t.Fatalf("inbound shipments: %+v", sh)
	}

	// notes and problem
	e.want(e.do("POST", "/api/orders/"+o.ID+"/notes", anna, map[string]string{"text": "Купила в Zara"}), 201, "note")
	e.want(e.do("PUT", "/api/orders/"+o.ID+"/problem", anna, map[string]any{"problem": "Оплату відхилено"}), 200, "problem")
	if p := e.order(anna, o.ID).Problem; p == nil || *p != "Оплату відхилено" {
		t.Fatalf("problem: %v", p)
	}
	e.want(e.do("PUT", "/api/orders/"+o.ID+"/problem", anna, map[string]any{"problem": nil}), 200, "clear problem")
}

func TestPriceConfirmation(t *testing.T) {
	e := setup(t)
	anna := e.login("anna@fitkit.space")
	o := e.orderByNumber(anna, "#0993") // second item pending
	item := o.Items[1]
	e.want(e.do("PATCH", "/api/orders/"+o.ID+"/items/"+item.ID, anna, map[string]string{"action": "send_confirmation"}), 409, "confirm without a price rise")
	e.want(e.do("PATCH", "/api/orders/"+o.ID+"/items/"+item.ID, anna, map[string]any{"action": "store_price", "price": 320000}), 200, "store price +19%")
	r := e.do("PATCH", "/api/orders/"+o.ID+"/items/"+item.ID, anna, map[string]string{"action": "send_confirmation"})
	e.want(r, 200, "send confirmation")
	r.json(&o)
	if o.Status != "awaiting" || o.ClientSees != "Ціна змінилась (потрібна дія)" || e.calls("notify:price_confirmation") != 1 {
		t.Fatalf("awaiting: %+v", o)
	}

	// the client app answers through a signed webhook
	ans := map[string]any{"orderExternalId": "demo-0993", "itemId": item.ID, "accept": true}
	e.want(e.hook("/webhooks/client-app/confirmations", "wrong-secret", ans), 401, "bad signature")
	e.want(e.hook("/webhooks/client-app/confirmations", clientSecret, ans), 204, "client accepts")
	o = e.order(anna, o.ID)
	if o.Status != "in_progress" || *o.Items[1].Price != 320000 || o.Items[1].Status != "checked" {
		t.Fatalf("after accept: %+v", o)
	}
	if last := o.History[len(o.History)-1]; last.Who != "Клієнт" {
		t.Fatalf("history: %+v", last)
	}
}

func TestAutoRefundAfter24h(t *testing.T) {
	e := setup(t)
	anna := e.login("anna@fitkit.space")
	// #0981 was sent 26 h ago; "0995" 18 h ago and "0976" 4 h ago stay
	n, err := e.svc.ExpireConfirmations(context.Background())
	if err != nil || n != 1 {
		t.Fatalf("expire: n=%d err=%v", n, err)
	}
	o := e.orderByNumber(anna, "#0981")
	if o.Status != "in_progress" || o.Items[1].Status != "cancelled" || e.calls("refund:#0981:224000") != 1 {
		t.Fatalf("0981 after expiry: %+v %v", o, e.fakes.Snapshot())
	}
	// 10 hours later the 18-hour-old one expires too
	e.svc.Now = func() time.Time { return time.Now().Add(10 * time.Hour) }
	if n, _ := e.svc.ExpireConfirmations(context.Background()); n != 1 {
		t.Fatalf("second expiry: %d", n)
	}
}

func TestShippingFlow(t *testing.T) {
	e := setup(t)
	anna, sklad := e.login("anna@fitkit.space"), e.login("sklad@fitkit.space")
	o := e.orderByNumber(anna, "#0947") // COS parcel stuck in DHL
	e.want(e.do("POST", "/api/orders/"+o.ID+"/ship", anna, nil), 409, "ship before arrival")

	var sh []domain.Shipment
	e.do("GET", "/api/shipments?order="+o.ID, anna, nil).json(&sh)
	// aggregator webhook: parcel arrives at the warehouse
	e.want(e.hook("/webhooks/tracking", trackerSecret, map[string]any{"tracking": "48210093 77", "status": "arrived_warehouse", "title": "Доставлено на склад"}), 204, "tracking webhook")
	o = e.order(anna, o.ID)
	if o.Problem != nil || *o.Location != "На складі" {
		t.Fatalf("after arrival: problem=%v location=%v", o.Problem, *o.Location)
	}

	r := e.do("POST", "/api/orders/"+o.ID+"/ship", sklad, nil) // warehouse ships
	e.want(r, 200, "ship")
	r.json(&o)
	if o.Status != "shipped" || e.calls("np:create:#0947") != 1 || e.calls("notify:shipped") != 1 {
		t.Fatalf("after ship: %+v %v", o, e.fakes.Snapshot())
	}
	e.want(e.do("POST", "/api/orders/"+o.ID+"/ship", sklad, nil), 409, "ship twice")

	e.do("GET", "/api/shipments?order="+o.ID, anna, nil).json(&sh)
	out := sh[len(sh)-1]
	if out.Leg != domain.LegOut || out.Tracking == nil {
		t.Fatalf("outbound: %+v", out)
	}
	e.want(e.hook("/webhooks/tracking", trackerSecret, map[string]any{"tracking": *out.Tracking, "status": "delivered", "title": "Отримано"}), 204, "delivered")
	if o = e.order(anna, o.ID); o.Status != "delivered" {
		t.Fatalf("not delivered: %s", o.Status)
	}
}

func TestManualTrackingAndWarehouse(t *testing.T) {
	e := setup(t)
	taras, sklad := e.login("taras@fitkit.space"), e.login("sklad@fitkit.space")
	o := e.orderByNumber(taras, "#0951") // Nike, no tracking
	var sh []domain.Shipment
	e.do("GET", "/api/shipments?order="+o.ID, taras, nil).json(&sh)
	e.want(e.do("POST", "/api/shipments/"+sh[0].ID+"/tracking", taras, map[string]string{"tracking": "1Z999AA10123456784"}), 204, "add tracking")
	if e.calls("track:register:1Z999AA10123456784") != 1 {
		t.Fatal("tracking not registered with the aggregator")
	}
	if p := e.order(taras, o.ID).Problem; p != nil {
		t.Fatalf("problem not cleared: %s", *p)
	}
	e.want(e.do("POST", "/api/shipments/"+sh[0].ID+"/arrived", sklad, nil), 204, "warehouse receives")
	e.want(e.do("POST", "/api/orders/"+o.ID+"/status", taras, map[string]string{"status": "shipped"}), 200, "ship via status")
}

func TestFlagStalled(t *testing.T) {
	e := setup(t)
	anna := e.login("anna@fitkit.space")
	// AliExpress parcel (last event 3.5 h ago) stalls after 5 days
	e.svc.Now = func() time.Time { return time.Now().Add(6 * 24 * time.Hour) }
	n, err := e.svc.FlagStalled(context.Background())
	if err != nil || n < 1 {
		t.Fatalf("flag: n=%d err=%v", n, err)
	}
	if p := e.orderByNumber(anna, "#0972").Problem; p == nil || *p != "Немає руху 5 днів" {
		t.Fatalf("0972 problem: %v", p)
	}
}

func TestTakeIsAtomic(t *testing.T) {
	e := setup(t)
	anna, taras := e.login("anna@fitkit.space"), e.login("taras@fitkit.space")
	o := e.orderByNumber(anna, "#003") // a new request
	var wg sync.WaitGroup
	codes := make(chan int, 20)
	for i := 0; i < 20; i++ {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			tok := anna
			if i%2 == 1 {
				tok = taras
			}
			codes <- e.do("POST", "/api/orders/"+o.ID+"/take", tok, nil).code
		}(i)
	}
	wg.Wait()
	close(codes)
	ok := 0
	for c := range codes {
		if c == 200 {
			ok++
		} else if c != 409 {
			t.Fatalf("unexpected %d", c)
		}
	}
	if ok != 1 {
		t.Fatalf("%d takes succeeded, want exactly 1", ok)
	}

	// take-next hands out the oldest remaining request
	var next orderView
	r := e.do("POST", "/api/orders/take-next", anna, nil)
	e.want(r, 200, "take next")
	r.json(&next)
	if next.Number != "#009" {
		t.Fatalf("take-next gave %s", next.Number)
	}
}

func TestCancelOrder(t *testing.T) {
	e := setup(t)
	anna := e.login("anna@fitkit.space")
	o := e.orderByNumber(anna, "#001")
	e.do("PATCH", "/api/orders/"+o.ID+"/items/"+o.Items[1].ID, anna, map[string]string{"action": "cancel"}) // jacket refunded first
	r := e.do("POST", "/api/orders/"+o.ID+"/cancel", anna, nil)
	e.want(r, 200, "cancel order")
	r.json(&o)
	// 7 777 paid − 3 999 already refunded = 3 778 refunded now
	if o.Status != "cancelled" || e.calls("refund:#001:377800") != 1 {
		t.Fatalf("cancel: %s %v", o.Status, e.fakes.Snapshot())
	}
}

func TestClientAppWebhooksAndSupport(t *testing.T) {
	e := setup(t)
	anna := e.login("anna@fitkit.space")
	newOrder := map[string]any{
		"externalId": "app-555", "paid": 249900, "fee": 18000,
		"client": map[string]any{"externalId": "app-client-9", "name": "Софія Гнатюк", "phone": "+380 67 000 11 22", "city": "Львів", "branch": "Відділення №1"},
		"items":  []map[string]any{{"name": "Худі", "store": "Zara", "url": "https://www.zara.com/ua/x", "size": "M", "qty": 1, "price": 249900}},
	}
	r := e.hook("/webhooks/client-app/orders", clientSecret, newOrder)
	e.want(r, 200, "ingest")
	var first, again struct{ ID, Number string }
	r.json(&first)
	e.hook("/webhooks/client-app/orders", clientSecret, newOrder).json(&again)
	if first.ID != again.ID || first.Number == "" {
		t.Fatalf("ingest is not idempotent: %v %v", first, again)
	}
	if o := e.order(anna, first.ID); o.Status != "new" || o.ClientSees != "Замовлення підтверджено" {
		t.Fatalf("ingested: %+v", o)
	}

	r = e.hook("/webhooks/client-app/messages", clientSecret, map[string]string{"clientExternalId": "app-client-9", "orderExternalId": "app-555", "text": "Коли буде?"})
	e.want(r, 200, "client message")
	var tk struct{ TicketID string }
	r.json(&tk)
	e.want(e.do("POST", "/api/tickets/"+tk.TicketID+"/messages", anna, map[string]string{"text": "Завтра відправимо"}), 201, "reply")
	if e.calls("notify:support_reply") != 1 {
		t.Fatal("reply not sent to the client app")
	}
	e.want(e.do("POST", "/api/tickets/"+tk.TicketID+"/resolve", anna, nil), 204, "resolve")
	e.want(e.do("POST", "/api/tickets/"+tk.TicketID+"/messages", anna, map[string]string{"text": "ще"}), 409, "reply to resolved")
	// a new client message after resolve opens a new ticket
	var tk2 struct{ TicketID string }
	e.hook("/webhooks/client-app/messages", clientSecret, map[string]string{"clientExternalId": "app-client-9", "orderExternalId": "app-555", "text": "Ще питання"}).json(&tk2)
	if tk2.TicketID == tk.TicketID {
		t.Fatal("resolved ticket was reopened")
	}
}

func TestEventsStream(t *testing.T) {
	e := setup(t)
	anna := e.login("anna@fitkit.space")
	req, _ := http.NewRequest("GET", e.srv.URL+"/api/events", nil)
	req.Header.Set("Authorization", "Bearer "+anna)
	resp, err := http.DefaultClient.Do(req)
	if err != nil || resp.StatusCode != 200 {
		t.Fatalf("events: %v %v", err, resp)
	}
	defer resp.Body.Close()
	got := make(chan string, 1)
	go func() {
		buf := make([]byte, 4096)
		acc := ""
		for {
			n, err := resp.Body.Read(buf)
			acc += string(buf[:n])
			if strings.Contains(acc, "event: order") {
				got <- acc
				return
			}
			if err != nil {
				return
			}
		}
	}()
	time.Sleep(100 * time.Millisecond)
	o := e.orderByNumber(anna, "#004")
	e.do("POST", "/api/orders/"+o.ID+"/take", anna, nil)
	select {
	case s := <-got:
		if !strings.Contains(s, o.ID) {
			t.Fatalf("event without order id: %s", s)
		}
	case <-time.After(3 * time.Second):
		t.Fatal("no event received")
	}
}
