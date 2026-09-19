package shop

import (
	"context"
	"errors"
	"fmt"
	"math"
	"net/http"
	"slices"
	"strings"
	"sync"
	"unicode"
	"unicode/utf8"

	"fitkit/server/internal/analyze"
	"fitkit/server/internal/store"
)

// Service builds checkout links and plans.
type Service struct {
	Cache     Cache
	Affiliate Affiliate
	// Client fetches store data; nil uses a client that refuses private
	// addresses.
	Client *http.Client
	// ShopPay sends Shopify checkouts to Shop Pay.
	ShopPay bool
	// Demo serves *.fitkit.example stores without network calls.
	Demo bool
}

const (
	MaxPlanItems = 8
	MaxQuantity  = 5
	maxSizeLen   = 32
	fetchWorkers = 4
)

// Disclosure is shown wherever affiliate links are.
const Disclosure = "Some store links may earn Fitkit a commission. It never changes your price."

// InputError is a problem with what the client sent, safe to show the user.
type InputError struct {
	Code    string
	Message string
	ItemID  string
}

func (e *InputError) Error() string { return e.Message }

func inputErr(code, itemID, format string, args ...any) *InputError {
	return &InputError{Code: code, ItemID: itemID, Message: fmt.Sprintf(format, args...)}
}

// Choice is one piece the user wants, as sent by the app.
type Choice struct {
	ItemID     string `json:"itemId"`
	ListingURL string `json:"listingUrl"`
	VariantID  int64  `json:"variantId,omitempty"`
	Size       string `json:"size,omitempty"`
	Quantity   int    `json:"quantity"`
}

type Money struct {
	Amount   float64 `json:"amount"`
	Currency string  `json:"currency"`
}

type PlanItem struct {
	ItemID     string `json:"itemId"`
	Label      string `json:"label"`
	Category   string `json:"category"`
	Title      string `json:"title"`
	ImageURL   string `json:"imageUrl,omitempty"`
	ListingURL string `json:"listingUrl"`
	// OpenURL is the item's own product page, tagged where possible.
	OpenURL      string `json:"openUrl"`
	Method       Method `json:"method"`
	InCart       bool   `json:"inCart"`
	VariantID    int64  `json:"variantId,omitempty"`
	VariantTitle string `json:"variantTitle,omitempty"`
	Size         string `json:"size,omitempty"`
	// SizeAtCheckout means the size is picked on the store's page.
	SizeAtCheckout bool   `json:"sizeAtCheckout"`
	Quantity       int    `json:"quantity"`
	Price          *Money `json:"price,omitempty"`
}

type PlanAddress struct {
	ID      string `json:"id"`
	Label   string `json:"label"`
	Kind    string `json:"kind"`
	Via     string `json:"via"`
	Summary string `json:"summary"`
}

type PlanStore struct {
	Merchant string `json:"merchant"`
	Name     string `json:"name"`
	// Method is how CheckoutURL checks out. Items not in the cart open on
	// their own OpenURL.
	Method      Method       `json:"method"`
	CheckoutURL string       `json:"checkoutUrl"`
	Prefilled   bool         `json:"prefilled"`
	Items       []PlanItem   `json:"items"`
	Subtotals   []Money      `json:"subtotals"`
	Address     *PlanAddress `json:"address,omitempty"`
	Note        string       `json:"note,omitempty"`
	Warnings    []string     `json:"warnings"`
}

type Plan struct {
	Stores     []PlanStore `json:"stores"`
	Totals     []Money     `json:"totals"`
	Warnings   []string    `json:"warnings"`
	Disclosure string      `json:"disclosure"`
}

// PlanRequest is everything a plan is built from. Items is the pin's stored
// analysis, never anything from the client: prices and links only come from
// there or from the store itself.
type PlanRequest struct {
	Items     []analyze.Item
	Choices   []Choice
	Email     string
	Deliver   *store.Address
	Addresses []store.Address
}

// resolved is one validated choice with what the store said about it.
type resolved struct {
	choice  Choice
	item    analyze.Item
	listing analyze.Listing
	class   Listing
	product *Product
	variant *Variant
	sf      *Storefront
	note    string
}

// Build validates the choices against the analysis and turns them into one
// checkout per store.
func (s *Service) Build(ctx context.Context, req PlanRequest) (Plan, error) {
	picks, err := validateChoices(req.Items, req.Choices)
	if err != nil {
		return Plan{}, err
	}
	if err := s.loadStores(ctx, picks); err != nil {
		return Plan{}, err
	}
	for _, p := range picks {
		if err := p.pickVariant(); err != nil {
			return Plan{}, err
		}
	}
	return s.assemble(picks, req), nil
}

func validateChoices(items []analyze.Item, choices []Choice) ([]*resolved, error) {
	if len(choices) == 0 {
		return nil, inputErr("no_items", "", "Pick at least one piece.")
	}
	if len(choices) > MaxPlanItems {
		return nil, inputErr("too_many_items", "", "Pick at most %d pieces.", MaxPlanItems)
	}
	byID := map[string]analyze.Item{}
	for _, it := range items {
		byID[it.ID] = it
	}
	seen := map[string]bool{}
	out := make([]*resolved, 0, len(choices))
	for _, c := range choices {
		item, ok := byID[c.ItemID]
		if !ok {
			return nil, inputErr("unknown_item", c.ItemID, "One of the pieces is no longer in this look. Refresh and try again.")
		}
		if seen[c.ItemID] {
			return nil, inputErr("duplicate_item", c.ItemID, "%s is in the list twice.", item.Label)
		}
		seen[c.ItemID] = true
		var listing *analyze.Listing
		for i := range item.Listings {
			if item.Listings[i].URL == c.ListingURL {
				listing = &item.Listings[i]
				break
			}
		}
		if listing == nil {
			return nil, inputErr("unknown_listing", c.ItemID, "That store option for %s is no longer available. Refresh and try again.", item.Label)
		}
		if c.Quantity < 1 || c.Quantity > MaxQuantity {
			return nil, inputErr("invalid_quantity", c.ItemID, "Quantity for %s must be between 1 and %d.", item.Label, MaxQuantity)
		}
		c.Size = strings.TrimSpace(c.Size)
		if utf8.RuneCountInString(c.Size) > maxSizeLen || strings.IndexFunc(c.Size, unicode.IsControl) >= 0 {
			return nil, inputErr("invalid_size", c.ItemID, "The size for %s doesn't look right.", item.Label)
		}
		if c.VariantID < 0 {
			return nil, inputErr("invalid_variant", c.ItemID, "That size for %s isn't available.", item.Label)
		}
		class, err := Classify(listing.URL)
		if err != nil {
			return nil, inputErr("unsupported_listing", c.ItemID, "%s's store link can't be opened.", item.Label)
		}
		out = append(out, &resolved{choice: c, item: item, listing: *listing, class: class})
	}
	return out, nil
}

// loadStores fetches Shopify product and store data for every pick, a few
// at a time. A store that can't be reached falls back to its product page.
func (s *Service) loadStores(ctx context.Context, picks []*resolved) error {
	var (
		wg   sync.WaitGroup
		mu   sync.Mutex
		fail error
		sem  = make(chan struct{}, fetchWorkers)
	)
	storefronts := map[string]*Storefront{}
	for _, p := range picks {
		if p.class.ShopifyHandle == "" {
			continue
		}
		wg.Go(func() {
			sem <- struct{}{}
			defer func() { <-sem }()
			product, err := s.ShopifyProduct(ctx, p.class)
			if err != nil && !errors.Is(err, ErrUnavailable) {
				mu.Lock()
				fail = err
				mu.Unlock()
				return
			}
			if err != nil {
				p.note = "Couldn't reach the store just now; choose the size at checkout."
			}
			p.product = product
		})
	}
	wg.Wait()
	if fail != nil {
		return fail
	}
	for _, p := range picks {
		if p.product == nil {
			continue
		}
		p.class.Method = MethodShopify
		if sf, ok := storefronts[p.class.Host]; ok {
			p.sf = sf
			continue
		}
		sf, err := s.ShopifyStorefront(ctx, p.class.Host)
		if err != nil && !errors.Is(err, ErrUnavailable) {
			return err
		}
		p.sf = &sf
		storefronts[p.class.Host] = &sf
	}
	return nil
}

// pickVariant settles which Shopify variant goes in the cart.
func (p *resolved) pickVariant() error {
	if p.product == nil {
		return nil
	}
	label := p.item.Label
	var v Variant
	switch {
	case p.choice.VariantID != 0:
		var ok bool
		if v, ok = p.product.Variant(p.choice.VariantID); !ok {
			return inputErr("invalid_variant", p.item.ID, "That size for %s isn't sold by this store. Pick another.", label)
		}
	case !p.product.NeedsChoice():
		v = p.product.Variants[0]
	default:
		var ok bool
		if v, ok = p.product.MatchSize(p.choice.Size); !ok {
			return inputErr("size_required", p.item.ID, "Pick a size for %s.", label)
		}
	}
	if !v.Available {
		return inputErr("sold_out", p.item.ID, "%s in %s is sold out. Pick another size.", label, orDefault(v.Title, "that size"))
	}
	p.variant = &v
	return nil
}

// sizedCategories are pieces bought by size. An Amazon link for one of these
// only goes in a cart when it already points at one size (psc=1).
var sizedCategories = map[string]bool{"top": true, "bottom": true, "dress": true, "outerwear": true, "shoes": true}

func (p *resolved) inCart(aff Affiliate) bool {
	switch p.class.Method {
	case MethodShopify:
		return p.variant != nil
	case MethodAmazon:
		return aff.AmazonTag(p.class.AmazonTLD) != "" && (p.class.Variation || !sizedCategories[p.item.Category])
	}
	return false
}

// price is the unit price in minor units: the store's own for Shopify, the
// search listing's otherwise.
func (p *resolved) price() (int64, string, bool) {
	if p.variant != nil && p.variant.Price > 0 {
		currency := strings.ToUpper(p.listing.Currency)
		if p.sf != nil && p.sf.Currency != "" {
			currency = p.sf.Currency
		}
		return p.variant.Price, currency, true
	}
	if p.listing.PriceValue == nil || *p.listing.PriceValue <= 0 || math.IsInf(*p.listing.PriceValue, 0) || *p.listing.PriceValue > 1e7 {
		return 0, "", false
	}
	return int64(math.Round(*p.listing.PriceValue * 100)), strings.ToUpper(p.listing.Currency), true
}

func (s *Service) assemble(picks []*resolved, req PlanRequest) Plan {
	var order []string
	groups := map[string][]*resolved{}
	for _, p := range picks {
		m := p.class.Merchant
		if _, ok := groups[m]; !ok {
			order = append(order, m)
		}
		groups[m] = append(groups[m], p)
	}

	plan := Plan{Stores: []PlanStore{}, Warnings: []string{}, Disclosure: Disclosure}
	totals := newSums()
	for _, merchant := range order {
		group := groups[merchant]
		st := PlanStore{Merchant: merchant, Name: orDefault(group[0].listing.Merchant, merchant), Warnings: []string{}}
		sums := newSums()
		var sf *Storefront
		for _, p := range group {
			if p.sf != nil {
				sf = p.sf
			}
		}

		rt := chooseRoute(merchant, sf, req.Deliver, req.Addresses)
		if rt.address != nil {
			st.Address = &PlanAddress{ID: rt.address.ID, Label: rt.address.Label, Kind: rt.address.Kind, Via: rt.via, Summary: AddressSummary(rt.address)}
		}
		st.Note = rt.note
		st.Warnings = append(st.Warnings, rt.warnings...)

		var cartLines []CartLine
		var amazonItems []AmazonCartItem
		for _, p := range group {
			item := PlanItem{
				ItemID: p.item.ID, Label: p.item.Label, Category: p.item.Category, Title: p.listing.Title,
				ImageURL: orDefault(p.item.CropURL, p.listing.ThumbnailURL), ListingURL: p.listing.URL,
				OpenURL: s.Affiliate.ProductURL(p.class), Method: p.class.Method, Quantity: p.choice.Quantity,
				Size: p.choice.Size, InCart: p.inCart(s.Affiliate),
			}
			if item.Method == MethodAmazon && !item.InCart {
				item.Method = MethodProductPage
			}
			if p.variant != nil {
				item.VariantID, item.VariantTitle = p.variant.ID, p.variant.Title
				item.Size = ""
			} else {
				item.SizeAtCheckout = sizedCategories[p.item.Category] && !(p.class.Method == MethodAmazon && p.class.Variation)
			}
			if amount, currency, ok := p.price(); ok {
				item.Price = &Money{Amount: float64(amount) / 100, Currency: currency}
				sums.add(currency, amount*int64(p.choice.Quantity))
			}
			if p.note != "" {
				st.Warnings = append(st.Warnings, p.item.Label+": "+p.note)
			}
			if item.InCart {
				switch p.class.Method {
				case MethodShopify:
					cartLines = append(cartLines, CartLine{VariantID: p.variant.ID, Quantity: p.choice.Quantity})
				case MethodAmazon:
					amazonItems = append(amazonItems, AmazonCartItem{ASIN: p.class.ASIN, Quantity: p.choice.Quantity})
				}
			}
			st.Items = append(st.Items, item)
		}

		switch {
		case len(cartLines) > 0:
			st.Method = MethodShopify
			st.CheckoutURL = ShopifyCartURL(group[0].class.Host, mergeLines(cartLines), prefill(req.Email, rt.address), s.ShopPay)
			st.Prefilled = rt.address != nil
		case len(amazonItems) > 0:
			st.Method = MethodAmazon
			st.CheckoutURL, _ = s.Affiliate.AmazonCartURL(group[0].class.AmazonTLD, amazonItems)
		default:
			first := st.Items[0]
			st.Method = first.Method
			if st.Method == MethodShopify {
				st.Method = MethodProductPage
			}
			st.CheckoutURL = first.OpenURL
		}

		st.Subtotals = sums.money()
		finalCountry := ""
		if req.Deliver != nil {
			finalCountry = req.Deliver.FinalCountry
		}
		for _, sub := range sums.order {
			st.Warnings = append(st.Warnings, customsWarnings(merchant, sf, finalCountry, sums.minor[sub], sub)...)
		}
		st.Warnings = slices.Compact(st.Warnings)
		totals.merge(sums)
		plan.Stores = append(plan.Stores, st)
	}
	plan.Totals = totals.money()
	if req.Deliver == nil {
		plan.Warnings = append(plan.Warnings, "Add a delivery address to have it filled in at checkout.")
	}
	if len(plan.Totals) > 1 {
		plan.Warnings = append(plan.Warnings, "Prices are in more than one currency, so totals are shown per currency.")
	}
	return plan
}

// mergeLines combines lines for the same variant.
func mergeLines(lines []CartLine) []CartLine {
	var out []CartLine
	index := map[int64]int{}
	for _, l := range lines {
		if i, ok := index[l.VariantID]; ok {
			out[i].Quantity += l.Quantity
			continue
		}
		index[l.VariantID] = len(out)
		out = append(out, l)
	}
	return out
}

// sums adds prices per currency in minor units, keeping first-seen order.
type sums struct {
	order []string
	minor map[string]int64
}

func newSums() *sums { return &sums{minor: map[string]int64{}} }

func (s *sums) add(currency string, amount int64) {
	if _, ok := s.minor[currency]; !ok {
		s.order = append(s.order, currency)
	}
	s.minor[currency] += amount
}

func (s *sums) merge(o *sums) {
	for _, c := range o.order {
		s.add(c, o.minor[c])
	}
}

func (s *sums) money() []Money {
	out := make([]Money, 0, len(s.order))
	for _, c := range s.order {
		out = append(out, Money{Amount: float64(s.minor[c]) / 100, Currency: c})
	}
	return out
}

func orDefault(v, fallback string) string {
	if strings.TrimSpace(v) != "" {
		return v
	}
	return fallback
}
