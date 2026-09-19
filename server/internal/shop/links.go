package shop

import (
	"net/url"
	"strconv"
	"strings"
)

// Affiliate holds the tags that earn Fitkit a commission. Every field is
// optional; with nothing set, links go out untouched.
type Affiliate struct {
	// AmazonTags maps an Amazon site's TLD ("com", "de", "co.uk") to its
	// Associates tag.
	AmazonTags map[string]string
	// AliExpressKey is the AliExpress Portals short key for deep links.
	AliExpressKey string
	// SkimlinksID is the sub-affiliate network's publisher ID, used for
	// every other affiliate store.
	SkimlinksID string
}

// AmazonTag returns the Associates tag for an Amazon site, if any.
func (a Affiliate) AmazonTag(tld string) string { return a.AmazonTags[tld] }

// ProductURL is the link that opens a listing's product page, tagged when
// Fitkit has a program for that store.
func (a Affiliate) ProductURL(l Listing) string {
	switch {
	case l.AmazonTLD != "" && l.ASIN != "":
		u := url.URL{Scheme: "https", Host: "www.amazon." + l.AmazonTLD, Path: "/dp/" + l.ASIN}
		q := url.Values{}
		if l.Variation {
			// Keeps Amazon on the size or color the URL pointed at.
			q.Set("psc", "1")
		}
		if tag := a.AmazonTag(l.AmazonTLD); tag != "" {
			q.Set("tag", tag)
		}
		u.RawQuery = q.Encode()
		return u.String()
	case l.Method != MethodAffiliate:
		return l.URL
	case isAliExpress(l.Host) && a.AliExpressKey != "":
		return "https://s.click.aliexpress.com/deep_link.htm?aff_short_key=" + url.QueryEscape(a.AliExpressKey) +
			"&dl_target_url=" + url.QueryEscape(l.URL)
	case a.SkimlinksID != "":
		return "https://go.skimresources.com/?id=" + url.QueryEscape(a.SkimlinksID) + "&xs=1&url=" + url.QueryEscape(l.URL)
	}
	return l.URL
}

// AmazonCartItem is one line of an Amazon add-to-cart link.
type AmazonCartItem struct {
	ASIN     string
	Quantity int
}

// AmazonCartURL builds Amazon's add-to-cart form link. The form requires an
// Associates tag, so there's no cart link for a site without one.
func (a Affiliate) AmazonCartURL(tld string, items []AmazonCartItem) (string, bool) {
	tag := a.AmazonTag(tld)
	if tag == "" || len(items) == 0 {
		return "", false
	}
	q := url.Values{}
	for i, it := range items {
		n := strconv.Itoa(i + 1)
		q.Set("ASIN."+n, it.ASIN)
		q.Set("Quantity."+n, strconv.Itoa(it.Quantity))
	}
	q.Set("AssociateTag", tag)
	return "https://www.amazon." + tld + "/gp/aws/cart/add.html?" + q.Encode(), true
}

// CartLine is one variant in a Shopify cart permalink.
type CartLine struct {
	VariantID int64
	Quantity  int
}

// Prefill is what a Shopify checkout can be filled in with.
type Prefill struct {
	Email     string
	FirstName string
	LastName  string
	Address1  string
	Address2  string
	City      string
	Province  string
	Country   string
	Zip       string
	Phone     string
}

// ShopifyCartURL builds a cart permalink that goes straight to checkout:
// https://<host>/cart/<variant>:<qty>,…?checkout[email]=…
func ShopifyCartURL(host string, lines []CartLine, p Prefill, shopPay bool) string {
	parts := make([]string, len(lines))
	for i, l := range lines {
		parts[i] = strconv.FormatInt(l.VariantID, 10) + ":" + strconv.Itoa(l.Quantity)
	}
	q := url.Values{}
	set := func(key, value string) {
		if value = strings.TrimSpace(value); value != "" {
			q.Set(key, value)
		}
	}
	set("checkout[email]", p.Email)
	set("checkout[shipping_address][first_name]", p.FirstName)
	set("checkout[shipping_address][last_name]", p.LastName)
	set("checkout[shipping_address][address1]", p.Address1)
	set("checkout[shipping_address][address2]", p.Address2)
	set("checkout[shipping_address][city]", p.City)
	set("checkout[shipping_address][province]", p.Province)
	set("checkout[shipping_address][country]", p.Country)
	set("checkout[shipping_address][zip]", p.Zip)
	set("checkout[shipping_address][phone]", p.Phone)
	if shopPay {
		q.Set("payment", "shop_pay")
	}
	u := url.URL{Scheme: "https", Host: host, Path: "/cart/" + strings.Join(parts, ",")}
	u.RawQuery = q.Encode()
	return u.String()
}
