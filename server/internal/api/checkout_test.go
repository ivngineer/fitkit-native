package api

import (
	"strings"
	"testing"

	"fitkit/server/internal/analyze"
	"fitkit/server/internal/config"
	"fitkit/server/internal/shop"
)

// signedIn signs a new user up and imports their pins.
func signedIn(t *testing.T, c *client, email string) *client {
	t.Helper()
	u := &client{t: t, base: c.base}
	var auth struct {
		Token string `json:"token"`
	}
	u.do("POST", "/v1/auth/signup", credentials{email, "password123"}, &auth)
	u.token = auth.Token
	return u
}

func TestCheckoutFlow(t *testing.T) {
	c := signedIn(t, newTestServer(t, config.Config{DemoAnalysis: true}), "ann@example.com")
	other := signedIn(t, c, "sam@example.com")

	c.do("PUT", "/v1/me/pinterest", map[string]string{"handle": "ann"}, nil)
	var job importJSON
	c.do("POST", "/v1/imports", nil, &job)
	eventually(t, func() bool {
		c.do("GET", "/v1/imports/"+job.ID, nil, &job)
		return job.Status == "done"
	})
	var page struct {
		Pins []pinJSON `json:"pins"`
	}
	c.do("GET", "/v1/pins", nil, &page)
	pinID := page.Pins[0].ID

	// Addresses: validation, first one becomes default, ownership.
	for name, a := range map[string]addressJSON{
		"no kind":          {Country: "US", Fields: fields()},
		"bad country":      {Kind: "home", Country: "USA", Fields: fields()},
		"no name":          {Kind: "home", Country: "US"},
		"branch not in UA": {Kind: "np_branch", Country: "PL", Fields: fields()},
		"no suite":         {Kind: "forwarder", Forwarder: "np_shopping", Country: "US", Fields: fields()},
		"bad forwarder":    {Kind: "forwarder", Forwarder: "acme", Country: "US", Fields: withSuite()},
		"bad phone":        {Kind: "home", Country: "US", Fields: func() (f addressFields) { f = fields(); f.Phone = "call me"; return }()},
		"too long":         {Kind: "home", Country: "US", Fields: func() (f addressFields) { f = fields(); f.City = strings.Repeat("a", 101); return }()},
	} {
		if code := c.do("POST", "/v1/addresses", a, nil); code != 422 {
			t.Errorf("%s: status %d", name, code)
		}
	}
	var branch, fwd addressJSON
	if code := c.do("POST", "/v1/addresses", addressJSON{Kind: "np_branch", Country: "ua", Fields: func() (f addressFields) {
		f = fields()
		f.NPBranch, f.Phone = "12", "+380 50 123 4567"
		return
	}()}, &branch); code != 201 || !branch.IsDefault || branch.Country != "UA" || branch.FinalCountry != "UA" {
		t.Fatalf("branch: %d %+v", code, branch)
	}
	if code := c.do("POST", "/v1/addresses", addressJSON{Kind: "forwarder", Forwarder: "np_shopping", Country: "US", Fields: withSuite()}, &fwd); code != 201 || fwd.IsDefault || fwd.FinalCountry != "UA" {
		t.Fatalf("forwarder: %d %+v", code, fwd)
	}
	if code := other.do("PUT", "/v1/addresses/"+fwd.ID, addressJSON{Kind: "home", Country: "US", Fields: fields()}, nil); code != 404 {
		t.Errorf("foreign address update %d", code)
	}
	if code := other.do("DELETE", "/v1/addresses/"+fwd.ID, nil, nil); code != 404 {
		t.Errorf("foreign address delete %d", code)
	}

	// Sizes.
	var sizes struct {
		Sizes map[string]string `json:"sizes"`
	}
	if code := c.do("PUT", "/v1/sizes", map[string]any{"sizes": map[string]string{"top": " M ", "shoes": ""}}, &sizes); code != 200 || sizes.Sizes["top"] != "M" || len(sizes.Sizes) != 1 {
		t.Fatalf("sizes: %d %+v", code, sizes)
	}
	if code := c.do("PUT", "/v1/sizes", map[string]any{"sizes": map[string]string{"rocket": "M"}}, nil); code != 422 {
		t.Errorf("bad size category %d", code)
	}

	// Checkout needs the analysis.
	if code := c.do("POST", "/v1/looks", map[string]any{"pinId": pinID, "items": []shop.Choice{{ItemID: "item-1", Quantity: 1}}}, nil); code != 409 {
		t.Fatalf("look before analysis %d", code)
	}
	var analysis struct {
		Status string          `json:"status"`
		Result *analyze.Result `json:"result"`
	}
	c.do("POST", "/v1/pins/"+pinID+"/analysis", nil, &analysis)
	eventually(t, func() bool {
		c.do("GET", "/v1/pins/"+pinID+"/analysis", nil, &analysis)
		return analysis.Status == "done"
	})
	top := analysis.Result.Items[0]

	var opts struct {
		Options []shop.ListingOptions `json:"options"`
	}
	if code := c.do("POST", "/v1/listings/options", map[string]any{"pinId": pinID, "urls": []string{top.Listings[0].URL}}, &opts); code != 200 ||
		len(opts.Options) != 1 || opts.Options[0].Method != shop.MethodShopify || len(opts.Options[0].Product.Variants) != 5 {
		t.Fatalf("options: %d %+v", code, opts)
	}
	if code := c.do("POST", "/v1/listings/options", map[string]any{"pinId": pinID, "urls": []string{"https://169.254.169.254/products/x"}}, nil); code != 422 {
		t.Errorf("options for a URL outside the analysis %d", code)
	}
	if code := other.do("POST", "/v1/listings/options", map[string]any{"pinId": pinID, "urls": []string{top.Listings[0].URL}}, nil); code != 404 {
		t.Errorf("options for a foreign pin %d", code)
	}

	variant := opts.Options[0].Product.Variants[2]
	body := map[string]any{"pinId": pinID, "addressId": branch.ID, "items": []shop.Choice{
		{ItemID: top.ID, ListingURL: top.Listings[0].URL, VariantID: variant.ID, Quantity: 1},
		{ItemID: analysis.Result.Items[1].ID, ListingURL: analysis.Result.Items[1].Listings[2].URL, Size: "M", Quantity: 1},
	}}
	var errBody struct {
		Error struct{ Code, ItemID string } `json:"error"`
	}
	bad := map[string]any{"pinId": pinID, "items": []shop.Choice{{ItemID: top.ID, ListingURL: top.Listings[0].URL, Quantity: 1}}}
	if code := c.do("POST", "/v1/looks", bad, &errBody); code != 422 || errBody.Error.Code != "size_required" || errBody.Error.ItemID != top.ID {
		t.Errorf("look without size: %d %+v", code, errBody)
	}
	if code := c.do("POST", "/v1/looks", map[string]any{"pinId": pinID, "addressId": "nope", "items": body["items"]}, nil); code != 422 {
		t.Errorf("look with unknown address %d", code)
	}
	if code := other.do("POST", "/v1/looks", body, nil); code != 404 {
		t.Errorf("look on a foreign pin %d", code)
	}

	var look struct {
		ID     string          `json:"id"`
		Plan   shop.Plan       `json:"plan"`
		Stores []lookStoreJSON `json:"stores"`
	}
	if code := c.do("POST", "/v1/looks", body, &look); code != 201 || len(look.Plan.Stores) != 2 || len(look.Stores) != 2 {
		t.Fatalf("look: %d %+v", code, look)
	}
	demo := look.Plan.Stores[0]
	if demo.Method != shop.MethodShopify || !strings.Contains(demo.CheckoutURL, "/cart/") || demo.Address == nil || demo.Address.ID != branch.ID {
		t.Errorf("demo store = %+v", demo)
	}
	if look.Stores[0].Status != "pending" || look.Plan.Disclosure == "" {
		t.Errorf("look = %+v", look)
	}

	// Orders: mark ordered with tracking; only the owner can.
	merchant := look.Stores[0].Merchant
	upd := map[string]any{"status": "shipped", "orderRef": "#1001", "trackingNo": "20450012345678", "carrier": "Nova Poshta"}
	if code := other.do("PUT", "/v1/looks/"+look.ID+"/stores/"+merchant, upd, nil); code != 404 {
		t.Errorf("foreign store update %d", code)
	}
	if code := c.do("PUT", "/v1/looks/"+look.ID+"/stores/"+merchant, map[string]any{"status": "paid"}, nil); code != 422 {
		t.Errorf("bad status %d", code)
	}
	if code := c.do("PUT", "/v1/looks/"+look.ID+"/stores/"+merchant, map[string]any{"status": "shipped", "trackingNo": "<script>"}, nil); code != 422 {
		t.Errorf("bad tracking %d", code)
	}
	var ls lookStoreJSON
	if code := c.do("PUT", "/v1/looks/"+look.ID+"/stores/"+merchant, upd, &ls); code != 200 || ls.Status != "shipped" ||
		ls.TrackingURL == nil || !strings.HasPrefix(*ls.TrackingURL, "https://novaposhta.ua/") {
		t.Errorf("store update: %d %+v", code, ls)
	}
	var looks struct {
		Looks []lookJSON `json:"looks"`
	}
	c.do("GET", "/v1/looks", nil, &looks)
	if len(looks.Looks) != 1 || looks.Looks[0].Stores[0].OrderRef != "#1001" || looks.Looks[0].PinTitle == "" {
		t.Errorf("looks = %+v", looks)
	}
	if code := other.do("GET", "/v1/looks/"+look.ID, nil, nil); code != 404 {
		t.Errorf("foreign look %d", code)
	}
	other.do("GET", "/v1/looks", nil, &looks)
	if len(looks.Looks) != 0 {
		t.Errorf("foreign looks listed: %+v", looks)
	}
}

type addressFields = struct {
	FirstName string `json:"firstName"`
	LastName  string `json:"lastName"`
	Line1     string `json:"line1"`
	Line2     string `json:"line2"`
	City      string `json:"city"`
	Region    string `json:"region"`
	Zip       string `json:"zip"`
	Phone     string `json:"phone"`
	SuiteID   string `json:"suiteId"`
	NPBranch  string `json:"npBranch"`
}

func fields() addressFields {
	return addressFields{FirstName: "Ann", LastName: "Lee", Line1: "1 Main St", City: "Kyiv", Zip: "01001"}
}

func withSuite() addressFields {
	f := fields()
	f.SuiteID = "NP12345"
	return f
}
