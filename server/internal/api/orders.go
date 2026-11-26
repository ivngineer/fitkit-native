package api

import (
	"errors"
	"math"
	"net/http"
	"net/url"
	"slices"
	"strings"
	"time"

	"fitkit/server/internal/analyze"
	wsdomain "fitkit/server/internal/workspace/domain"
	wsservice "fitkit/server/internal/workspace/service"
	wsstore "fitkit/server/internal/workspace/store"
)

// Buying through Fitkit: “Купити” in the app sends the pieces the user picked;
// the server stores them as a new request in the employee workspace (where a
// buyer purchases them) and returns the order number. Delivery and payment are
// not part of this yet. As with looks, names, links, prices and photos come from
// the pin's stored analysis, never from the request.

const (
	maxOrderItems   = 20
	maxOrderQty     = 10
	maxNameLen      = 60
	maxCommentLen   = 300
	maxIdemKeyLen   = 64
	noSize          = "—"
	clientIDPrefix  = "user:"
	orderIDPrefix   = "app:"
	uahCurrencyCode = "UAH"
)

func (s *Server) orderRoutes(mux *http.ServeMux) {
	mux.HandleFunc("POST /v1/orders", s.authed(s.userLimited(s.createOrder)))
	mux.HandleFunc("GET /v1/orders", s.authed(s.listOrders))
	mux.HandleFunc("GET /v1/orders/{id}", s.authed(s.getOrder))
}

type orderItemRequest struct {
	ItemID     string `json:"itemId"`
	ListingURL string `json:"listingUrl"`
	Size       string `json:"size"`
	Quantity   int    `json:"quantity"`
	Comment    string `json:"comment"`
}

type orderItemJSON struct {
	Name         string `json:"name"`
	Store        string `json:"store"`
	Size         string `json:"size"`
	Quantity     int    `json:"quantity"`
	PhotoURL     string `json:"photoUrl"`
	Price        *int64 `json:"price"`        // kopiyky
	ListingPrice string `json:"listingPrice"` // store price text when there is no UAH price
	Cancelled    bool   `json:"cancelled"`
}

type orderJSON struct {
	ID        string          `json:"id"`
	Number    string          `json:"number"`
	Status    string          `json:"status"` // what the client sees, e.g. “Замовлення підтверджено”
	PhotoURL  string          `json:"photoUrl"`
	Total     *int64          `json:"total"` // kopiyky, when every price is known
	Items     []orderItemJSON `json:"items"`
	CreatedAt time.Time       `json:"createdAt"`
}

func toOrderJSON(o wsdomain.Order) orderJSON {
	out := orderJSON{ID: o.ID, Number: o.Number, Status: wsdomain.ClientStatus(o), CreatedAt: o.CreatedAt, Total: o.Paid, Items: []orderItemJSON{}}
	if o.Photo != nil {
		out.PhotoURL = *o.Photo
	}
	for _, it := range o.Items {
		j := orderItemJSON{Name: it.Name, Store: it.Store, Size: it.Size, Quantity: it.Qty, PhotoURL: it.Photo, Price: it.Price, Cancelled: it.Status == wsdomain.ItemCancelled}
		if it.ListingPrice != nil {
			j.ListingPrice = *it.ListingPrice
		}
		out.Items = append(out.Items, j)
	}
	return out
}

// uahPrice converts a listing price to kopiyky when it is in hryvnias.
func uahPrice(l analyze.Listing) *int64 {
	cur := strings.ToUpper(strings.TrimSpace(l.Currency))
	isUAH := cur == uahCurrencyCode || cur == "₴" || cur == "ГРН" || (cur == "" && (strings.Contains(l.Price, "₴") || strings.Contains(strings.ToLower(l.Price), "грн")))
	if !isUAH || l.PriceValue == nil || *l.PriceValue <= 0 {
		return nil
	}
	v := int64(math.Round(*l.PriceValue * 100))
	return &v
}

func merchantName(l analyze.Listing) string {
	if m := strings.TrimSpace(l.Merchant); m != "" {
		return m
	}
	if u, err := url.Parse(l.URL); err == nil && u.Host != "" {
		return strings.TrimPrefix(u.Host, "www.")
	}
	return "Магазин"
}

func (s *Server) createOrder(w http.ResponseWriter, r *http.Request, sess session) {
	if s.Workspace == nil {
		writeError(w, http.StatusServiceUnavailable, "orders_unavailable", "Ordering isn't available right now.")
		return
	}
	var body struct {
		PinID          string             `json:"pinId"`
		Name           string             `json:"name"`
		Items          []orderItemRequest `json:"items"`
		IdempotencyKey string             `json:"idempotencyKey"`
	}
	if !decode(w, r, &body) {
		return
	}
	name, ok := cleanText(body.Name, maxNameLen)
	if !ok || name == "" {
		writeError(w, http.StatusUnprocessableEntity, "invalid_name", "Add your name or nickname (up to 60 characters).")
		return
	}
	if len(body.Items) == 0 || len(body.Items) > maxOrderItems {
		writeError(w, http.StatusUnprocessableEntity, "invalid_items", "Pick between 1 and 20 pieces.")
		return
	}
	key, ok := cleanText(body.IdempotencyKey, maxIdemKeyLen)
	if !ok {
		writeError(w, http.StatusUnprocessableEntity, "invalid_idempotency_key", "The idempotency key is too long.")
		return
	}
	analyzed, ok := s.analyzedItems(w, r, sess, body.PinID)
	if !ok {
		return
	}
	pin, err := s.Store.PinByID(r.Context(), body.PinID)
	if err != nil {
		s.internal(w, r, err)
		return
	}
	sizes, err := s.Store.Sizes(r.Context(), sess.user.ID)
	if err != nil {
		s.internal(w, r, err)
		return
	}

	byID := map[string]analyze.Item{}
	for _, it := range analyzed {
		byID[it.ID] = it
	}
	items := make([]wsservice.IncomingItem, 0, len(body.Items))
	var total int64
	allPriced := true
	for _, req := range body.Items {
		it, found := byID[req.ItemID]
		var listing *analyze.Listing
		if found {
			for i := range it.Listings {
				if it.Listings[i].URL == req.ListingURL {
					listing = &it.Listings[i]
				}
			}
		}
		if listing == nil {
			writeJSON(w, http.StatusUnprocessableEntity, map[string]any{"error": map[string]string{
				"code": "unknown_listing", "message": "One of the pieces isn't part of this look. Refresh and try again.", "itemId": req.ItemID,
			}})
			return
		}
		qty := req.Quantity
		if qty == 0 {
			qty = 1
		}
		if qty < 1 || qty > maxOrderQty {
			writeError(w, http.StatusUnprocessableEntity, "invalid_quantity", "Quantity must be between 1 and 10.")
			return
		}
		size, ok := cleanText(req.Size, maxSizeValueLen)
		if !ok {
			writeError(w, http.StatusUnprocessableEntity, "invalid_size", "A size is too long.")
			return
		}
		if size == "" {
			size = sizes[it.Category]
		}
		if size == "" {
			size = noSize
		}
		comment, ok := cleanText(req.Comment, maxCommentLen)
		if !ok {
			writeError(w, http.StatusUnprocessableEntity, "invalid_comment", "The comment is too long.")
			return
		}
		photo := it.CropURL
		if !strings.HasPrefix(photo, "/media/") && listing.ThumbnailURL != "" {
			photo = listing.ThumbnailURL // hosted crops expire; the store thumbnail doesn't
		}
		in := wsservice.IncomingItem{Photo: photo, Name: it.Label, Store: merchantName(*listing), URL: listing.URL, Size: size, Qty: qty, Price: uahPrice(*listing)}
		if in.Name == "" {
			in.Name = listing.Title
		}
		if comment != "" {
			in.Comment = &comment
		}
		if in.Price == nil {
			allPriced = false
			if p := strings.TrimSpace(listing.Price); p != "" {
				in.ListingPrice = &p
			}
		} else {
			total += *in.Price * int64(qty)
		}
		items = append(items, in)
	}

	if key == "" {
		key = wsstore.NewID()
	}
	order := wsservice.IncomingOrder{
		ExternalID: orderIDPrefix + sess.user.ID + ":" + key,
		Client:     wsservice.IncomingClient{ExternalID: clientIDPrefix + sess.user.ID, Name: name},
		Items:      items,
	}
	if allPriced {
		order.Paid = &total
	}
	if pin.ImageFile != "" {
		photo := "/media/pins/" + pin.ImageFile
		order.Photo = &photo
	}
	id, _, err := s.Workspace.IngestOrder(r.Context(), order)
	var se *wsservice.Error
	if errors.As(err, &se) {
		writeError(w, se.Code, "order_rejected", se.Msg)
		return
	}
	if err != nil {
		s.internal(w, r, err)
		return
	}
	o, err := s.Workspace.Store.Q().Order(r.Context(), id, false)
	if err != nil {
		s.internal(w, r, err)
		return
	}
	writeJSON(w, http.StatusCreated, toOrderJSON(o))
}

// userOrders returns the orders of the signed-in user, newest first.
func (s *Server) userOrders(r *http.Request, sess session) ([]wsdomain.Order, error) {
	q := s.Workspace.Store.Q()
	cid, err := q.ClientIDByExternal(r.Context(), clientIDPrefix+sess.user.ID)
	if errors.Is(err, wsstore.ErrNotFound) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	list, err := q.Orders(r.Context(), wsstore.OrderFilter{ClientID: cid})
	slices.SortFunc(list, func(a, b wsdomain.Order) int { return b.CreatedAt.Compare(a.CreatedAt) })
	return list, err
}

func (s *Server) listOrders(w http.ResponseWriter, r *http.Request, sess session) {
	if s.Workspace == nil {
		writeJSON(w, http.StatusOK, map[string]any{"orders": []orderJSON{}})
		return
	}
	list, err := s.userOrders(r, sess)
	if err != nil {
		s.internal(w, r, err)
		return
	}
	out := make([]orderJSON, len(list))
	for i, o := range list {
		out[i] = toOrderJSON(o)
	}
	writeJSON(w, http.StatusOK, map[string]any{"orders": out})
}

func (s *Server) getOrder(w http.ResponseWriter, r *http.Request, sess session) {
	if s.Workspace != nil {
		list, err := s.userOrders(r, sess)
		if err != nil {
			s.internal(w, r, err)
			return
		}
		for _, o := range list {
			if o.ID == r.PathValue("id") {
				writeJSON(w, http.StatusOK, toOrderJSON(o))
				return
			}
		}
	}
	writeError(w, http.StatusNotFound, "not_found", "Order not found.")
}
