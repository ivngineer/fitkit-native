package shop

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"net/netip"
	"net/url"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"fitkit/server/internal/analyze"
	"fitkit/server/internal/store"
)

func TestClassify(t *testing.T) {
	cases := []struct {
		url, merchant string
		method        Method
		asin, handle  string
		variation     bool
	}{
		{"https://www.everlane.com/products/womens-organic-cotton-box-cut-tee?variant=1#reviews", "everlane.com", MethodProductPage, "", "womens-organic-cotton-box-cut-tee", false},
		{"https://shop.brand.co/en-us/collections/new/products/linen-shirt", "brand.co", MethodProductPage, "", "linen-shirt", false},
		{"https://www.amazon.com/Levis-Womens-Jeans/dp/B07K2Z5XYZ/ref=sr_1_1?psc=1", "amazon.com", MethodAmazon, "B07K2Z5XYZ", "", true},
		{"https://www.amazon.de/gp/product/B08ABCDEF1", "amazon.de", MethodAmazon, "B08ABCDEF1", "", false},
		{"https://amazon.co.uk/dp/B08ABCDEF2/", "amazon.co.uk", MethodAmazon, "B08ABCDEF2", "", false},
		{"https://www.amazon.com/s?k=jeans", "amazon.com", MethodProductPage, "", "", false},
		{"https://www.google.com/url?q=https://www.aliexpress.com/item/1005.html&sa=U", "aliexpress.com", MethodAffiliate, "", "", false},
		{"https://us.shein.com/Dress-p-123.html", "shein.com", MethodAffiliate, "", "", false},
		{"https://www2.hm.com/en_us/productpage.1.html", "www2.hm.com", MethodAffiliate, "", "", false},
		{"https://www.nordstrom.com/s/coat/123", "nordstrom.com", MethodProductPage, "", "", false},
	}
	for _, c := range cases {
		l, err := Classify(c.url)
		if err != nil {
			t.Fatalf("%s: %v", c.url, err)
		}
		if l.Merchant != c.merchant || l.Method != c.method || l.ASIN != c.asin || l.ShopifyHandle != c.handle || l.Variation != c.variation {
			t.Errorf("%s: got %+v", c.url, l)
		}
		if strings.Contains(l.URL, "#") {
			t.Errorf("%s: fragment kept: %s", c.url, l.URL)
		}
	}
	for _, bad := range []string{"javascript:alert(1)", "ftp://x.com/a", "https://user:pw@shop.com/products/a", "not a url", ""} {
		if _, err := Classify(bad); err == nil {
			t.Errorf("%q classified", bad)
		}
	}
}

func TestLinks(t *testing.T) {
	aff := Affiliate{AmazonTags: map[string]string{"com": "fitkit-20"}, AliExpressKey: "ak", SkimlinksID: "123X"}
	cart, ok := aff.AmazonCartURL("com", []AmazonCartItem{{"B07K2Z5XYZ", 1}, {"B08ABCDEF1", 2}})
	want := "https://www.amazon.com/gp/aws/cart/add.html?ASIN.1=B07K2Z5XYZ&ASIN.2=B08ABCDEF1&AssociateTag=fitkit-20&Quantity.1=1&Quantity.2=2"
	if !ok || cart != want {
		t.Errorf("amazon cart = %s", cart)
	}
	if _, ok := aff.AmazonCartURL("de", []AmazonCartItem{{"B08ABCDEF1", 1}}); ok {
		t.Error("amazon cart without a tag")
	}
	l, _ := Classify("https://www.amazon.com/x/dp/B07K2Z5XYZ?psc=1&ref=abc")
	if got := aff.ProductURL(l); got != "https://www.amazon.com/dp/B07K2Z5XYZ?psc=1&tag=fitkit-20" {
		t.Errorf("amazon product = %s", got)
	}
	l, _ = Classify("https://www.aliexpress.com/item/1.html")
	if got := aff.ProductURL(l); !strings.HasPrefix(got, "https://s.click.aliexpress.com/deep_link.htm?aff_short_key=ak&dl_target_url=https%3A%2F%2F") {
		t.Errorf("aliexpress = %s", got)
	}
	l, _ = Classify("https://us.shein.com/a.html")
	if got := aff.ProductURL(l); !strings.HasPrefix(got, "https://go.skimresources.com/?id=123X&xs=1&url=") {
		t.Errorf("skimlinks = %s", got)
	}
	if got := (Affiliate{}).ProductURL(l); got != "https://us.shein.com/a.html" {
		t.Errorf("untagged = %s", got)
	}

	u := ShopifyCartURL("everlane.com", []CartLine{{11, 1}, {22, 2}}, Prefill{
		Email: "a+b@x.com", FirstName: "Ann", Address1: "1 Main St & Co", Country: "US",
	}, true)
	parsed, err := url.Parse(u)
	if err != nil || parsed.Path != "/cart/11:1,22:2" || parsed.Host != "everlane.com" {
		t.Fatalf("shopify cart = %s", u)
	}
	q := parsed.Query()
	if q.Get("checkout[email]") != "a+b@x.com" || q.Get("checkout[shipping_address][address1]") != "1 Main St & Co" ||
		q.Get("payment") != "shop_pay" || q.Has("checkout[shipping_address][zip]") {
		t.Errorf("shopify query = %v", q)
	}
}

const productJS = `{"id":1,"title":"Box-Cut Tee","handle":"tee",
 "options":[{"name":"Size","position":1,"values":["S","M"]},{"name":"Color","position":2,"values":["Black"]}],
 "variants":[
  {"id":101,"title":"S / Black","option1":"S","option2":"Black","option3":null,"available":false,"price":3000},
  {"id":102,"title":"M / Black","option1":"M","option2":"Black","option3":null,"available":true,"price":3000}]}`

func TestParseShopifyProduct(t *testing.T) {
	p, err := parseShopifyProduct([]byte(productJS))
	if err != nil {
		t.Fatal(err)
	}
	if len(p.Options) != 2 || p.Options[0].Name != "Size" || len(p.Variants) != 2 || p.Variants[1].Options[0] != "M" {
		t.Fatalf("product = %+v", p)
	}
	if v, ok := p.MatchSize("m"); !ok || v.ID != 102 {
		t.Errorf("match m = %+v", v)
	}
	if v, ok := p.MatchSize("S"); !ok || v.Available {
		t.Errorf("match S = %+v", v)
	}
	old, err := parseShopifyProduct([]byte(`{"title":"Cap","options":["Title"],"variants":[{"id":5,"title":"Default Title","option1":"Default Title","available":true,"price":1500}]}`))
	if err != nil || len(old.Options) != 0 || old.NeedsChoice() {
		t.Errorf("single variant = %+v %v", old, err)
	}
	if _, err := parseShopifyProduct([]byte(`<html>`)); err == nil {
		t.Error("html parsed")
	}
}

func TestPublicAddr(t *testing.T) {
	for addr, want := range map[string]bool{
		"8.8.8.8": true, "2606:4700::1111": true,
		"127.0.0.1": false, "10.1.2.3": false, "192.168.0.1": false, "169.254.169.254": false,
		"100.64.1.1": false, "::1": false, "::ffff:127.0.0.1": false, "0.0.0.0": false, "fd00::1": false,
	} {
		if got := publicAddr(netip.MustParseAddr(addr)); got != want {
			t.Errorf("%s = %v", addr, got)
		}
	}
	// The default client refuses private addresses even when DNS points there.
	s := &Service{}
	if _, err := s.fetch(context.Background(), "https://127.0.0.1/products/x.js"); !errors.Is(err, ErrUnavailable) {
		t.Errorf("loopback fetch err = %v", err)
	}
}

type memCache struct{ m map[string]string }

func (c *memCache) CachedShopData(_ context.Context, key string, _ time.Duration) (string, bool, error) {
	v, ok := c.m[key]
	return v, ok, nil
}

func (c *memCache) PutShopData(_ context.Context, key, body string) error {
	c.m[key] = body
	return nil
}

// shopServer serves one Shopify product and counts requests.
func shopServer(t *testing.T) (*httptest.Server, *atomic.Int32) {
	var hits atomic.Int32
	ts := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		hits.Add(1)
		switch r.URL.Path {
		case "/products/tee.js":
			w.Write([]byte(productJS))
		case "/meta.json":
			w.Write([]byte(`{"currency":"USD","ships_to_countries":["US","PL"]}`))
		default:
			http.NotFound(w, r)
		}
	}))
	t.Cleanup(ts.Close)
	return ts, &hits
}

func price(v float64) *float64 { return &v }

func TestBuildPlan(t *testing.T) {
	ts, hits := shopServer(t)
	host := strings.TrimPrefix(ts.URL, "https://")
	svc := &Service{Cache: &memCache{m: map[string]string{}}, Client: ts.Client(), ShopPay: true,
		Affiliate: Affiliate{AmazonTags: map[string]string{"com": "fitkit-20"}}}
	items := []analyze.Item{
		{ID: "item-1", Label: "Tee", Category: "top", Listings: []analyze.Listing{
			{Title: "Box-Cut Tee", Merchant: "Everlane", URL: "https://" + host + "/products/tee", PriceValue: price(1), Currency: "USD"},
		}},
		{ID: "item-2", Label: "Jeans", Category: "bottom", Listings: []analyze.Listing{
			{Title: "Jeans", Merchant: "Amazon", URL: "https://www.amazon.com/dp/B07K2Z5XYZ", PriceValue: price(49.99), Currency: "USD"},
		}},
		{ID: "item-3", Label: "Bag", Category: "bag", Listings: []analyze.Listing{
			{Title: "Tote", Merchant: "Amazon", URL: "https://www.amazon.com/dp/B08ABCDEF1", PriceValue: price(20), Currency: "USD"},
		}},
	}
	home := store.Address{ID: "a1", Kind: KindHome, Country: "US", FinalCountry: "US",
		Fields: store.AddressFields{FirstName: "Ann", LastName: "Lee", Line1: "1 Main St", City: "Austin", Zip: "78701", Region: "TX"}}
	req := func(choices ...Choice) PlanRequest {
		return PlanRequest{Items: items, Choices: choices, Email: "ann@x.com", Deliver: &home, Addresses: []store.Address{home}}
	}
	tee := Choice{ItemID: "item-1", ListingURL: items[0].Listings[0].URL, Size: "M", Quantity: 2}
	jeans := Choice{ItemID: "item-2", ListingURL: items[1].Listings[0].URL, Size: "28", Quantity: 1}
	bag := Choice{ItemID: "item-3", ListingURL: items[2].Listings[0].URL, Quantity: 1}

	plan, err := svc.Build(context.Background(), req(tee, jeans, bag))
	if err != nil {
		t.Fatal(err)
	}
	if len(plan.Stores) != 2 {
		t.Fatalf("stores = %+v", plan.Stores)
	}
	shopify := plan.Stores[0]
	if shopify.Method != MethodShopify || !shopify.Prefilled || shopify.Items[0].VariantID != 102 {
		t.Fatalf("shopify store = %+v", shopify)
	}
	// The store's price wins over the search listing's.
	if shopify.Subtotals[0].Amount != 60 || shopify.Items[0].Price.Amount != 30 {
		t.Errorf("shopify subtotal = %+v", shopify.Subtotals)
	}
	cart, _ := url.Parse(shopify.CheckoutURL)
	if cart.Path != "/cart/102:2" || cart.Query().Get("checkout[shipping_address][zip]") != "78701" || cart.Query().Get("checkout[email]") != "ann@x.com" {
		t.Errorf("cart url = %s", shopify.CheckoutURL)
	}
	amazon := plan.Stores[1]
	// Jeans need a size and the URL isn't one variation, so only the bag
	// goes in the cart; the jeans open on their own page.
	if amazon.Method != MethodAmazon || !strings.Contains(amazon.CheckoutURL, "ASIN.1=B08ABCDEF1") || strings.Contains(amazon.CheckoutURL, "B07K2Z5XYZ") {
		t.Errorf("amazon = %+v", amazon)
	}
	if amazon.Items[0].InCart || amazon.Items[0].Method != MethodProductPage || !amazon.Items[0].SizeAtCheckout || !amazon.Items[1].InCart {
		t.Errorf("amazon items = %+v", amazon.Items)
	}
	if len(plan.Totals) != 1 || plan.Totals[0].Amount != 129.99 {
		t.Errorf("totals = %+v", plan.Totals)
	}

	// Store data is cached: a second plan makes no requests.
	before := hits.Load()
	if _, err := svc.Build(context.Background(), req(tee)); err != nil || hits.Load() != before {
		t.Errorf("second build: err %v, %d new requests", err, hits.Load()-before)
	}

	for name, c := range map[string]struct {
		choices []Choice
		code    string
	}{
		"none":            {nil, "no_items"},
		"unknown item":    {[]Choice{{ItemID: "item-9", ListingURL: tee.ListingURL, Quantity: 1}}, "unknown_item"},
		"foreign listing": {[]Choice{{ItemID: "item-1", ListingURL: "https://evil.example/products/tee", Quantity: 1}}, "unknown_listing"},
		"other item's":    {[]Choice{{ItemID: "item-1", ListingURL: bag.ListingURL, Quantity: 1}}, "unknown_listing"},
		"zero quantity":   {[]Choice{{ItemID: "item-3", ListingURL: bag.ListingURL}}, "invalid_quantity"},
		"big quantity":    {[]Choice{{ItemID: "item-3", ListingURL: bag.ListingURL, Quantity: 6}}, "invalid_quantity"},
		"duplicate":       {[]Choice{bag, bag}, "duplicate_item"},
		"no size":         {[]Choice{{ItemID: "item-1", ListingURL: tee.ListingURL, Quantity: 1}}, "size_required"},
		"sold out":        {[]Choice{{ItemID: "item-1", ListingURL: tee.ListingURL, Size: "S", Quantity: 1}}, "sold_out"},
		"foreign variant": {[]Choice{{ItemID: "item-1", ListingURL: tee.ListingURL, VariantID: 999, Quantity: 1}}, "invalid_variant"},
		"control chars":   {[]Choice{{ItemID: "item-3", ListingURL: bag.ListingURL, Size: "M\n", Quantity: 1}}, ""},
		"long size":       {[]Choice{{ItemID: "item-3", ListingURL: bag.ListingURL, Size: strings.Repeat("x", 40), Quantity: 1}}, "invalid_size"},
	} {
		_, err := svc.Build(context.Background(), req(c.choices...))
		var ie *InputError
		if c.code == "" {
			// Surrounding whitespace is trimmed, not rejected.
			if err != nil {
				t.Errorf("%s: %v", name, err)
			}
			continue
		}
		if !errors.As(err, &ie) || ie.Code != c.code {
			t.Errorf("%s: err = %v, want %s", name, err, c.code)
		}
	}
}

func TestRoutes(t *testing.T) {
	home := store.Address{ID: "home", Kind: KindNPBranch, Country: "UA", FinalCountry: "UA"}
	us := store.Address{ID: "us", Kind: KindForwarder, Forwarder: "np_shopping", Country: "US", FinalCountry: "UA"}
	pl := store.Address{ID: "pl", Kind: KindForwarder, Forwarder: "np_shopping", Country: "PL", FinalCountry: "UA"}
	book := []store.Address{home, us, pl}

	if r := chooseRoute("aliexpress.com", nil, &home, book); r.address.ID != "home" || r.via != "direct" {
		t.Errorf("aliexpress ships to UA: %+v", r)
	}
	if r := chooseRoute("nordstrom.com", nil, &home, book); r.address.ID != "us" || r.via != "forwarder" || !strings.Contains(r.note, "NP Shopping") {
		t.Errorf("US store: %+v", r)
	}
	if r := chooseRoute("zalando.de", nil, &home, book); r.address.ID != "pl" {
		t.Errorf("EU store: %+v", r)
	}
	if r := chooseRoute("brand.co", &Storefront{Currency: "EUR"}, &home, book); r.address.ID != "pl" {
		t.Errorf("unknown EUR store: %+v", r)
	}
	if r := chooseRoute("brand.co", &Storefront{ShipsTo: []string{"UA"}}, &home, book); r.address.ID != "home" {
		t.Errorf("Shopify store shipping to UA: %+v", r)
	}
	if r := chooseRoute("nordstrom.com", nil, &home, []store.Address{home}); r.address.ID != "home" || len(r.warnings) != 1 {
		t.Errorf("no forwarder: %+v", r)
	}
	de := store.Address{ID: "de", Kind: KindHome, Country: "DE", FinalCountry: "DE"}
	if r := chooseRoute("nordstrom.com", nil, &de, nil); len(r.warnings) != 1 {
		t.Errorf("US-only store to DE: %+v", r)
	}
	if w := customsWarnings("aliexpress.com", nil, "DE", 1000, "EUR"); len(w) != 1 {
		t.Errorf("EU customs = %v", w)
	}
	if w := customsWarnings("aliexpress.com", nil, "UA", 200_00, "USD"); len(w) != 1 || !strings.Contains(w[0], "€150") {
		t.Errorf("UA customs = %v", w)
	}
	p := prefill("a@x.com", &store.Address{Kind: KindForwarder, Country: "PL", Fields: store.AddressFields{Line1: "Magazynowa 1", SuiteID: "NP123"}})
	if p.Address2 != "NP123" || p.Country != "PL" {
		t.Errorf("forwarder prefill = %+v", p)
	}
}
