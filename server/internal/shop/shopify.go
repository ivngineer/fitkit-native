package shop

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/netip"
	"net/url"
	"strings"
	"syscall"
	"time"
)

// cacheTTL is how long fetched store data is reused.
const cacheTTL = 6 * time.Hour

const maxBody = 2 << 20

// Cache keeps fetched store data between requests.
type Cache interface {
	CachedShopData(ctx context.Context, key string, maxAge time.Duration) (string, bool, error)
	PutShopData(ctx context.Context, key, body string) error
}

// Product is a Shopify product's buyable variants.
type Product struct {
	Title    string    `json:"title"`
	Options  []Option  `json:"options"`
	Variants []Variant `json:"variants"`
}

type Option struct {
	Name   string   `json:"name"`
	Values []string `json:"values"`
}

type Variant struct {
	ID    int64  `json:"id"`
	Title string `json:"title"`
	// Options holds this variant's value for each of Product.Options.
	Options   []string `json:"options"`
	Available bool     `json:"available"`
	// Price is in the store's minor units (cents).
	Price int64 `json:"price"`
}

// Variant returns the variant with the given ID.
func (p *Product) Variant(id int64) (Variant, bool) {
	for _, v := range p.Variants {
		if v.ID == id {
			return v, true
		}
	}
	return Variant{}, false
}

// MatchSize picks the variant whose options include size, preferring one in
// stock. Sizes compare case-insensitively.
func (p *Product) MatchSize(size string) (Variant, bool) {
	size = strings.TrimSpace(size)
	if size == "" {
		return Variant{}, false
	}
	var found *Variant
	for i, v := range p.Variants {
		for _, o := range v.Options {
			if strings.EqualFold(strings.TrimSpace(o), size) {
				if v.Available {
					return v, true
				}
				if found == nil {
					found = &p.Variants[i]
				}
			}
		}
	}
	if found != nil {
		return *found, true
	}
	return Variant{}, false
}

// NeedsChoice reports whether the buyer has to pick among variants.
func (p *Product) NeedsChoice() bool { return len(p.Variants) > 1 }

// Storefront is what a Shopify store says about itself in /meta.json.
type Storefront struct {
	Currency string   `json:"currency"`
	ShipsTo  []string `json:"shipsTo"`
}

// ShipsTo reports whether the store lists the country; ok is false when the
// store doesn't say.
func (s Storefront) Ships(country string) (ships, ok bool) {
	if len(s.ShipsTo) == 0 {
		return false, false
	}
	for _, c := range s.ShipsTo {
		if strings.EqualFold(c, country) || c == "*" {
			return true, true
		}
	}
	return false, true
}

// ErrUnavailable means the store couldn't be reached; unlike "not a Shopify
// store" it isn't remembered.
var ErrUnavailable = errors.New("store unavailable")

// ShopifyProduct loads a product's variants. It returns nil, nil when the
// listing isn't a Shopify product.
func (s *Service) ShopifyProduct(ctx context.Context, l Listing) (*Product, error) {
	if l.ShopifyHandle == "" {
		return nil, nil
	}
	if s.Demo && isDemoHost(l.Host) {
		return demoProduct(l.ShopifyHandle), nil
	}
	key := "shopify:product:" + l.Host + "/" + l.ShopifyHandle
	body, err := s.cached(ctx, key, "https://"+l.Host+"/products/"+url.PathEscape(l.ShopifyHandle)+".js", func(data []byte) (any, error) {
		return parseShopifyProduct(data)
	})
	if err != nil || body == "" {
		return nil, err
	}
	var p Product
	if err := json.Unmarshal([]byte(body), &p); err != nil {
		return nil, err
	}
	return &p, nil
}

// ShopifyStorefront loads a Shopify store's currency and shipping countries.
func (s *Service) ShopifyStorefront(ctx context.Context, host string) (Storefront, error) {
	if s.Demo && isDemoHost(host) {
		return Storefront{Currency: "USD", ShipsTo: []string{"US", "CA", "GB", "DE", "FR", "PL", "UA"}}, nil
	}
	body, err := s.cached(ctx, "shopify:meta:"+host, "https://"+host+"/meta.json", func(data []byte) (any, error) {
		var raw struct {
			Currency string   `json:"currency"`
			ShipsTo  []string `json:"ships_to_countries"`
		}
		if err := json.Unmarshal(data, &raw); err != nil {
			return nil, err
		}
		return Storefront{Currency: strings.ToUpper(raw.Currency), ShipsTo: raw.ShipsTo}, nil
	})
	if err != nil || body == "" {
		return Storefront{}, err
	}
	var sf Storefront
	err = json.Unmarshal([]byte(body), &sf)
	return sf, err
}

// cached fetches url through the cache. A definite "no" from the store (a
// 4xx or a body that doesn't parse) is cached as "" so it isn't refetched.
func (s *Service) cached(ctx context.Context, key, target string, parse func([]byte) (any, error)) (string, error) {
	if s.Cache != nil {
		if body, ok, err := s.Cache.CachedShopData(ctx, key, cacheTTL); err != nil {
			return "", err
		} else if ok {
			return body, nil
		}
	}
	data, err := s.fetch(ctx, target)
	var body string
	switch {
	case errors.Is(err, errNotFound):
	case err != nil:
		return "", err
	default:
		if v, perr := parse(data); perr == nil {
			encoded, _ := json.Marshal(v)
			body = string(encoded)
		}
	}
	if s.Cache != nil {
		if err := s.Cache.PutShopData(ctx, key, body); err != nil {
			return "", err
		}
	}
	return body, nil
}

var errNotFound = errors.New("not found")

func (s *Service) fetch(ctx context.Context, target string) ([]byte, error) {
	ctx, cancel := context.WithTimeout(ctx, 8*time.Second)
	defer cancel()
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, target, nil)
	if err != nil {
		return nil, err
	}
	req.Header.Set("Accept", "application/json")
	req.Header.Set("User-Agent", "Mozilla/5.0 (compatible; Fitkit/1.0)")
	client := s.Client
	if client == nil {
		client = safeClient
	}
	resp, err := client.Do(req)
	if err != nil {
		return nil, fmt.Errorf("%w: %v", ErrUnavailable, err)
	}
	defer resp.Body.Close()
	switch {
	case resp.StatusCode >= 400 && resp.StatusCode < 500 && resp.StatusCode != http.StatusTooManyRequests:
		return nil, errNotFound
	case resp.StatusCode != http.StatusOK:
		return nil, fmt.Errorf("%w: HTTP %d", ErrUnavailable, resp.StatusCode)
	}
	data, err := io.ReadAll(io.LimitReader(resp.Body, maxBody+1))
	if err != nil {
		return nil, fmt.Errorf("%w: %v", ErrUnavailable, err)
	}
	if len(data) > maxBody {
		return nil, errNotFound
	}
	return data, nil
}

// safeClient only talks HTTPS to public addresses. Listing URLs come from
// search results, so they must never reach the server's own network.
var safeClient = &http.Client{
	Timeout: 10 * time.Second,
	Transport: &http.Transport{
		Proxy: nil,
		DialContext: (&net.Dialer{
			Timeout: 5 * time.Second,
			Control: func(network, address string, _ syscall.RawConn) error {
				addr, err := netip.ParseAddrPort(address)
				if err != nil || !publicAddr(addr.Addr()) || addr.Port() != 443 {
					return fmt.Errorf("refusing to connect to %s", address)
				}
				return nil
			},
		}).DialContext,
		TLSHandshakeTimeout:   5 * time.Second,
		ResponseHeaderTimeout: 8 * time.Second,
		MaxIdleConnsPerHost:   2,
	},
	CheckRedirect: func(req *http.Request, via []*http.Request) error {
		if len(via) >= 3 || req.URL.Scheme != "https" {
			return errors.New("redirect refused")
		}
		return nil
	},
}

func publicAddr(a netip.Addr) bool {
	a = a.Unmap()
	if !a.IsValid() || a.IsLoopback() || a.IsPrivate() || a.IsLinkLocalUnicast() || a.IsLinkLocalMulticast() ||
		a.IsMulticast() || a.IsUnspecified() || a.IsInterfaceLocalMulticast() {
		return false
	}
	for _, p := range blockedPrefixes {
		if p.Contains(a) {
			return false
		}
	}
	return true
}

var blockedPrefixes = []netip.Prefix{
	netip.MustParsePrefix("100.64.0.0/10"), // carrier-grade NAT
	netip.MustParsePrefix("192.0.0.0/24"),
	netip.MustParsePrefix("198.18.0.0/15"),
	netip.MustParsePrefix("240.0.0.0/4"),
	netip.MustParsePrefix("64:ff9b::/96"), // NAT64 can reach private IPv4
}

// parseShopifyProduct reads the public /products/<handle>.js JSON. Options
// come as objects on current stores and as bare names on older ones.
func parseShopifyProduct(data []byte) (Product, error) {
	var raw struct {
		Title    string            `json:"title"`
		Options  []json.RawMessage `json:"options"`
		Variants []struct {
			ID        int64   `json:"id"`
			Title     string  `json:"title"`
			Option1   *string `json:"option1"`
			Option2   *string `json:"option2"`
			Option3   *string `json:"option3"`
			Available bool    `json:"available"`
			Price     int64   `json:"price"`
		} `json:"variants"`
	}
	if err := json.Unmarshal(data, &raw); err != nil {
		return Product{}, err
	}
	if len(raw.Variants) == 0 {
		return Product{}, errors.New("product has no variants")
	}
	p := Product{Title: raw.Title}
	for _, o := range raw.Options {
		var named struct {
			Name   string   `json:"name"`
			Values []string `json:"values"`
		}
		if json.Unmarshal(o, &named) != nil {
			if json.Unmarshal(o, &named.Name) != nil {
				continue
			}
		}
		p.Options = append(p.Options, Option{Name: named.Name, Values: named.Values})
	}
	for _, v := range raw.Variants {
		if v.ID <= 0 {
			continue
		}
		var opts []string
		for i, o := range []*string{v.Option1, v.Option2, v.Option3} {
			if o == nil || i >= max(len(p.Options), 1) {
				continue
			}
			opts = append(opts, *o)
		}
		p.Variants = append(p.Variants, Variant{ID: v.ID, Title: v.Title, Options: opts, Available: v.Available, Price: v.Price})
	}
	// Older payloads list option names without values; fill them in.
	for i := range p.Options {
		if len(p.Options[i].Values) > 0 {
			continue
		}
		seen := map[string]bool{}
		for _, v := range p.Variants {
			if i < len(v.Options) && !seen[v.Options[i]] {
				seen[v.Options[i]] = true
				p.Options[i].Values = append(p.Options[i].Values, v.Options[i])
			}
		}
	}
	// "Default Title" is Shopify's placeholder for products without options.
	if len(p.Options) == 1 && len(p.Variants) == 1 && p.Options[0].Name == "Title" {
		p.Options = nil
		p.Variants[0].Options = nil
	}
	return p, nil
}

// Demo mode serves products from *.fitkit.example without any network.
const demoDomain = ".fitkit.example"

func isDemoHost(host string) bool { return strings.HasSuffix(stripPort(host), demoDomain) }

func demoProduct(handle string) *Product {
	p := &Product{Title: strings.ReplaceAll(handle, "-", " "), Options: []Option{{Name: "Size", Values: []string{"XS", "S", "M", "L", "XL"}}}}
	for i, size := range p.Options[0].Values {
		p.Variants = append(p.Variants, Variant{
			ID: int64(40000000 + len(handle)*100 + i), Title: size, Options: []string{size},
			Available: size != "XS", Price: 3990,
		})
	}
	return p
}
