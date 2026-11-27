package api

import (
	"bytes"
	"encoding/json"

	"log/slog"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"golang.org/x/crypto/bcrypt"

	"fitkit/server/internal/analyze"
	"fitkit/server/internal/config"
	"fitkit/server/internal/importer"
	"fitkit/server/internal/pinterest"
	"fitkit/server/internal/shop"
	"fitkit/server/internal/testutil"
	wsdomain "fitkit/server/internal/workspace/domain"
	wshttp "fitkit/server/internal/workspace/httpapi"
	"fitkit/server/internal/workspace/integrations"
	wsservice "fitkit/server/internal/workspace/service"
	wsstore "fitkit/server/internal/workspace/store"
)

// newShopServer is newTestServer plus the employee workspace, routed like fitkitd:
// /v1 and /media to the app API, everything else to the workspace API.
func newShopServer(t *testing.T) (*client, *wsservice.Service) {
	t.Helper()
	img := testutil.JPEG(t, 300, 450)
	images := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { w.Write(img) }))
	t.Cleanup(images.Close)
	st := testutil.OpenStore(t)
	mediaDir := t.TempDir()
	if err := EnsureMediaDirs(mediaDir); err != nil {
		t.Fatal(err)
	}
	log := slog.New(slog.NewTextHandler(testWriter{t}, nil))
	imp := &importer.Importer{
		Store: st, MediaDir: filepath.Join(mediaDir, "pins"), Limit: 10, Log: log,
		Discoverer: stubDiscoverer{pins: []pinterest.Pin{{ID: "900", Title: "Street style", ImageURL: images.URL + "/a.jpg"}}},
	}
	if err := imp.Start(t.Context()); err != nil {
		t.Fatal(err)
	}
	ws := wsstore.New(st.DB())
	if err := ws.Migrate(t.Context()); err != nil {
		t.Fatal(err)
	}
	_, ints := integrations.NewFakes(log)
	hub := wshttp.NewHub()
	svc := &wsservice.Service{Store: ws, Int: ints, Events: hub, Log: log}
	cfg := config.Config{DemoAnalysis: true}
	app := (&Server{
		Store: st, Importer: imp, MediaDir: mediaDir, SessionTTL: time.Hour, Log: log, Workspace: svc,
		Analysis: analyze.NewService(cfg, st, filepath.Join(mediaDir, "pins"), filepath.Join(mediaDir, "crops"), log),
		Shop:     &shop.Service{Cache: st, Demo: true},
	}).Handler()
	mux := http.NewServeMux()
	mux.Handle("/v1/", app)
	mux.Handle("/media/", app)
	mux.Handle("/", wshttp.New(wshttp.Config{Service: svc, Hub: hub, Log: log}))
	ts := httptest.NewServer(mux)
	t.Cleanup(ts.Close)
	return &client{t: t, base: ts.URL}, svc
}

// analyzedPin imports the user's pins and runs the demo analysis on the first one.
func analyzedPin(t *testing.T, c *client) (string, *analyze.Result) {
	t.Helper()
	c.do("PUT", "/v1/me/pinterest", map[string]string{"handle": "ann"}, nil)
	var job importJSON
	c.do("POST", "/v1/imports", nil, &job)
	eventually(t, func() bool { c.do("GET", "/v1/imports/"+job.ID, nil, &job); return job.Status == "done" })
	var page struct {
		Pins []pinJSON `json:"pins"`
	}
	c.do("GET", "/v1/pins", nil, &page)
	pinID := page.Pins[0].ID
	var a struct {
		Status string          `json:"status"`
		Result *analyze.Result `json:"result"`
	}
	c.do("POST", "/v1/pins/"+pinID+"/analysis", nil, &a)
	eventually(t, func() bool { c.do("GET", "/v1/pins/"+pinID+"/analysis", nil, &a); return a.Status == "done" })
	return pinID, a.Result
}

// employee signs a workspace employee in and returns a cookie-less bearer client for /api.
func employee(t *testing.T, base string, svc *wsservice.Service) *client {
	t.Helper()
	hash, _ := bcrypt.GenerateFromPassword([]byte("buyer-password"), bcrypt.MinCost)
	if _, err := svc.Store.Q().CreateEmployee(t.Context(), "Анна Коваль", "anna@fitkit.space", wsdomain.RoleBuyer, string(hash)); err != nil {
		t.Fatal(err)
	}
	body, _ := json.Marshal(map[string]string{"email": "anna@fitkit.space", "password": "buyer-password"})
	resp, err := http.Post(base+"/api/auth/login", "application/json", bytes.NewReader(body))
	if err != nil || resp.StatusCode != 200 {
		t.Fatalf("employee login: %v %v", err, resp.Status)
	}
	defer resp.Body.Close()
	var out struct{ Token string }
	json.NewDecoder(resp.Body).Decode(&out)
	return &client{t: t, base: base, token: out.Token}
}

func TestBuyPlacesWorkspaceOrder(t *testing.T) {
	c0, svc := newShopServer(t)
	c := signedIn(t, c0, "ann@example.com")
	other := signedIn(t, c0, "sam@example.com")
	pinID, result := analyzedPin(t, c)
	top, second := result.Items[0], result.Items[1]
	c.do("PUT", "/v1/sizes", map[string]any{"sizes": map[string]string{second.Category: "S"}}, nil)

	valid := func() map[string]any {
		return map[string]any{
			"pinId": pinID, "name": "Аня 🌸", "idempotencyKey": "tap-1",
			"items": []map[string]any{
				{"itemId": top.ID, "listingUrl": top.Listings[0].URL, "size": "M", "quantity": 2, "comment": "Чорний, як на фото"},
				{"itemId": second.ID, "listingUrl": second.Listings[1].URL},
			},
		}
	}
	for name, mutate := range map[string]func(map[string]any){
		"no name":  func(b map[string]any) { b["name"] = "  " },
		"no items": func(b map[string]any) { b["items"] = []any{} },
		"unknown listing": func(b map[string]any) {
			b["items"] = []map[string]any{{"itemId": top.ID, "listingUrl": "https://evil.example/x"}}
		},
		"bad quantity": func(b map[string]any) {
			b["items"] = []map[string]any{{"itemId": top.ID, "listingUrl": top.Listings[0].URL, "quantity": 50}}
		},
	} {
		b := valid()
		mutate(b)
		if code := c.do("POST", "/v1/orders", b, nil); code != 422 {
			t.Errorf("%s: status %d", name, code)
		}
	}
	if code := other.do("POST", "/v1/orders", valid(), nil); code != 404 {
		t.Errorf("buying someone else's pin: %d", code)
	}

	var order orderJSON
	if code := c.do("POST", "/v1/orders", valid(), &order); code != 201 {
		t.Fatalf("buy: %d", code)
	}
	if !strings.HasPrefix(order.Number, "#") || order.Status != "Замовлення підтверджено" || len(order.Items) != 2 || !strings.HasPrefix(order.PhotoURL, "/media/pins/") {
		t.Fatalf("order: %+v", order)
	}
	if it := order.Items[0]; it.Name != top.Label || it.Size != "M" || it.Quantity != 2 || it.Price != nil || it.ListingPrice != top.Listings[0].Price || it.Store != "Fitkit Demo Shop" {
		t.Fatalf("first piece: %+v", it)
	}
	if order.Items[1].Size != "S" || order.Total != nil {
		t.Fatalf("saved size / total: %+v", order)
	}
	// A retried tap with the same key returns the same order.
	var again orderJSON
	c.do("POST", "/v1/orders", valid(), &again)
	if again.ID != order.ID || again.Number != order.Number {
		t.Fatalf("idempotency: %s vs %s", again.Number, order.Number)
	}

	var mine struct{ Orders []orderJSON }
	c.do("GET", "/v1/orders", nil, &mine)
	if len(mine.Orders) != 1 || mine.Orders[0].Number != order.Number {
		t.Fatalf("my orders: %+v", mine)
	}
	other.do("GET", "/v1/orders", nil, &mine)
	if len(mine.Orders) != 0 {
		t.Fatal("another user sees the order")
	}
	if code := other.do("GET", "/v1/orders/"+order.ID, nil, nil); code != 404 {
		t.Fatalf("foreign order: %d", code)
	}

	// The desktop workspace sees it as a real new request.
	buyer := employee(t, c0.base, svc)
	var requests []struct {
		ID, Number, ClientID, Status string
		Photo                        string
		Items                        []struct{ Name, Store, Size, Status, Photo, ListingPrice string }
	}
	buyer.do("GET", "/api/orders?status=new", nil, &requests)
	if len(requests) != 1 || requests[0].Number != order.Number || requests[0].Photo != order.PhotoURL || requests[0].Items[0].ListingPrice == "" || requests[0].Items[0].Status != "pending" {
		t.Fatalf("workspace requests: %+v", requests)
	}
	client, err := svc.Store.Q().Client(t.Context(), requests[0].ClientID)
	if err != nil || client.Name != "Аня 🌸" {
		t.Fatalf("client: %+v %v", client, err)
	}

	// A buyer takes it; the app shows the client-facing status.
	if code := buyer.do("POST", "/api/orders/"+order.ID+"/take", nil, nil); code != 200 {
		t.Fatalf("take: %d", code)
	}
	c.do("GET", "/v1/orders/"+order.ID, nil, &order)
	if order.Status != "Готуємо ваше замовлення" {
		t.Fatalf("status after take: %q", order.Status)
	}

	// Complaints from the app count against the employee who has the order.
	if code := c.do("POST", "/v1/orders/"+order.ID+"/complaints", map[string]string{"text": "  "}, nil); code != 422 {
		t.Fatalf("empty complaint: %d", code)
	}
	if code := other.do("POST", "/v1/orders/"+order.ID+"/complaints", map[string]string{"text": "Not mine"}, nil); code != 404 {
		t.Fatalf("complaint about someone else's order: %d", code)
	}
	if code := c.do("POST", "/v1/orders/"+order.ID+"/complaints", map[string]string{"text": "Розмір не той"}, nil); code != 201 {
		t.Fatalf("complaint: %d", code)
	}
	stats, err := svc.Store.Q().AllEmployeeStats(t.Context())
	if err != nil {
		t.Fatal(err)
	}
	total := 0
	for _, st := range stats {
		total += st.Complaints
	}
	if total != 1 {
		t.Fatalf("complaints counted: %+v", stats)
	}
}

func TestBuyNeedsAnalysis(t *testing.T) {
	c0, _ := newShopServer(t)
	c := signedIn(t, c0, "ann@example.com")
	c.do("PUT", "/v1/me/pinterest", map[string]string{"handle": "ann"}, nil)
	var job importJSON
	c.do("POST", "/v1/imports", nil, &job)
	eventually(t, func() bool { c.do("GET", "/v1/imports/"+job.ID, nil, &job); return job.Status == "done" })
	var page struct {
		Pins []pinJSON `json:"pins"`
	}
	c.do("GET", "/v1/pins", nil, &page)
	if code := c.do("POST", "/v1/orders", map[string]any{"pinId": page.Pins[0].ID, "name": "Ann", "items": []map[string]any{{"itemId": "item-1", "listingUrl": "x"}}}, nil); code != 409 {
		t.Fatalf("buy before analysis: %d", code)
	}
}

func TestUAHPrice(t *testing.T) {
	v := func(f float64) *float64 { return &f }
	for _, c := range []struct {
		l    analyze.Listing
		want int64
	}{
		{analyze.Listing{Price: "1 799 ₴", PriceValue: v(1799), Currency: "UAH"}, 179900},
		{analyze.Listing{Price: "1799,50 грн", PriceValue: v(1799.5)}, 179950},
		{analyze.Listing{Price: "$49.99", PriceValue: v(49.99), Currency: "USD"}, 0},
		{analyze.Listing{Price: "", Currency: "UAH"}, 0},
	} {
		got := uahPrice(c.l)
		if (got == nil) != (c.want == 0) || (got != nil && *got != c.want) {
			t.Errorf("%q: got %v, want %d", c.l.Price, got, c.want)
		}
	}
}
