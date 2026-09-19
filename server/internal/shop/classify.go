// Package shop turns shopping listings into checkout links. Fitkit never
// takes payment: every link opens the store's own checkout, prefilled as far
// as the store allows.
package shop

import (
	"errors"
	"net/url"
	"regexp"
	"strings"
)

// Method is how a listing's store is checked out.
type Method string

const (
	// MethodShopify opens a cart permalink straight to checkout with the
	// chosen variants, email and shipping address filled in.
	MethodShopify Method = "shopify_cart"
	// MethodAmazon opens Amazon's add-to-cart form with the item added.
	MethodAmazon Method = "amazon_cart"
	// MethodAffiliate opens the product page through an affiliate deep link.
	MethodAffiliate Method = "affiliate_link"
	// MethodProductPage opens the product page as is.
	MethodProductPage Method = "product_page"
)

// Rank orders methods from the least to the most friction at checkout.
func (m Method) Rank() int {
	switch m {
	case MethodShopify:
		return 0
	case MethodAmazon:
		return 1
	case MethodAffiliate:
		return 2
	}
	return 3
}

var ErrBadURL = errors.New("listing URL must be an http or https link")

// Listing is a classified listing URL. Method is a first guess from the URL
// alone: Shopify stores can only be confirmed by loading their product JSON,
// so a Shopify-shaped URL reports MethodProductPage with ShopifyHandle set.
type Listing struct {
	// URL is the product page, unwrapped from redirectors, with no fragment.
	URL string
	// Host includes the port when the URL has one.
	Host     string
	Merchant string
	Method   Method

	// Amazon listings.
	ASIN      string
	AmazonTLD string
	// Variation reports that the Amazon URL points at one size or color
	// (psc=1), so its ASIN can go straight into a cart.
	Variation bool

	// ShopifyHandle is the product handle when the path looks like a
	// Shopify product page (/products/<handle>).
	ShopifyHandle string
}

// Classify normalizes a listing URL and guesses its checkout method.
func Classify(raw string) (Listing, error) {
	u, err := parseHTTP(raw)
	if err != nil {
		return Listing{}, err
	}
	for range 3 {
		inner := unwrapRedirect(u)
		if inner == nil {
			break
		}
		u = inner
	}
	u.Fragment = ""
	u.RawFragment = ""
	host := strings.ToLower(u.Hostname())
	l := Listing{URL: u.String(), Host: strings.ToLower(u.Host), Merchant: merchantKey(host), Method: MethodProductPage}

	if tld, ok := amazonTLD(host); ok {
		l.AmazonTLD = tld
		l.Merchant = "amazon." + tld
		if asin := extractASIN(u.EscapedPath()); asin != "" {
			l.ASIN = asin
			l.Variation = u.Query().Get("psc") == "1"
			l.Method = MethodAmazon
		}
		return l, nil
	}
	if affiliateMerchant(host) {
		l.Method = MethodAffiliate
		return l, nil
	}
	l.ShopifyHandle = shopifyHandle(u.EscapedPath())
	return l, nil
}

func parseHTTP(raw string) (*url.URL, error) {
	u, err := url.Parse(strings.TrimSpace(raw))
	if err != nil || (u.Scheme != "https" && u.Scheme != "http") || u.Hostname() == "" || u.User != nil {
		return nil, ErrBadURL
	}
	return u, nil
}

// unwrapRedirect returns the target of a known redirector link, or nil.
// Visual search results sometimes point at Google's click trackers.
func unwrapRedirect(u *url.URL) *url.URL {
	host := strings.ToLower(u.Hostname())
	q := u.Query()
	var target string
	switch {
	case isGoogleHost(host) && u.Path == "/url":
		target = firstNonEmpty(q.Get("q"), q.Get("url"))
	case isGoogleHost(host) && u.Path == "/aclk":
		target = q.Get("adurl")
	case host == "l.facebook.com" || host == "lm.facebook.com":
		target = q.Get("u")
	case host == "l.instagram.com":
		target = q.Get("u")
	}
	if target == "" {
		return nil
	}
	inner, err := parseHTTP(target)
	if err != nil {
		return nil
	}
	return inner
}

func isGoogleHost(host string) bool {
	host = strings.TrimPrefix(host, "www.")
	return host == "google.com" || strings.HasPrefix(host, "google.")
}

// merchantKey names a store by its host without common subdomains, so
// www.everlane.com and everlane.com group together.
func merchantKey(host string) string {
	for _, prefix := range []string{"www.", "m.", "shop.", "store.", "us.", "eu."} {
		if rest, ok := strings.CutPrefix(host, prefix); ok && strings.Contains(rest, ".") {
			return rest
		}
	}
	return host
}

var amazonTLDs = []string{"com", "de", "fr", "it", "es", "co.uk", "nl", "pl", "se", "com.be", "ie"}

func amazonTLD(host string) (string, bool) {
	host = strings.TrimPrefix(strings.TrimPrefix(host, "www."), "smile.")
	for _, tld := range amazonTLDs {
		if host == "amazon."+tld {
			return tld, true
		}
	}
	return "", false
}

var asinPattern = regexp.MustCompile(`/(?:dp|gp/product|gp/aw/d)/([A-Z0-9]{10})(?:[/?]|$)`)

func extractASIN(path string) string {
	if m := asinPattern.FindStringSubmatch(path); m != nil {
		return m[1]
	}
	return ""
}

// handlePattern matches /products/<handle>, optionally under a collection or
// a locale prefix like /en-us.
var handlePattern = regexp.MustCompile(`^(?:/[a-z]{2}(?:-[a-z]{2})?)?(?:/collections/[^/]+)?/products/([a-z0-9][a-z0-9\-_%.]*?)(?:\.js|\.json)?/?$`)

func shopifyHandle(path string) string {
	m := handlePattern.FindStringSubmatch(strings.ToLower(path))
	if m == nil {
		return ""
	}
	h, err := url.PathUnescape(m[1])
	if err != nil || strings.ContainsAny(h, "/?#") {
		return ""
	}
	return h
}

// affiliateHosts sell through affiliate networks rather than an open cart.
var affiliateHosts = []string{"aliexpress.", "shein.", "hm.com", "zara.com", "etsy.com", "dhgate.com", "asos.com", "zalando."}

func affiliateMerchant(host string) bool {
	host = merchantKey(host)
	for _, h := range affiliateHosts {
		if strings.HasSuffix(h, ".") {
			if strings.HasPrefix(host, h) || strings.Contains(host, "."+h) {
				return true
			}
		} else if host == h || strings.HasSuffix(host, "."+h) {
			return true
		}
	}
	return false
}

func isAliExpress(host string) bool {
	host = merchantKey(stripPort(host))
	return strings.HasPrefix(host, "aliexpress.") || strings.Contains(host, ".aliexpress.")
}

func stripPort(host string) string {
	if h, _, ok := strings.Cut(host, ":"); ok {
		return h
	}
	return host
}

func firstNonEmpty(values ...string) string {
	for _, v := range values {
		if v != "" {
			return v
		}
	}
	return ""
}
