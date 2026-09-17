package api

import (
	"bytes"
	"context"
	"encoding/json"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"testing"
	"time"

	"fitkit/server/internal/analyze"
	"fitkit/server/internal/config"
	"fitkit/server/internal/importer"
	"fitkit/server/internal/pinterest"
	"fitkit/server/internal/testutil"
)

type stubDiscoverer struct{ pins []pinterest.Pin }

func (s stubDiscoverer) Discover(context.Context, string, int) ([]pinterest.Pin, error) {
	return s.pins, nil
}

type client struct {
	t     *testing.T
	base  string
	token string
}

func (c *client) do(method, path string, body any, out any) int {
	c.t.Helper()
	var r io.Reader
	if body != nil {
		data, _ := json.Marshal(body)
		r = bytes.NewReader(data)
	}
	req, _ := http.NewRequest(method, c.base+path, r)
	if c.token != "" {
		req.Header.Set("Authorization", "Bearer "+c.token)
	}
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		c.t.Fatal(err)
	}
	defer resp.Body.Close()
	if out != nil {
		json.NewDecoder(resp.Body).Decode(out)
	}
	return resp.StatusCode
}

func newTestServer(t *testing.T, cfg config.Config) *client {
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
		Discoverer: stubDiscoverer{pins: []pinterest.Pin{
			{ID: "900", Title: "Street style", ImageURL: images.URL + "/a.jpg"},
			{ID: "901", Title: "Coat", ImageURL: images.URL + "/b.jpg"},
		}},
	}
	if err := imp.Start(t.Context()); err != nil {
		t.Fatal(err)
	}
	srv := &Server{
		Store: st, Importer: imp, MediaDir: mediaDir, SessionTTL: time.Hour, Log: log,
		Analysis: analyze.NewService(cfg, st, filepath.Join(mediaDir, "pins"), filepath.Join(mediaDir, "crops"), log),
	}
	ts := httptest.NewServer(srv.Handler())
	t.Cleanup(ts.Close)
	return &client{t: t, base: ts.URL}
}

func eventually(t *testing.T, cond func() bool) {
	t.Helper()
	deadline := time.Now().Add(5 * time.Second)
	for !cond() {
		if time.Now().After(deadline) {
			t.Fatal("condition not met in time")
		}
		time.Sleep(20 * time.Millisecond)
	}
}

func TestFullFlow(t *testing.T) {
	c := newTestServer(t, config.Config{DemoAnalysis: true})

	// Auth.
	var errBody map[string]apiError
	if code := c.do("POST", "/v1/auth/signup", credentials{"not-an-email", "password123"}, &errBody); code != 422 {
		t.Fatalf("invalid email status %d", code)
	}
	var auth struct {
		Token string   `json:"token"`
		User  userJSON `json:"user"`
	}
	if code := c.do("POST", "/v1/auth/signup", credentials{" Jane@Example.com", "password123"}, &auth); code != 201 || auth.Token == "" {
		t.Fatalf("signup status %d %+v", code, auth)
	}
	if code := c.do("POST", "/v1/auth/signup", credentials{"jane@example.com", "password123"}, nil); code != 409 {
		t.Fatalf("duplicate signup status %d", code)
	}
	if code := c.do("POST", "/v1/auth/login", credentials{"jane@example.com", "wrong-password"}, nil); code != 401 {
		t.Fatalf("bad login status %d", code)
	}
	if code := c.do("POST", "/v1/auth/login", credentials{"JANE@example.com", "password123"}, &auth); code != 200 {
		t.Fatalf("login status %d", code)
	}
	if code := c.do("GET", "/v1/me", nil, nil); code != 401 {
		t.Fatalf("unauthenticated /me status %d", code)
	}
	c.token = auth.Token

	// Import requires a Pinterest handle.
	if code := c.do("POST", "/v1/imports", nil, nil); code != 422 {
		t.Fatalf("import without handle status %d", code)
	}

	// Onboarding steps save independently.
	var me userJSON
	if code := c.do("PUT", "/v1/me/referral-source", map[string]string{"source": "tiktok"}, &me); code != 200 || *me.ReferralSource != "tiktok" {
		t.Fatalf("referral status %d %+v", code, me)
	}
	if code := c.do("PUT", "/v1/me/referral-source", map[string]string{"source": "billboard"}, nil); code != 422 {
		t.Fatalf("bad referral status %d", code)
	}
	if code := c.do("PUT", "/v1/me/pinterest", map[string]string{"handle": "https://www.pinterest.com/pin/1/"}, nil); code != 422 {
		t.Fatalf("pin link as handle status %d", code)
	}
	c.do("PUT", "/v1/me/pinterest", map[string]string{"handle": "https://www.pinterest.com/Jane_Doe/_saved/"}, &me)
	c.do("GET", "/v1/me", nil, &me)
	if me.PinterestUsername == nil || *me.PinterestUsername != "jane_doe" || *me.ReferralSource != "tiktok" {
		t.Fatalf("me = %+v", me)
	}

	// Import runs in the background; client polls.
	var job importJSON
	if code := c.do("POST", "/v1/imports", nil, &job); code != 202 {
		t.Fatalf("import status %d", code)
	}
	eventually(t, func() bool {
		c.do("GET", "/v1/imports/"+job.ID, nil, &job)
		return job.Status == "done"
	})
	if job.Discovered != 2 || job.Imported != 2 {
		t.Fatalf("job = %+v", job)
	}

	var page struct {
		Pins       []pinJSON `json:"pins"`
		NextCursor *string   `json:"nextCursor"`
	}
	c.do("GET", "/v1/pins?limit=1", nil, &page)
	if len(page.Pins) != 1 || page.NextCursor == nil || page.Pins[0].PinterestID != "900" {
		t.Fatalf("page 1 = %+v", page)
	}
	first := page.Pins[0]
	c.do("GET", "/v1/pins?limit=1&cursor="+*page.NextCursor, nil, &page)
	if len(page.Pins) != 1 || page.Pins[0].PinterestID != "901" {
		t.Fatalf("page 2 = %+v", page)
	}
	if first.Width != 300 || first.Height != 450 {
		t.Errorf("pin dimensions = %dx%d", first.Width, first.Height)
	}

	resp, err := http.Get(c.base + first.ImageURL)
	if err != nil || resp.StatusCode != 200 {
		t.Fatalf("media fetch: %v %v", err, resp.StatusCode)
	}
	resp.Body.Close()

	// Analysis (demo providers).
	var analysis struct {
		Status string          `json:"status"`
		Result *analyze.Result `json:"result"`
	}
	c.do("GET", "/v1/pins/"+first.ID+"/analysis", nil, &analysis)
	if analysis.Status != "none" {
		t.Fatalf("initial analysis status %q", analysis.Status)
	}
	if code := c.do("POST", "/v1/pins/"+first.ID+"/analysis", nil, &analysis); code != 202 {
		t.Fatalf("start analysis status %d", code)
	}
	eventually(t, func() bool {
		c.do("GET", "/v1/pins/"+first.ID+"/analysis", nil, &analysis)
		return analysis.Status == "done"
	})
	if analysis.Result == nil || len(analysis.Result.Items) != 3 || !analysis.Result.Demo {
		t.Fatalf("analysis = %+v", analysis.Result)
	}
	crop, err := http.Get(c.base + analysis.Result.Items[0].CropURL)
	if err != nil || crop.StatusCode != 200 {
		t.Fatalf("crop fetch: %v %d", err, crop.StatusCode)
	}
	crop.Body.Close()

	// Removing a pin hides it from the grid but keeps it on the server.
	if code := c.do("PUT", "/v1/pins/"+first.ID+"/hidden", nil, nil); code != 204 {
		t.Fatalf("hide status %d", code)
	}
	c.do("GET", "/v1/pins", nil, &page)
	if len(page.Pins) != 1 || page.Pins[0].ID == first.ID {
		t.Fatalf("pins after hide = %+v", page.Pins)
	}
	if code := c.do("GET", "/v1/pins/"+first.ID+"/analysis", nil, nil); code != 200 {
		t.Fatalf("hidden pin analysis status %d", code)
	}
	c.do("POST", "/v1/imports?force=true", nil, &job)
	eventually(t, func() bool {
		c.do("GET", "/v1/imports/"+job.ID, nil, &job)
		return job.Status == "done"
	})
	c.do("GET", "/v1/pins", nil, &page)
	if len(page.Pins) != 1 {
		t.Fatalf("re-import brought back a hidden pin: %+v", page.Pins)
	}
	if code := c.do("DELETE", "/v1/pins/"+first.ID+"/hidden", nil, nil); code != 204 {
		t.Fatalf("unhide status %d", code)
	}
	c.do("GET", "/v1/pins", nil, &page)
	if len(page.Pins) != 2 {
		t.Fatalf("pins after unhide = %+v", page.Pins)
	}

	// The cart keeps a pin even after it's hidden from the grid.
	if code := c.do("PUT", "/v1/cart/"+first.ID, nil, nil); code != 204 {
		t.Fatalf("add to cart status %d", code)
	}
	var cart struct {
		Pins []pinJSON `json:"pins"`
	}
	c.do("GET", "/v1/cart", nil, &cart)
	if len(cart.Pins) != 1 || cart.Pins[0].ID != first.ID {
		t.Fatalf("cart = %+v", cart.Pins)
	}
	if code := c.do("PUT", "/v1/cart/"+first.ID, nil, nil); code != 204 {
		t.Fatalf("second add to cart status %d", code)
	}
	c.do("GET", "/v1/cart", nil, &cart)
	if len(cart.Pins) != 1 {
		t.Fatalf("cart after duplicate add = %+v", cart.Pins)
	}
	if code := c.do("DELETE", "/v1/cart/"+first.ID, nil, nil); code != 204 {
		t.Fatalf("remove from cart status %d", code)
	}
	c.do("GET", "/v1/cart", nil, &cart)
	if len(cart.Pins) != 0 {
		t.Fatalf("cart after removal = %+v", cart.Pins)
	}

	// Other users can't read pins they haven't saved.
	other := &client{t: t, base: c.base}
	var otherAuth struct {
		Token string `json:"token"`
	}
	other.do("POST", "/v1/auth/signup", credentials{"sam@example.com", "password123"}, &otherAuth)
	other.token = otherAuth.Token
	if code := other.do("GET", "/v1/pins/"+first.ID+"/analysis", nil, nil); code != 404 {
		t.Fatalf("foreign pin analysis status %d", code)
	}
	if code := other.do("PUT", "/v1/pins/"+first.ID+"/hidden", nil, nil); code != 404 {
		t.Fatalf("foreign pin hide status %d", code)
	}
	if code := other.do("PUT", "/v1/cart/"+first.ID, nil, nil); code != 404 {
		t.Fatalf("foreign pin add to cart status %d", code)
	}
	if code := other.do("GET", "/v1/imports/"+job.ID, nil, nil); code != 404 {
		t.Fatalf("foreign import status %d", code)
	}

	// Logout invalidates the token; account deletion removes the user.
	if code := other.do("DELETE", "/v1/me", nil, nil); code != 204 {
		t.Fatalf("delete account status %d", code)
	}
	if code := other.do("GET", "/v1/me", nil, nil); code != 401 {
		t.Fatalf("deleted account /me status %d", code)
	}
	if code := c.do("POST", "/v1/auth/logout", nil, nil); code != 204 {
		t.Fatalf("logout status %d", code)
	}
	if code := c.do("GET", "/v1/me", nil, nil); code != 401 {
		t.Fatalf("after logout /me status %d", code)
	}
}

func TestAnalysisNotConfigured(t *testing.T) {
	c := newTestServer(t, config.Config{LensProvider: "serpapi"})
	var auth struct {
		Token string `json:"token"`
	}
	c.do("POST", "/v1/auth/signup", credentials{"k@example.com", "password123"}, &auth)
	c.token = auth.Token
	c.do("PUT", "/v1/me/pinterest", map[string]string{"handle": "kay"}, nil)
	var job importJSON
	c.do("POST", "/v1/imports", nil, &job)
	eventually(t, func() bool {
		c.do("GET", "/v1/imports/latest", nil, &job)
		return job.Status == "done"
	})
	var page struct {
		Pins []pinJSON `json:"pins"`
	}
	c.do("GET", "/v1/pins", nil, &page)
	var analysis struct {
		Status string   `json:"status"`
		Error  apiError `json:"error"`
	}
	c.do("POST", "/v1/pins/"+page.Pins[0].ID+"/analysis", nil, &analysis)
	if analysis.Status != "failed" || analysis.Error.Code != "not_configured" {
		t.Fatalf("analysis = %+v", analysis)
	}
}

type testWriter struct{ t *testing.T }

func (w testWriter) Write(p []byte) (int, error) {
	w.t.Log(string(bytes.TrimSpace(p)))
	return len(p), nil
}

func TestGeminiOnlyConfigurationIsUsable(t *testing.T) {
	st := testutil.OpenStore(t)
	svc := analyze.NewService(config.Config{GeminiAPIKey: "k", GeminiModel: "m", LensProvider: "serpapi"}, st, t.TempDir(), t.TempDir(), slog.New(slog.NewTextHandler(io.Discard, nil)))
	if svc.Pipeline == nil || svc.Pipeline.Searcher.Name() != "search-links" || len(svc.Missing) != 2 {
		t.Fatalf("pipeline = %+v, missing = %v", svc.Pipeline, svc.Missing)
	}
}
