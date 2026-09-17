package analyze

import (
	"bytes"
	"context"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"mime/multipart"
	"net"
	"net/http"
	"net/url"
	"slices"
	"strconv"
	"strings"
	"time"
)

// Detection sends the pin inline, so give the request room on a slow link
// while staying well inside the pipeline's own timeout.
var defaultHTTP = &http.Client{Timeout: 90 * time.Second}

type httpError struct {
	Status int
	Host   string
	Body   string
}

func (e *httpError) Error() string {
	return fmt.Sprintf("%s: HTTP %d: %s", e.Host, e.Status, e.Body)
}

// retryable reports whether a provider's response is worth another try:
// rate limits and transient server-side failures.
func (e *httpError) retryable() bool {
	return e.Status == http.StatusTooManyRequests || e.Status >= 500
}

// retryable reports whether a failed attempt is worth repeating. Besides rate
// limits and 5xx, an overloaded model often just stops responding, which
// surfaces as a client timeout rather than a status code.
func retryable(err error) bool {
	var httpErr *httpError
	if errors.As(err, &httpErr) {
		return httpErr.retryable()
	}
	var netErr net.Error
	return errors.As(err, &netErr) && netErr.Timeout()
}

// retryDelay is the wait before attempt n (1-based); tests shrink it.
var retryDelay = func(attempt int) time.Duration { return time.Duration(attempt) * 2 * time.Second }

// doJSONRetry retries rate limits and 5xx responses with a growing delay.
// build makes a fresh request each attempt so the body can be replayed.
func doJSONRetry(ctx context.Context, client *http.Client, build func() (*http.Request, error), out any) error {
	const attempts = 3
	var err error
	for attempt := range attempts {
		if attempt > 0 {
			select {
			case <-ctx.Done():
				return ctx.Err()
			case <-time.After(retryDelay(attempt)):
			}
		}
		var req *http.Request
		if req, err = build(); err != nil {
			return err
		}
		err = doJSON(client, req, out)
		if err == nil || ctx.Err() != nil || !retryable(err) {
			return err
		}
	}
	return err
}

func doJSON(client *http.Client, req *http.Request, out any) error {
	if client == nil {
		client = defaultHTTP
	}
	resp, err := client.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	body, err := io.ReadAll(io.LimitReader(resp.Body, 32<<20))
	if err != nil {
		return err
	}
	if resp.StatusCode/100 != 2 {
		snippet := string(body)
		if len(snippet) > 300 {
			snippet = snippet[:300]
		}
		return &httpError{Status: resp.StatusCode, Host: req.URL.Host, Body: snippet}
	}
	return json.Unmarshal(body, out)
}

// ---- Gemini object detection ----

type Gemini struct {
	APIKey string
	Model  string
	// Fallbacks are tried in order when Model is overloaded or unavailable,
	// which free-tier keys hit regularly.
	Fallbacks []string
	BaseURL   string // defaults to the public Generative Language API
	HTTP      *http.Client
}

// modelChain lists the models to try, primary first and duplicates dropped.
func (g *Gemini) modelChain() []string {
	out := make([]string, 0, len(g.Fallbacks)+1)
	for _, m := range append([]string{g.Model}, g.Fallbacks...) {
		if m = strings.TrimSpace(m); m != "" && !slices.Contains(out, m) {
			out = append(out, m)
		}
	}
	if len(out) == 0 {
		out = []string{"gemini-3.6-flash"}
	}
	return out
}

const detectPrompt = `You are a fashion product detector. Find every distinct wearable or shoppable item in this image: garments, shoes, bags, jewelry, hats, eyewear, belts, watches and similar accessories.
Ignore people, faces, backgrounds, and items that are mostly hidden or too small to identify.
For each item return:
- "label": a short shopper-friendly name such as "Cropped denim jacket"
- "category": one of top, outerwear, bottom, dress, shoes, bag, accessory, jewelry, eyewear, hat, other
- "description": color, material and style details useful for finding it in a store
- "box_2d": [ymin, xmin, ymax, xmax] normalized to 0-1000
Return at most 8 items, largest and most prominent first.`

func (g *Gemini) Detect(ctx context.Context, img []byte, mimeType string) ([]Detection, error) {
	base := g.BaseURL
	if base == "" {
		base = "https://generativelanguage.googleapis.com"
	}
	body := map[string]any{
		"contents": []any{map[string]any{
			"parts": []any{
				map[string]any{"inline_data": map[string]any{"mime_type": mimeType, "data": base64.StdEncoding.EncodeToString(img)}},
				map[string]any{"text": detectPrompt},
			},
		}},
		"generationConfig": map[string]any{
			"temperature":      0.2,
			"responseMimeType": "application/json",
			"responseSchema": map[string]any{
				"type": "ARRAY",
				"items": map[string]any{
					"type": "OBJECT",
					"properties": map[string]any{
						"label":       map[string]any{"type": "STRING"},
						"category":    map[string]any{"type": "STRING"},
						"description": map[string]any{"type": "STRING"},
						"box_2d":      map[string]any{"type": "ARRAY", "items": map[string]any{"type": "INTEGER"}},
					},
					"required": []string{"label", "category", "box_2d"},
				},
			},
		},
	}
	payload, _ := json.Marshal(body)

	var errs []error
	for _, model := range g.modelChain() {
		endpoint := fmt.Sprintf("%s/v1beta/models/%s:generateContent", strings.TrimRight(base, "/"), url.PathEscape(model))
		build := func() (*http.Request, error) {
			req, err := http.NewRequestWithContext(ctx, http.MethodPost, endpoint, bytes.NewReader(payload))
			if err != nil {
				return nil, err
			}
			req.Header.Set("Content-Type", "application/json")
			req.Header.Set("x-goog-api-key", g.APIKey)
			return req, nil
		}

		var resp struct {
			Candidates []struct {
				Content struct {
					Parts []struct {
						Text string `json:"text"`
					} `json:"parts"`
				} `json:"content"`
			} `json:"candidates"`
		}
		err := doJSONRetry(ctx, g.HTTP, build, &resp)
		if err == nil {
			if len(resp.Candidates) == 0 || len(resp.Candidates[0].Content.Parts) == 0 {
				return nil, errors.New("gemini returned no candidates")
			}
			return parseGeminiDetections(resp.Candidates[0].Content.Parts[0].Text)
		}
		errs = append(errs, fmt.Errorf("%s: %w", model, err))
		if ctx.Err() != nil || !modelUnavailable(err) {
			break
		}
	}
	return nil, errors.Join(errs...)
}

// modelUnavailable reports whether another model is worth trying: the one we
// asked for is overloaded, rate limited, or gone from this key's catalogue.
func modelUnavailable(err error) bool {
	var httpErr *httpError
	if errors.As(err, &httpErr) {
		return httpErr.retryable() || httpErr.Status == http.StatusNotFound || httpErr.Status == http.StatusForbidden
	}
	return retryable(err)
}

func parseGeminiDetections(text string) ([]Detection, error) {
	text = strings.TrimSpace(text)
	text = strings.TrimPrefix(text, "```json")
	text = strings.TrimSuffix(strings.TrimPrefix(text, "```"), "```")
	var raw []struct {
		Label       string    `json:"label"`
		Category    string    `json:"category"`
		Description string    `json:"description"`
		Box         []float64 `json:"box_2d"`
	}
	if err := json.Unmarshal([]byte(strings.TrimSpace(text)), &raw); err != nil {
		return nil, fmt.Errorf("parse gemini detections: %w", err)
	}
	out := make([]Detection, 0, len(raw))
	for _, r := range raw {
		if len(r.Box) != 4 || strings.TrimSpace(r.Label) == "" {
			continue
		}
		ymin, xmin, ymax, xmax := r.Box[0]/1000, r.Box[1]/1000, r.Box[2]/1000, r.Box[3]/1000
		ymin, ymax = clamp(min(ymin, ymax), 0, 1), clamp(max(ymin, ymax), 0, 1)
		xmin, xmax = clamp(min(xmin, xmax), 0, 1), clamp(max(xmin, xmax), 0, 1)
		if xmax-xmin < 0.02 || ymax-ymin < 0.02 {
			continue
		}
		category := strings.ToLower(strings.TrimSpace(r.Category))
		if category == "" {
			category = "other"
		}
		out = append(out, Detection{
			Label: strings.TrimSpace(r.Label), Category: category, Description: strings.TrimSpace(r.Description),
			Box: Box{X: xmin, Y: ymin, Width: xmax - xmin, Height: ymax - ymin},
		})
	}
	return out, nil
}

// ---- imgbb hosting ----

type Imgbb struct {
	APIKey  string
	BaseURL string
	// Expiration in seconds; crops only need to live long enough to be searched.
	Expiration int
	HTTP       *http.Client
}

func (i *Imgbb) Upload(ctx context.Context, data []byte, name string) (string, error) {
	base := i.BaseURL
	if base == "" {
		base = "https://api.imgbb.com"
	}
	var buf bytes.Buffer
	w := multipart.NewWriter(&buf)
	_ = w.WriteField("image", base64.StdEncoding.EncodeToString(data))
	_ = w.WriteField("name", strings.TrimSuffix(name, ".jpg"))
	w.Close()

	q := url.Values{"key": {i.APIKey}}
	if i.Expiration > 0 {
		q.Set("expiration", strconv.Itoa(i.Expiration))
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, strings.TrimRight(base, "/")+"/1/upload?"+q.Encode(), &buf)
	if err != nil {
		return "", err
	}
	req.Header.Set("Content-Type", w.FormDataContentType())
	var resp struct {
		Success bool `json:"success"`
		Data    struct {
			URL string `json:"url"`
		} `json:"data"`
	}
	if err := doJSON(i.HTTP, req, &resp); err != nil {
		return "", err
	}
	if !resp.Success || resp.Data.URL == "" {
		return "", errors.New("imgbb upload failed")
	}
	return resp.Data.URL, nil
}

// ---- Google Lens style visual search ----

type LensProvider struct {
	Provider string // serpapi, scrapingdog or searchapi
	APIKey   string
	BaseURL  string
	HTTP     *http.Client
}

func (l *LensProvider) Name() string { return l.Provider }

func (l *LensProvider) Search(ctx context.Context, imageURL string, _ Detection) ([]Listing, error) {
	var endpoint string
	q := url.Values{"url": {imageURL}}
	switch l.Provider {
	case "serpapi":
		endpoint = orDefault(l.BaseURL, "https://serpapi.com") + "/search.json"
		q.Set("engine", "google_lens")
		q.Set("api_key", l.APIKey)
	case "scrapingdog":
		endpoint = orDefault(l.BaseURL, "https://api.scrapingdog.com") + "/google_lens"
		q.Set("api_key", l.APIKey)
	case "searchapi":
		endpoint = orDefault(l.BaseURL, "https://www.searchapi.io") + "/api/v1/search"
		q.Set("engine", "google_lens")
		q.Set("api_key", l.APIKey)
	default:
		return nil, fmt.Errorf("unknown lens provider %q", l.Provider)
	}
	build := func() (*http.Request, error) {
		return http.NewRequestWithContext(ctx, http.MethodGet, endpoint+"?"+q.Encode(), nil)
	}
	var raw map[string]json.RawMessage
	if err := doJSONRetry(ctx, l.HTTP, build, &raw); err != nil {
		return nil, err
	}
	return parseLensResults(raw), nil
}

// FallbackSearcher tries each searcher in order until one succeeds.
type FallbackSearcher []VisualSearcher

func (f FallbackSearcher) Name() string {
	names := make([]string, len(f))
	for i, s := range f {
		names[i] = s.Name()
	}
	return strings.Join(names, "+")
}

func (f FallbackSearcher) Search(ctx context.Context, imageURL string, item Detection) ([]Listing, error) {
	var errs []error
	for _, s := range f {
		listings, err := s.Search(ctx, imageURL, item)
		if err == nil {
			return listings, nil
		}
		if ctx.Err() != nil {
			return nil, ctx.Err()
		}
		errs = append(errs, fmt.Errorf("%s: %w", s.Name(), err))
	}
	return nil, errors.Join(errs...)
}

func orDefault(v, fallback string) string {
	if v == "" {
		return fallback
	}
	return strings.TrimRight(v, "/")
}

// parseLensResults normalizes the result shapes of SerpApi ("visual_matches"),
// SearchApi.io ("visual_matches") and Scrapingdog ("lens_results").
func parseLensResults(raw map[string]json.RawMessage) []Listing {
	type rawMatch struct {
		Title     string          `json:"title"`
		Link      string          `json:"link"`
		URL       string          `json:"url"`
		Source    string          `json:"source"`
		Domain    string          `json:"domain"`
		Thumbnail string          `json:"thumbnail"`
		Image     json.RawMessage `json:"image"`
		Price     json.RawMessage `json:"price"`
		Extracted *float64        `json:"extracted_price"`
		Currency  string          `json:"currency"`
		InStock   *bool           `json:"in_stock"`
	}
	var matches []rawMatch
	for _, key := range []string{"visual_matches", "lens_results", "shopping_results", "products"} {
		if data, ok := raw[key]; ok {
			var batch []rawMatch
			if json.Unmarshal(data, &batch) == nil {
				matches = append(matches, batch...)
			}
		}
	}

	listings := make([]Listing, 0, len(matches))
	for _, m := range matches {
		l := Listing{
			Title:        strings.TrimSpace(m.Title),
			URL:          firstNonEmpty(m.Link, m.URL),
			Merchant:     firstNonEmpty(m.Source, m.Domain),
			ThumbnailURL: firstNonEmpty(m.Thumbnail, imageLink(m.Image)),
			Currency:     m.Currency,
			InStock:      m.InStock,
			PriceValue:   m.Extracted,
		}
		if l.Merchant == "" {
			if u, err := url.Parse(l.URL); err == nil {
				l.Merchant = strings.TrimPrefix(u.Hostname(), "www.")
			}
		}
		parsePrice(m.Price, &l)
		listings = append(listings, l)
	}
	return listings
}

// imageLink reads an image given as a URL string or as {"link": "..."}.
func imageLink(data json.RawMessage) string {
	var s string
	if json.Unmarshal(data, &s) == nil {
		return s
	}
	var obj struct {
		Link string `json:"link"`
	}
	json.Unmarshal(data, &obj)
	return obj.Link
}

// parsePrice handles price as a string ("$49.99") or an object
// ({"value": "$49.99", "extracted_value": 49.99, "currency": "$"}).
func parsePrice(data json.RawMessage, l *Listing) {
	if len(data) == 0 || string(data) == "null" {
		return
	}
	var s string
	if json.Unmarshal(data, &s) == nil {
		l.Price = strings.TrimSpace(s)
		return
	}
	var obj struct {
		Value          string   `json:"value"`
		ExtractedValue *float64 `json:"extracted_value"`
		Currency       string   `json:"currency"`
	}
	if json.Unmarshal(data, &obj) == nil {
		l.Price = strings.TrimSpace(obj.Value)
		if obj.ExtractedValue != nil {
			l.PriceValue = obj.ExtractedValue
		}
		if obj.Currency != "" {
			l.Currency = obj.Currency
		}
	}
}

func firstNonEmpty(values ...string) string {
	for _, v := range values {
		if v = strings.TrimSpace(v); v != "" {
			return v
		}
	}
	return ""
}
