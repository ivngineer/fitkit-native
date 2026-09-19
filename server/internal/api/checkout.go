package api

import (
	"encoding/json"
	"errors"
	"net/http"
	"net/url"
	"regexp"
	"slices"
	"strings"
	"time"
	"unicode"
	"unicode/utf8"

	"fitkit/server/internal/analyze"
	"fitkit/server/internal/shop"
	"fitkit/server/internal/store"
)

// Checkout never moves money: the server builds links to each store's own
// checkout and records what the user says they ordered. Everything the links
// are built from (listings, prices, variants) comes from the stored analysis
// or the store itself, never from the request.

const (
	maxAddresses    = 20
	maxOptionURLs   = 30
	maxSizeEntries  = 20
	maxFieldLen     = 100
	maxLabelLen     = 40
	maxOrderRefLen  = 64
	maxTrackingLen  = 40
	maxCarrierLen   = 32
	maxSizeValueLen = 16
)

func (s *Server) checkoutRoutes(mux *http.ServeMux) {
	mux.HandleFunc("GET /v1/addresses", s.authed(s.listAddresses))
	mux.HandleFunc("POST /v1/addresses", s.authed(s.createAddress))
	mux.HandleFunc("PUT /v1/addresses/{id}", s.authed(s.updateAddress))
	mux.HandleFunc("DELETE /v1/addresses/{id}", s.authed(s.deleteAddress))

	mux.HandleFunc("GET /v1/sizes", s.authed(s.getSizes))
	mux.HandleFunc("PUT /v1/sizes", s.authed(s.putSizes))

	mux.HandleFunc("POST /v1/listings/options", s.authed(s.userLimited(s.listingOptions)))

	mux.HandleFunc("POST /v1/looks", s.authed(s.userLimited(s.createLook)))
	mux.HandleFunc("GET /v1/looks", s.authed(s.listLooks))
	mux.HandleFunc("GET /v1/looks/{id}", s.authed(s.getLook))
	mux.HandleFunc("PUT /v1/looks/{id}/stores/{merchant}", s.authed(s.updateLookStore))
}

// userLimited rate limits per user, for endpoints that reach out to stores.
func (s *Server) userLimited(next func(http.ResponseWriter, *http.Request, session)) func(http.ResponseWriter, *http.Request, session) {
	return func(w http.ResponseWriter, r *http.Request, sess session) {
		if !s.limiter.allow("user:" + sess.user.ID + r.URL.Path) {
			writeError(w, http.StatusTooManyRequests, "rate_limited", "Too many requests. Wait a minute and try again.")
			return
		}
		next(w, r, sess)
	}
}

// cleanText trims a field and rejects control characters and overlong values.
func cleanText(v string, maxLen int) (string, bool) {
	v = strings.TrimSpace(v)
	if utf8.RuneCountInString(v) > maxLen || !utf8.ValidString(v) || strings.IndexFunc(v, unicode.IsControl) >= 0 {
		return "", false
	}
	return v, true
}

var countryCode = regexp.MustCompile(`^[A-Z]{2}$`)

// ---- addresses ----

type addressJSON struct {
	ID           string              `json:"id"`
	Kind         string              `json:"kind"`
	Label        string              `json:"label"`
	Country      string              `json:"country"`
	Forwarder    string              `json:"forwarder"`
	FinalCountry string              `json:"finalCountry"`
	Fields       store.AddressFields `json:"fields"`
	IsDefault    bool                `json:"isDefault"`
	CreatedAt    time.Time           `json:"createdAt"`
}

func toAddressJSON(a store.Address) addressJSON {
	return addressJSON{
		ID: a.ID, Kind: a.Kind, Label: a.Label, Country: a.Country, Forwarder: a.Forwarder,
		FinalCountry: a.FinalCountry, Fields: a.Fields, IsDefault: a.IsDefault, CreatedAt: a.CreatedAt,
	}
}

// validateAddress normalizes an address from the app. It returns a message
// for the user when something's missing or malformed.
func validateAddress(in addressJSON) (store.Address, string) {
	a := store.Address{Kind: in.Kind, Forwarder: in.Forwarder, IsDefault: in.IsDefault}
	if !slices.Contains([]string{shop.KindHome, shop.KindNPBranch, shop.KindForwarder}, a.Kind) {
		return a, "Choose what kind of address this is."
	}
	var ok bool
	if a.Label, ok = cleanText(in.Label, maxLabelLen); !ok {
		return a, "The label is too long."
	}
	a.Country = strings.ToUpper(strings.TrimSpace(in.Country))
	if !countryCode.MatchString(a.Country) {
		return a, "Choose a country."
	}

	f := in.Fields
	for _, field := range []*string{&f.FirstName, &f.LastName, &f.Line1, &f.Line2, &f.City, &f.Region, &f.Zip, &f.SuiteID, &f.NPBranch} {
		if *field, ok = cleanText(*field, maxFieldLen); !ok {
			return a, "One of the fields is too long or has characters that aren't allowed."
		}
	}
	if f.Phone, ok = cleanText(f.Phone, 32); !ok || strings.ContainsFunc(f.Phone, func(r rune) bool {
		return !unicode.IsDigit(r) && !strings.ContainsRune("+-() ", r)
	}) {
		return a, "The phone number doesn't look right."
	}
	a.Fields = f

	missing := func(values ...string) bool { return slices.Contains(values, "") }
	if missing(f.FirstName, f.LastName) {
		return a, "Add the recipient's first and last name."
	}
	switch a.Kind {
	case shop.KindNPBranch:
		if a.Country != "UA" {
			return a, "Nova Poshta branches are in Ukraine."
		}
		if missing(f.City, f.NPBranch, f.Phone) {
			return a, "Add the city, branch number and phone number."
		}
	default:
		if missing(f.Line1, f.City, f.Zip) {
			return a, "Add the street address, city and postal code."
		}
	}
	if a.Kind == shop.KindForwarder {
		if !slices.Contains(shop.Forwarders, a.Forwarder) {
			return a, "Choose the forwarding service."
		}
		if f.SuiteID == "" {
			return a, "Add your personal suite or customer ID from the forwarder."
		}
		a.FinalCountry = strings.ToUpper(strings.TrimSpace(in.FinalCountry))
		if a.FinalCountry == "" {
			a.FinalCountry = "UA"
		}
		if !countryCode.MatchString(a.FinalCountry) || a.FinalCountry == a.Country {
			return a, "Choose the country the forwarder delivers to."
		}
	} else {
		a.Forwarder = ""
		a.FinalCountry = a.Country
	}
	return a, ""
}

func (s *Server) listAddresses(w http.ResponseWriter, r *http.Request, sess session) {
	list, err := s.Store.Addresses(r.Context(), sess.user.ID)
	if err != nil {
		s.internal(w, r, err)
		return
	}
	out := make([]addressJSON, len(list))
	for i, a := range list {
		out[i] = toAddressJSON(a)
	}
	writeJSON(w, http.StatusOK, map[string]any{"addresses": out})
}

func (s *Server) createAddress(w http.ResponseWriter, r *http.Request, sess session) {
	var body addressJSON
	if !decode(w, r, &body) {
		return
	}
	a, problem := validateAddress(body)
	if problem != "" {
		writeError(w, http.StatusUnprocessableEntity, "invalid_address", problem)
		return
	}
	existing, err := s.Store.Addresses(r.Context(), sess.user.ID)
	if err != nil {
		s.internal(w, r, err)
		return
	}
	if len(existing) >= maxAddresses {
		writeError(w, http.StatusUnprocessableEntity, "too_many_addresses", "You can save up to 20 addresses. Delete one first.")
		return
	}
	a.UserID = sess.user.ID
	if err := s.Store.CreateAddress(r.Context(), &a); err != nil {
		s.internal(w, r, err)
		return
	}
	writeJSON(w, http.StatusCreated, toAddressJSON(a))
}

func (s *Server) updateAddress(w http.ResponseWriter, r *http.Request, sess session) {
	var body addressJSON
	if !decode(w, r, &body) {
		return
	}
	a, problem := validateAddress(body)
	if problem != "" {
		writeError(w, http.StatusUnprocessableEntity, "invalid_address", problem)
		return
	}
	a.ID, a.UserID = r.PathValue("id"), sess.user.ID
	err := s.Store.UpdateAddress(r.Context(), &a)
	if errors.Is(err, store.ErrNotFound) {
		writeError(w, http.StatusNotFound, "not_found", "Address not found.")
		return
	}
	if err != nil {
		s.internal(w, r, err)
		return
	}
	updated, err := s.Store.AddressByID(r.Context(), sess.user.ID, a.ID)
	if err != nil {
		s.internal(w, r, err)
		return
	}
	writeJSON(w, http.StatusOK, toAddressJSON(updated))
}

func (s *Server) deleteAddress(w http.ResponseWriter, r *http.Request, sess session) {
	err := s.Store.DeleteAddress(r.Context(), sess.user.ID, r.PathValue("id"))
	if errors.Is(err, store.ErrNotFound) {
		writeError(w, http.StatusNotFound, "not_found", "Address not found.")
		return
	}
	if err != nil {
		s.internal(w, r, err)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

// ---- sizes ----

// SizeCategories are the detected item categories a size can be saved for.
var SizeCategories = []string{"top", "bottom", "dress", "outerwear", "shoes", "hat", "accessory", "bag", "jewelry", "eyewear", "other"}

func (s *Server) getSizes(w http.ResponseWriter, r *http.Request, sess session) {
	sizes, err := s.Store.Sizes(r.Context(), sess.user.ID)
	if err != nil {
		s.internal(w, r, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"sizes": sizes})
}

func (s *Server) putSizes(w http.ResponseWriter, r *http.Request, sess session) {
	var body struct {
		Sizes map[string]string `json:"sizes"`
	}
	if !decode(w, r, &body) {
		return
	}
	if len(body.Sizes) > maxSizeEntries {
		writeError(w, http.StatusUnprocessableEntity, "invalid_sizes", "Too many sizes.")
		return
	}
	clean := map[string]string{}
	for category, size := range body.Sizes {
		if !slices.Contains(SizeCategories, category) {
			writeError(w, http.StatusUnprocessableEntity, "invalid_sizes", "Unknown clothing category.")
			return
		}
		v, ok := cleanText(size, maxSizeValueLen)
		if !ok {
			writeError(w, http.StatusUnprocessableEntity, "invalid_sizes", "A size is too long.")
			return
		}
		if v != "" {
			clean[category] = v
		}
	}
	if err := s.Store.SetSizes(r.Context(), sess.user.ID, clean); err != nil {
		s.internal(w, r, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"sizes": clean})
}

// ---- listing options ----

// analyzedItems returns a saved pin's finished analysis. It writes the
// error response and returns false when there's none.
func (s *Server) analyzedItems(w http.ResponseWriter, r *http.Request, sess session, pinID string) ([]analyze.Item, bool) {
	pin, err := s.Store.PinByID(r.Context(), pinID)
	saved := false
	if err == nil {
		saved, err = s.Store.IsPinSaved(r.Context(), sess.user.ID, pin.ID)
	}
	if errors.Is(err, store.ErrNotFound) || (err == nil && !saved) {
		writeError(w, http.StatusNotFound, "not_found", "Pin not found.")
		return nil, false
	}
	if err != nil {
		s.internal(w, r, err)
		return nil, false
	}
	st, err := s.Analysis.Get(r.Context(), pin.ID)
	if err != nil && !errors.Is(err, store.ErrNotFound) {
		s.internal(w, r, err)
		return nil, false
	}
	if err != nil || st.Result == nil {
		writeError(w, http.StatusConflict, "not_analyzed", "Find this look's pieces first.")
		return nil, false
	}
	return st.Result.Items, true
}

func (s *Server) listingOptions(w http.ResponseWriter, r *http.Request, sess session) {
	var body struct {
		PinID string   `json:"pinId"`
		URLs  []string `json:"urls"`
	}
	if !decode(w, r, &body) {
		return
	}
	if len(body.URLs) == 0 || len(body.URLs) > maxOptionURLs {
		writeError(w, http.StatusUnprocessableEntity, "invalid_urls", "Send between 1 and 30 listing links.")
		return
	}
	items, ok := s.analyzedItems(w, r, sess, body.PinID)
	if !ok {
		return
	}
	// Only listings from this pin's analysis are looked up, so the endpoint
	// can't be used to make the server fetch arbitrary URLs.
	known := map[string]bool{}
	for _, it := range items {
		for _, l := range it.Listings {
			known[l.URL] = true
		}
	}
	urls := slices.Compact(slices.Sorted(slices.Values(body.URLs)))
	for _, u := range urls {
		if !known[u] {
			writeError(w, http.StatusUnprocessableEntity, "unknown_listing", "One of the listings isn't part of this look. Refresh and try again.")
			return
		}
	}
	options, err := s.Shop.Options(r.Context(), urls)
	if err != nil {
		s.internal(w, r, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"options": options})
}

// ---- looks ----

var lookStatuses = []string{"pending", "ordered", "shipped", "delivered", "skipped"}

type lookStoreJSON struct {
	Merchant    string    `json:"merchant"`
	Status      string    `json:"status"`
	OrderRef    string    `json:"orderRef"`
	TrackingNo  string    `json:"trackingNo"`
	Carrier     string    `json:"carrier"`
	TrackingURL *string   `json:"trackingUrl"`
	UpdatedAt   time.Time `json:"updatedAt"`
}

type lookJSON struct {
	ID          string          `json:"id"`
	PinID       string          `json:"pinId"`
	PinTitle    string          `json:"pinTitle"`
	PinImageURL string          `json:"pinImageUrl"`
	Plan        json.RawMessage `json:"plan"`
	Stores      []lookStoreJSON `json:"stores"`
	CreatedAt   time.Time       `json:"createdAt"`
}

func toLookStoreJSON(ls store.LookStore) lookStoreJSON {
	return lookStoreJSON{
		Merchant: ls.Merchant, Status: ls.Status, OrderRef: ls.OrderRef, TrackingNo: ls.TrackingNo,
		Carrier: ls.Carrier, TrackingURL: trackingURL(ls.Carrier, ls.TrackingNo), UpdatedAt: ls.UpdatedAt,
	}
}

func (s *Server) toLookJSON(r *http.Request, l store.Look) lookJSON {
	out := lookJSON{ID: l.ID, PinID: l.PinID, Plan: json.RawMessage(l.PlanJSON), CreatedAt: l.CreatedAt, Stores: []lookStoreJSON{}}
	if pin, err := s.Store.PinByID(r.Context(), l.PinID); err == nil {
		out.PinTitle, out.PinImageURL = pin.Title, "/media/pins/"+pin.ImageFile
	}
	for _, ls := range l.Stores {
		out.Stores = append(out.Stores, toLookStoreJSON(ls))
	}
	return out
}

var carrierTracking = map[string]string{
	"usps":        "https://tools.usps.com/go/TrackConfirmAction?tLabels=",
	"ups":         "https://www.ups.com/track?tracknum=",
	"fedex":       "https://www.fedex.com/fedextrack/?trknbr=",
	"dhl":         "https://www.dhl.com/global-en/home/tracking/tracking-express.html?tracking-id=",
	"nova_poshta": "https://novaposhta.ua/tracking/?cargo_number=",
	"meest":       "https://t.meest-group.com/",
	"ukrposhta":   "https://track.ukrposhta.ua/tracking_UA.html?barcode=",
	"inpost":      "https://inpost.pl/sledzenie-przesylek?number=",
	"royal_mail":  "https://www.royalmail.com/track-your-item#/tracking-results/",
}

var trackingNumber = regexp.MustCompile(`^[A-Za-z0-9-]{4,40}$`)

// trackingURL links to the carrier's tracking page, or a tracking aggregator
// when the carrier isn't known.
func trackingURL(carrier, number string) *string {
	number = strings.ReplaceAll(number, " ", "")
	if !trackingNumber.MatchString(number) {
		return nil
	}
	base, ok := carrierTracking[carrier]
	if !ok {
		base = "https://parcelsapp.com/en/tracking/"
	}
	u := base + url.PathEscape(number)
	return &u
}

func (s *Server) createLook(w http.ResponseWriter, r *http.Request, sess session) {
	var body struct {
		PinID     string        `json:"pinId"`
		AddressID string        `json:"addressId"`
		Items     []shop.Choice `json:"items"`
	}
	if !decode(w, r, &body) {
		return
	}
	items, ok := s.analyzedItems(w, r, sess, body.PinID)
	if !ok {
		return
	}
	book, err := s.Store.Addresses(r.Context(), sess.user.ID)
	if err != nil {
		s.internal(w, r, err)
		return
	}
	var deliver *store.Address
	if body.AddressID != "" {
		for i := range book {
			if book[i].ID == body.AddressID {
				deliver = &book[i]
			}
		}
		if deliver == nil {
			writeError(w, http.StatusUnprocessableEntity, "unknown_address", "That address was removed. Pick another.")
			return
		}
	}

	plan, err := s.Shop.Build(r.Context(), shop.PlanRequest{
		Items: items, Choices: body.Items, Email: sess.user.Email, Deliver: deliver, Addresses: book,
	})
	var inputErr *shop.InputError
	if errors.As(err, &inputErr) {
		writeJSON(w, http.StatusUnprocessableEntity, map[string]any{"error": map[string]string{
			"code": inputErr.Code, "message": inputErr.Message, "itemId": inputErr.ItemID,
		}})
		return
	}
	if err != nil {
		s.internal(w, r, err)
		return
	}
	planJSON, err := json.Marshal(plan)
	if err != nil {
		s.internal(w, r, err)
		return
	}
	look := store.Look{UserID: sess.user.ID, PinID: body.PinID, PlanJSON: string(planJSON)}
	for _, st := range plan.Stores {
		look.Stores = append(look.Stores, store.LookStore{Merchant: st.Merchant, Status: "pending"})
	}
	if err := s.Store.CreateLook(r.Context(), &look); err != nil {
		s.internal(w, r, err)
		return
	}
	writeJSON(w, http.StatusCreated, s.toLookJSON(r, look))
}

func (s *Server) listLooks(w http.ResponseWriter, r *http.Request, sess session) {
	looks, err := s.Store.Looks(r.Context(), sess.user.ID, 100)
	if err != nil {
		s.internal(w, r, err)
		return
	}
	out := make([]lookJSON, len(looks))
	for i, l := range looks {
		out[i] = s.toLookJSON(r, l)
	}
	writeJSON(w, http.StatusOK, map[string]any{"looks": out})
}

func (s *Server) getLook(w http.ResponseWriter, r *http.Request, sess session) {
	look, err := s.Store.LookByID(r.Context(), sess.user.ID, r.PathValue("id"))
	if errors.Is(err, store.ErrNotFound) {
		writeError(w, http.StatusNotFound, "not_found", "Order not found.")
		return
	}
	if err != nil {
		s.internal(w, r, err)
		return
	}
	writeJSON(w, http.StatusOK, s.toLookJSON(r, look))
}

func (s *Server) updateLookStore(w http.ResponseWriter, r *http.Request, sess session) {
	var body struct {
		Status     string  `json:"status"`
		OrderRef   *string `json:"orderRef"`
		TrackingNo *string `json:"trackingNo"`
		Carrier    *string `json:"carrier"`
	}
	if !decode(w, r, &body) {
		return
	}
	if !slices.Contains(lookStatuses, body.Status) {
		writeError(w, http.StatusUnprocessableEntity, "invalid_status", "Unknown order status.")
		return
	}
	u := store.LookStoreUpdate{Status: body.Status}
	for _, f := range []struct {
		in     *string
		out    **string
		maxLen int
	}{{body.OrderRef, &u.OrderRef, maxOrderRefLen}, {body.TrackingNo, &u.TrackingNo, maxTrackingLen}, {body.Carrier, &u.Carrier, maxCarrierLen}} {
		if f.in == nil {
			continue
		}
		v, ok := cleanText(*f.in, f.maxLen)
		if !ok {
			writeError(w, http.StatusUnprocessableEntity, "invalid_order_details", "The order or tracking number is too long.")
			return
		}
		*f.out = &v
	}
	if u.TrackingNo != nil && *u.TrackingNo != "" && trackingURL("", *u.TrackingNo) == nil {
		writeError(w, http.StatusUnprocessableEntity, "invalid_tracking", "Tracking numbers use letters, digits and dashes.")
		return
	}
	if u.Carrier != nil {
		c := strings.ToLower(strings.ReplaceAll(*u.Carrier, " ", "_"))
		u.Carrier = &c
	}
	ls, err := s.Store.UpdateLookStore(r.Context(), sess.user.ID, r.PathValue("id"), r.PathValue("merchant"), u)
	if errors.Is(err, store.ErrNotFound) {
		writeError(w, http.StatusNotFound, "not_found", "Order not found.")
		return
	}
	if err != nil {
		s.internal(w, r, err)
		return
	}
	writeJSON(w, http.StatusOK, toLookStoreJSON(ls))
}
