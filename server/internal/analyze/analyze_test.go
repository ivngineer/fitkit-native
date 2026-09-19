package analyze

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"image"
	"io"
	"math"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"

	"fitkit/server/internal/config"
	"fitkit/server/internal/testutil"
)

type fakeDetector struct {
	detections []Detection
	err        error
}

func (f fakeDetector) Detect(context.Context, []byte, string) ([]Detection, error) {
	return f.detections, f.err
}

type fakeUploader struct {
	mu      sync.Mutex
	uploads map[string][]byte
}

func (f *fakeUploader) Upload(_ context.Context, data []byte, name string) (string, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.uploads[name] = data
	return "https://img.example/" + name, nil
}

type fakeSearcher struct{ failFor string }

func (fakeSearcher) Name() string { return "fake" }

func (f fakeSearcher) Search(_ context.Context, url string, _ Detection) ([]Listing, error) {
	if f.failFor != "" && strings.Contains(url, f.failFor) {
		return nil, errors.New("boom")
	}
	return []Listing{
		{Title: "No price", Merchant: "A", URL: "https://a.example/1"},
		{Title: "Priced", Merchant: "B", URL: "https://b.example/1", Price: "$20"},
		{Title: "Dupe", Merchant: "B", URL: "https://b.example/1", Price: "$20"},
		{Title: "", URL: "https://c.example/untitled"},
	}, nil
}

func TestPipelineRun(t *testing.T) {
	up := &fakeUploader{uploads: map[string][]byte{}}
	p := &Pipeline{
		Detector: fakeDetector{detections: []Detection{
			{Label: "Jacket", Category: "outerwear", Box: Box{X: 0.1, Y: 0.1, Width: 0.5, Height: 0.4}},
			{Label: "Boots", Category: "shoes", Box: Box{X: 0.3, Y: 0.7, Width: 0.4, Height: 0.25}},
		}},
		Uploader: up,
		Searcher: fakeSearcher{failFor: "item-2"},
	}
	res, err := p.Run(context.Background(), testutil.JPEG(t, 400, 600), "image/jpeg")
	if err != nil {
		t.Fatal(err)
	}
	if len(res.Items) != 2 || res.Provider != "fake" {
		t.Fatalf("result = %+v", res)
	}
	jacket := res.Items[0]
	if jacket.CropURL != "https://img.example/item-1.jpg" || len(jacket.Listings) != 2 || jacket.Listings[0].Title != "Priced" {
		t.Errorf("jacket = %+v", jacket)
	}
	if res.Items[1].Error == "" {
		t.Error("boots search failure should be recorded per item")
	}

	crop, _, err := image.DecodeConfig(bytes.NewReader(up.uploads["item-1.jpg"]))
	if err != nil {
		t.Fatal(err)
	}
	// 0.5 * 400 wide plus 6% padding each side, clamped inside the image.
	if crop.Width < 200 || crop.Width > 230 || crop.Height < 240 || crop.Height > 270 {
		t.Errorf("crop size = %dx%d", crop.Width, crop.Height)
	}
}

func TestPipelineKeepsOneDetectionPerKind(t *testing.T) {
	p := &Pipeline{
		Detector: fakeDetector{detections: []Detection{
			{Label: "Watch", Category: "accessory", Box: Box{X: 0.1, Y: 0.1, Width: 0.2, Height: 0.2}},
			{Label: "Hoodie", Category: "top", Box: Box{X: 0, Y: 0, Width: 0.4, Height: 0.3}},
			// The same hoodie boxed again, larger: it replaces the first.
			{Label: "Grey hoodie", Category: "top", Box: Box{X: 0, Y: 0, Width: 0.6, Height: 0.5}},
			// Another accessory keeps its own row, unlike a second top.
			{Label: "Belt", Category: "accessory", Box: Box{X: 0.4, Y: 0.5, Width: 0.3, Height: 0.1}},
		}},
		Uploader: &fakeUploader{uploads: map[string][]byte{}},
		Searcher: fakeSearcher{},
	}
	res, err := p.Run(context.Background(), testutil.JPEG(t, 400, 600), "image/jpeg")
	if err != nil {
		t.Fatal(err)
	}
	labels := []string{}
	for _, it := range res.Items {
		labels = append(labels, it.Label)
	}
	if len(labels) != 3 || labels[1] != "Grey hoodie" {
		t.Fatalf("labels = %v", labels)
	}
}

func TestRankListingsSkipsPinterest(t *testing.T) {
	got := rankListings([]Listing{
		{Title: "Pin", Merchant: "Pinterest", URL: "https://www.pinterest.com/pin/1/"},
		{Title: "Pin", Merchant: "Pinterest", URL: "https://ru.pinterest.com/pin/2/"},
		{Title: "Hoodie", Merchant: "Shop", URL: "https://shop.example/hoodie"},
	})
	if len(got) != 1 || got[0].Merchant != "Shop" {
		t.Fatalf("listings = %+v", got)
	}
}

func TestPipelineNoItems(t *testing.T) {
	p := &Pipeline{Detector: fakeDetector{}, Uploader: &fakeUploader{}, Searcher: fakeSearcher{}}
	if _, err := p.Run(context.Background(), testutil.JPEG(t, 100, 100), "image/jpeg"); !errors.Is(err, ErrNoItems) {
		t.Fatalf("err = %v", err)
	}
}

func TestCropClampsToBounds(t *testing.T) {
	src, _, _ := image.Decode(bytes.NewReader(testutil.JPEG(t, 400, 300)))
	data, err := Crop(src, Box{X: 0.9, Y: -0.2, Width: 0.5, Height: 1.5}, 0.1)
	if err != nil {
		t.Fatal(err)
	}
	cfg, _, _ := image.DecodeConfig(bytes.NewReader(data))
	if cfg.Width != 60 || cfg.Height != 300 {
		t.Errorf("crop = %dx%d, want 60x300", cfg.Width, cfg.Height)
	}
	if _, err := Crop(src, Box{X: 0.5, Y: 0.5, Width: 0.01, Height: 0.01}, 0); err == nil {
		t.Error("tiny crop should fail")
	}
}

func TestParseGeminiDetections(t *testing.T) {
	text := "```json\n" + `[
		{"label": "Leather bag", "category": "Bag", "description": "brown tote", "box_2d": [500, 100, 800, 400]},
		{"label": "", "category": "top", "box_2d": [0, 0, 100, 100]},
		{"label": "Speck", "category": "jewelry", "box_2d": [10, 10, 12, 12]},
		{"label": "Bad", "box_2d": [1, 2, 3]}
	]` + "\n```"
	got, err := parseGeminiDetections(text)
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 1 {
		t.Fatalf("got %d detections: %+v", len(got), got)
	}
	d := got[0]
	if d.Category != "bag" || !near(d.Box.X, 0.1) || !near(d.Box.Y, 0.5) || !near(d.Box.Width, 0.3) || !near(d.Box.Height, 0.3) {
		t.Errorf("detection = %+v", d)
	}
}

func TestGeminiRequest(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("x-goog-api-key") != "k" || !strings.HasSuffix(r.URL.Path, "/models/gemini-test:generateContent") {
			http.Error(w, "bad request", 400)
			return
		}
		body, _ := io.ReadAll(r.Body)
		if !bytes.Contains(body, []byte(`"mime_type":"image/jpeg"`)) {
			http.Error(w, "no image", 400)
			return
		}
		inner := `[{"label":"Hat","category":"hat","box_2d":[0,0,500,500]}]`
		json.NewEncoder(w).Encode(map[string]any{"candidates": []any{map[string]any{
			"content": map[string]any{"parts": []any{map[string]any{"text": inner}}}}}})
	}))
	defer srv.Close()
	g := &Gemini{APIKey: "k", Model: "gemini-test", BaseURL: srv.URL}
	got, err := g.Detect(context.Background(), []byte("img"), "image/jpeg")
	if err != nil || len(got) != 1 || got[0].Label != "Hat" {
		t.Fatalf("got %+v, %v", got, err)
	}
}

func TestImgbbUpload(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Query().Get("key") != "k" || r.FormValue("image") == "" {
			http.Error(w, "bad", 400)
			return
		}
		w.Write([]byte(`{"success":true,"data":{"url":"https://i.ibb.co/x/item.jpg"}}`))
	}))
	defer srv.Close()
	url, err := (&Imgbb{APIKey: "k", BaseURL: srv.URL}).Upload(context.Background(), []byte{1, 2, 3}, "item.jpg")
	if err != nil || url != "https://i.ibb.co/x/item.jpg" {
		t.Fatalf("url = %q, err = %v", url, err)
	}
}

func TestLensProviders(t *testing.T) {
	responses := map[string]string{
		"/search.json":   `{"visual_matches":[{"title":"Serp boot","link":"https://shop.example/boot","source":"Shop","thumbnail":"https://t/1.jpg","price":{"value":"$89.00","extracted_value":89,"currency":"$"},"in_stock":true}]}`,
		"/google_lens":   `{"lens_results":[{"title":"Dog boot","link":"https://www.dogshop.example/boot","thumbnail":"https://t/2.jpg"}]}`,
		"/api/v1/search": `{"visual_matches":[{"title":"SearchApi boot","link":"https://s.example/boot","source":"S","image":{"link":"https://s.example/boot.jpg","width":800},"price":"€70","extracted_price":70,"currency":"EUR"}]}`,
	}
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Query().Get("api_key") != "k" || r.URL.Query().Get("url") != "https://img/crop.jpg" {
			http.Error(w, "bad", 400)
			return
		}
		w.Write([]byte(responses[r.URL.Path]))
	}))
	defer srv.Close()

	check := func(provider string, want Listing) {
		t.Helper()
		got, err := (&LensProvider{Provider: provider, APIKey: "k", BaseURL: srv.URL}).Search(context.Background(), "https://img/crop.jpg", Detection{})
		if err != nil || len(got) != 1 {
			t.Fatalf("%s: %+v, %v", provider, got, err)
		}
		g := got[0]
		if g.Title != want.Title || g.Merchant != want.Merchant || g.Price != want.Price || g.URL != want.URL {
			t.Errorf("%s: got %+v want %+v", provider, g, want)
		}
		if g.ThumbnailURL == "" {
			t.Errorf("%s: missing thumbnail", provider)
		}
		if want.PriceValue != nil && (g.PriceValue == nil || *g.PriceValue != *want.PriceValue) {
			t.Errorf("%s: price value %v", provider, g.PriceValue)
		}
	}
	f := func(v float64) *float64 { return &v }
	check("serpapi", Listing{Title: "Serp boot", Merchant: "Shop", URL: "https://shop.example/boot", Price: "$89.00", PriceValue: f(89)})
	check("scrapingdog", Listing{Title: "Dog boot", Merchant: "dogshop.example", URL: "https://www.dogshop.example/boot"})
	check("searchapi", Listing{Title: "SearchApi boot", Merchant: "S", URL: "https://s.example/boot", Price: "€70", PriceValue: f(70)})
}

func near(a, b float64) bool { return math.Abs(a-b) < 1e-9 }

func TestSearchLinksUseDetectedText(t *testing.T) {
	got, err := SearchLinks{}.Search(context.Background(), "", Detection{Label: "Denim jacket", Description: "light wash, cropped"})
	if err != nil || len(got) == 0 {
		t.Fatalf("got %v, %v", got, err)
	}
	if !strings.Contains(got[0].URL, "Denim+jacket+light+wash") || got[0].Price != "" {
		t.Errorf("listing = %+v", got[0])
	}
}

type stubSearcher struct {
	name  string
	err   error
	calls *int
}

func (s stubSearcher) Name() string { return s.name }

func (s stubSearcher) Search(context.Context, string, Detection) ([]Listing, error) {
	*s.calls++
	if s.err != nil {
		return nil, s.err
	}
	return []Listing{{Title: s.name}}, nil
}

func TestFallbackSearcherTriesNextProvider(t *testing.T) {
	var calls int
	chain := FallbackSearcher{
		stubSearcher{name: "a", err: errors.New("quota"), calls: &calls},
		stubSearcher{name: "b", calls: &calls},
		stubSearcher{name: "c", calls: &calls},
	}
	got, err := chain.Search(context.Background(), "", Detection{})
	if err != nil || len(got) != 1 || got[0].Title != "b" || calls != 2 {
		t.Fatalf("got %v, %v after %d calls", got, err, calls)
	}
}

func TestLensSearcherSkipsProvidersWithoutKeys(t *testing.T) {
	s := lensSearcher(config.Config{LensProvider: "serpapi", ScrapingdogAPIKey: "x", SearchAPIKey: "y"})
	if s == nil || s.Name() != "searchapi+scrapingdog" {
		t.Fatalf("searcher = %v", s)
	}
	if lensSearcher(config.Config{LensProvider: "serpapi"}) != nil {
		t.Fatal("expected no searcher without keys")
	}
}

// fastRetries keeps the retry tests from waiting out the real backoff.
func fastRetries(t *testing.T) {
	original := retryDelay
	retryDelay = func(int) time.Duration { return time.Millisecond }
	t.Cleanup(func() { retryDelay = original })
}

func TestGeminiRetriesTransientFailures(t *testing.T) {
	fastRetries(t)
	var calls int
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		calls++
		if calls == 1 {
			http.Error(w, `{"error":{"code":503,"message":"overloaded"}}`, http.StatusServiceUnavailable)
			return
		}
		w.Write([]byte(`{"candidates":[{"content":{"parts":[{"text":"[{\"label\":\"Coat\",\"category\":\"outerwear\",\"box_2d\":[100,100,900,900]}]"}]}}]}`))
	}))
	defer srv.Close()

	g := &Gemini{APIKey: "k", Model: "gemini-3.6-flash", BaseURL: srv.URL}
	got, err := g.Detect(t.Context(), []byte("image"), "image/jpeg")
	if err != nil || len(got) != 1 || got[0].Label != "Coat" {
		t.Fatalf("got %+v, %v after %d calls", got, err, calls)
	}
	if calls != 2 {
		t.Errorf("calls = %d, want 2", calls)
	}
}

func TestGeminiRetriesTimeouts(t *testing.T) {
	fastRetries(t)
	var mu sync.Mutex
	var calls int
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		mu.Lock()
		calls++
		first := calls == 1
		mu.Unlock()
		if first {
			// An overloaded model often stops responding instead of failing.
			time.Sleep(300 * time.Millisecond)
		}
		w.Write([]byte(`{"candidates":[{"content":{"parts":[{"text":"[{\"label\":\"Coat\",\"category\":\"outerwear\",\"box_2d\":[100,100,900,900]}]"}]}}]}`))
	}))
	defer srv.Close()

	g := &Gemini{APIKey: "k", Model: "gemini-3.6-flash", BaseURL: srv.URL, HTTP: &http.Client{Timeout: 80 * time.Millisecond}}
	got, err := g.Detect(t.Context(), []byte("image"), "image/jpeg")
	if err != nil || len(got) != 1 {
		t.Fatalf("got %+v, %v", got, err)
	}
}

func TestGeminiFallsBackToAnotherModel(t *testing.T) {
	fastRetries(t)
	var tried []string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		tried = append(tried, r.URL.Path)
		if strings.Contains(r.URL.Path, "busy-model") {
			http.Error(w, `{"error":{"code":503,"message":"overloaded"}}`, http.StatusServiceUnavailable)
			return
		}
		w.Write([]byte(`{"candidates":[{"content":{"parts":[{"text":"[{\"label\":\"Coat\",\"category\":\"outerwear\",\"box_2d\":[100,100,900,900]}]"}]}}]}`))
	}))
	defer srv.Close()

	g := &Gemini{APIKey: "k", Model: "busy-model", Fallbacks: []string{"busy-model", "spare-model"}, BaseURL: srv.URL}
	got, err := g.Detect(t.Context(), []byte("image"), "image/jpeg")
	if err != nil || len(got) != 1 || got[0].Label != "Coat" {
		t.Fatalf("got %+v, %v after %v", got, err, tried)
	}
	// Three attempts on the overloaded model, then one on the spare; the
	// duplicate fallback entry is skipped.
	if len(tried) != 4 || !strings.Contains(tried[3], "spare-model") {
		t.Errorf("tried = %v", tried)
	}
}

func TestGeminiGivesUpOnPermanentFailures(t *testing.T) {
	var calls int
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		calls++
		http.Error(w, `{"error":{"code":404,"message":"model is no longer available"}}`, http.StatusNotFound)
	}))
	defer srv.Close()

	_, err := (&Gemini{APIKey: "k", Model: "gone", BaseURL: srv.URL}).Detect(t.Context(), []byte("image"), "image/jpeg")
	if err == nil || !strings.Contains(err.Error(), "404") {
		t.Fatalf("err = %v", err)
	}
	if calls != 1 {
		t.Errorf("calls = %d, want 1", calls)
	}
}

func TestLocalCropCopy(t *testing.T) {
	p := &Pipeline{
		Detector: fakeDetector{detections: []Detection{{Label: "Coat", Category: "outerwear", Box: Box{X: 0.1, Y: 0.1, Width: 0.6, Height: 0.6}}}},
		Uploader: &fakeUploader{uploads: map[string][]byte{}},
		Crops:    &LocalUploader{Dir: t.TempDir(), URLPrefix: "/media/crops/"},
		Searcher: fakeSearcher{},
	}
	r, err := p.Run(context.Background(), testutil.JPEG(t, 200, 200), "image/jpeg")
	if err != nil {
		t.Fatal(err)
	}
	if !strings.HasPrefix(r.Items[0].CropURL, "/media/crops/item-1-") {
		t.Errorf("crop url = %q, want the local copy", r.Items[0].CropURL)
	}
}

func TestDropExpiredCrops(t *testing.T) {
	fresh := Result{Items: []Item{{CropURL: "https://i.ibb.co/x/item-1.jpg"}, {CropURL: "/media/crops/a.jpg"}}}
	dropExpiredCrops(&fresh, time.Now().Add(-time.Hour))
	if fresh.Items[0].CropURL == "" {
		t.Error("fresh imgbb crop dropped")
	}
	old := fresh
	old.Items = append([]Item(nil), fresh.Items...)
	dropExpiredCrops(&old, time.Now().Add(-30*time.Hour))
	if old.Items[0].CropURL != "" || old.Items[1].CropURL != "/media/crops/a.jpg" {
		t.Errorf("old = %+v", old.Items)
	}
}
