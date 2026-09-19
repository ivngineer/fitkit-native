// Package analyze turns a pin image into a shoppable breakdown:
// detect items, crop each, host the crop, reverse image search it.
package analyze

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"image"
	"image/draw"
	_ "image/gif"
	"image/jpeg"
	_ "image/png"
	"net/url"
	"sort"
	"strings"
	"sync"
	"time"

	_ "golang.org/x/image/webp"
)

// Box is a bounding box in normalized [0, 1] image coordinates.
type Box struct {
	X      float64 `json:"x"`
	Y      float64 `json:"y"`
	Width  float64 `json:"width"`
	Height float64 `json:"height"`
}

type Detection struct {
	Label       string
	Category    string
	Description string
	Box         Box
}

type Listing struct {
	Title        string   `json:"title"`
	Merchant     string   `json:"merchant"`
	URL          string   `json:"url"`
	ThumbnailURL string   `json:"thumbnailUrl,omitempty"`
	Price        string   `json:"price,omitempty"`
	PriceValue   *float64 `json:"priceValue,omitempty"`
	Currency     string   `json:"currency,omitempty"`
	InStock      *bool    `json:"inStock,omitempty"`
}

type Item struct {
	ID          string    `json:"id"`
	Label       string    `json:"label"`
	Category    string    `json:"category"`
	Description string    `json:"description,omitempty"`
	Box         Box       `json:"box"`
	CropURL     string    `json:"cropUrl,omitempty"`
	Listings    []Listing `json:"listings"`
	Error       string    `json:"error,omitempty"`
}

type Result struct {
	Items       []Item    `json:"items"`
	Provider    string    `json:"provider"`
	Demo        bool      `json:"demo"`
	GeneratedAt time.Time `json:"generatedAt"`
}

type Detector interface {
	Detect(ctx context.Context, img []byte, mimeType string) ([]Detection, error)
}

type Uploader interface {
	Upload(ctx context.Context, jpegData []byte, name string) (url string, err error)
}

type VisualSearcher interface {
	Name() string
	// Search finds listings for a hosted crop of the detected item.
	Search(ctx context.Context, imageURL string, item Detection) ([]Listing, error)
}

var ErrNoItems = errors.New("no shoppable items were found in this pin")

const (
	maxItems           = 8
	maxListingsPerItem = 12
	cropPadding        = 0.06
	minCropSidePixels  = 48
)

type Pipeline struct {
	Detector Detector
	// Uploader hosts crops for visual search.
	Uploader Uploader
	// Crops keeps a lasting copy of each crop for the app to show, when the
	// search host deletes its copies after a while. Nil shows the
	// Uploader's copy.
	Crops    Uploader
	Searcher VisualSearcher
	Demo     bool
}

func (p *Pipeline) Run(ctx context.Context, img []byte, mimeType string) (Result, error) {
	detections, err := p.Detector.Detect(ctx, img, mimeType)
	if err != nil {
		return Result{}, fmt.Errorf("detect items: %w", err)
	}
	detections = dedupeDetections(detections)
	if len(detections) == 0 {
		return Result{}, ErrNoItems
	}
	if len(detections) > maxItems {
		detections = detections[:maxItems]
	}

	src, _, err := image.Decode(bytes.NewReader(img))
	if err != nil {
		return Result{}, fmt.Errorf("decode image: %w", err)
	}

	items := make([]Item, len(detections))
	var wg sync.WaitGroup
	for i, d := range detections {
		wg.Go(func() {
			items[i] = p.processItem(ctx, src, i, d)
		})
	}
	wg.Wait()

	succeeded := 0
	for _, it := range items {
		if it.Error == "" {
			succeeded++
		}
	}
	if succeeded == 0 {
		return Result{}, fmt.Errorf("every item failed: %s", items[0].Error)
	}
	return Result{Items: items, Provider: p.Searcher.Name(), Demo: p.Demo, GeneratedAt: time.Now().UTC()}, nil
}

// dedupeDetections keeps one detection per kind of garment, so a look with a
// jacket and jeans lists one jacket and one pair of jeans even when the model
// boxes the same piece twice. The biggest box wins, since it's the clearest
// view of the item. Catch-all categories key on the label instead, so a belt
// and a watch don't collapse into one accessory.
func dedupeDetections(detections []Detection) []Detection {
	broadCategories := map[string]bool{"accessory": true, "jewelry": true, "other": true}
	best := map[string]int{}
	out := make([]Detection, 0, len(detections))
	for _, d := range detections {
		key := d.Category
		if key == "" || broadCategories[key] {
			key += "|" + strings.ToLower(strings.TrimSpace(d.Label))
		}
		index, seen := best[key]
		if !seen {
			best[key] = len(out)
			out = append(out, d)
			continue
		}
		if area(d.Box) > area(out[index].Box) {
			out[index] = d
		}
	}
	return out
}

func area(b Box) float64 { return b.Width * b.Height }

func (p *Pipeline) processItem(ctx context.Context, src image.Image, index int, d Detection) Item {
	item := Item{
		ID: fmt.Sprintf("item-%d", index+1), Label: d.Label, Category: d.Category,
		Description: d.Description, Box: d.Box, Listings: []Listing{},
	}
	crop, err := Crop(src, d.Box, cropPadding)
	if err != nil {
		item.Error = "Couldn't crop this item."
		return item
	}
	url, err := p.Uploader.Upload(ctx, crop, item.ID+".jpg")
	if err != nil {
		item.Error = "Couldn't upload this item for search."
		return item
	}
	item.CropURL = url
	if p.Crops != nil {
		if local, err := p.Crops.Upload(ctx, crop, item.ID+".jpg"); err == nil {
			item.CropURL = local
		}
	}
	listings, err := p.Searcher.Search(ctx, url, d)
	if err != nil {
		item.Error = "Visual search failed for this item."
		return item
	}
	item.Listings = rankListings(listings)
	return item
}

// excludedMerchants never sell anything: they're where the pin came from.
var excludedMerchants = []string{"pinterest."}

// fromExcludedMerchant reports whether a listing links somewhere that can't be
// shopped, so the next match takes its place.
func fromExcludedMerchant(l Listing) bool {
	u, err := url.Parse(l.URL)
	if err != nil {
		return false
	}
	host := strings.ToLower(u.Hostname())
	for _, bad := range excludedMerchants {
		if host == strings.TrimSuffix(bad, ".") || strings.Contains(host, bad) {
			return true
		}
	}
	return false
}

// rankListings puts priced listings first and trims the list.
func rankListings(listings []Listing) []Listing {
	out := make([]Listing, 0, len(listings))
	seen := map[string]bool{}
	for _, l := range listings {
		if l.URL == "" || l.Title == "" || seen[l.URL] || fromExcludedMerchant(l) {
			continue
		}
		seen[l.URL] = true
		out = append(out, l)
	}
	sort.SliceStable(out, func(i, j int) bool {
		return out[i].Price != "" && out[j].Price == ""
	})
	if len(out) > maxListingsPerItem {
		out = out[:maxListingsPerItem]
	}
	return out
}

// Crop cuts a padded box out of src and encodes it as JPEG.
func Crop(src image.Image, box Box, padding float64) ([]byte, error) {
	b := src.Bounds()
	w, h := float64(b.Dx()), float64(b.Dy())
	x0 := clamp(box.X-box.Width*padding, 0, 1) * w
	y0 := clamp(box.Y-box.Height*padding, 0, 1) * h
	x1 := clamp(box.X+box.Width*(1+padding), 0, 1) * w
	y1 := clamp(box.Y+box.Height*(1+padding), 0, 1) * h
	rect := image.Rect(b.Min.X+int(x0), b.Min.Y+int(y0), b.Min.X+int(x1), b.Min.Y+int(y1))
	if rect.Dx() < minCropSidePixels || rect.Dy() < minCropSidePixels {
		return nil, errors.New("crop too small")
	}
	dst := image.NewRGBA(image.Rect(0, 0, rect.Dx(), rect.Dy()))
	draw.Draw(dst, dst.Bounds(), src, rect.Min, draw.Src)
	var buf bytes.Buffer
	if err := jpeg.Encode(&buf, dst, &jpeg.Options{Quality: 90}); err != nil {
		return nil, err
	}
	return buf.Bytes(), nil
}

func clamp(v, lo, hi float64) float64 {
	return max(lo, min(hi, v))
}
