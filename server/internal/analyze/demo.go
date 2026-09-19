package analyze

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"net/url"
	"os"
	"path/filepath"
	"strings"
)

// DemoDetector returns fixed regions so the pipeline can run without Gemini.
type DemoDetector struct{}

func (DemoDetector) Detect(context.Context, []byte, string) ([]Detection, error) {
	return []Detection{
		{Label: "Top", Category: "top", Description: "Demo detection of the upper garment", Box: Box{X: 0.2, Y: 0.15, Width: 0.6, Height: 0.35}},
		{Label: "Bottoms", Category: "bottom", Description: "Demo detection of the lower garment", Box: Box{X: 0.22, Y: 0.48, Width: 0.56, Height: 0.35}},
		{Label: "Shoes", Category: "shoes", Description: "Demo detection of footwear", Box: Box{X: 0.25, Y: 0.82, Width: 0.5, Height: 0.16}},
	}, nil
}

// DemoSearcher links to real shopping searches for the item label.
type DemoSearcher struct{}

func (DemoSearcher) Name() string { return "demo" }

func (DemoSearcher) Search(_ context.Context, imageURL string, _ Detection) ([]Listing, error) {
	label := "outfit"
	switch {
	case strings.Contains(imageURL, "item-1"):
		label = "top"
	case strings.Contains(imageURL, "item-2"):
		label = "trousers"
	case strings.Contains(imageURL, "item-3"):
		label = "sneakers"
	}
	q := url.QueryEscape(label)
	price := func(v float64, s string) (*float64, string) { return &v, s }
	p1, s1 := price(39.90, "$39.90")
	p2, s2 := price(64.00, "$64.00")
	// A made-up Shopify store (served without network in demo mode), an
	// Amazon listing and a plain store search, one per checkout method.
	return []Listing{
		{Title: "Demo " + label, Merchant: "Fitkit Demo Shop", URL: "https://demo-shop.fitkit.example/products/demo-" + label, PriceValue: p1, Price: s1, Currency: "USD"},
		{Title: "Demo " + label + " on Amazon", Merchant: "Amazon", URL: "https://www.amazon.com/dp/B0DEMO000" + demoASINDigit(label) + "?psc=1", PriceValue: p2, Price: s2, Currency: "USD"},
		{Title: "Search “" + label + "” on Zara", Merchant: "Zara", URL: "https://www.zara.com/us/en/search?searchTerm=" + q},
	}, nil
}

func demoASINDigit(label string) string {
	return string(rune('0' + len(label)%10))
}

// SearchLinks builds store search links from the detected item's text. It
// backs Gemini-only setups, before image hosting and visual search are
// configured, so listings carry no prices.
type SearchLinks struct{}

func (SearchLinks) Name() string { return "search-links" }

func (SearchLinks) Search(_ context.Context, _ string, item Detection) ([]Listing, error) {
	query := strings.TrimSpace(item.Label)
	if item.Description != "" {
		query += " " + item.Description
	}
	q := url.QueryEscape(query)
	short := url.QueryEscape(item.Label)
	return []Listing{
		{Title: "Shop “" + item.Label + "” on Google Shopping", Merchant: "Google Shopping", URL: "https://www.google.com/search?udm=28&q=" + q},
		{Title: "Search “" + item.Label + "” on Amazon", Merchant: "Amazon", URL: "https://www.amazon.com/s?k=" + short},
		{Title: "Search “" + item.Label + "” on Nordstrom", Merchant: "Nordstrom", URL: "https://www.nordstrom.com/sr?keyword=" + short},
	}, nil
}

// LocalUploader stores crops next to pin media instead of an image host.
type LocalUploader struct {
	Dir       string
	URLPrefix string
}

func (u *LocalUploader) Upload(_ context.Context, data []byte, name string) (string, error) {
	if err := os.MkdirAll(u.Dir, 0o755); err != nil {
		return "", err
	}
	file := NewFileName(name)
	if err := os.WriteFile(filepath.Join(u.Dir, file), data, 0o644); err != nil {
		return "", err
	}
	return u.URLPrefix + file, nil
}

func NewFileName(name string) string {
	return strings.TrimSuffix(name, ".jpg") + "-" + randomSuffix() + ".jpg"
}

func randomSuffix() string {
	var b [6]byte
	_, _ = rand.Read(b[:])
	return hex.EncodeToString(b[:])
}
