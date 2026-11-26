// Package httpapi exposes the service over HTTP (JSON) and Server-Sent Events.
package httpapi

import (
	"context"
	"crypto/hmac"
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"errors"
	"io"
	"log/slog"
	"net/http"
	"strings"
	"time"

	"golang.org/x/crypto/bcrypt"

	"fitkit/server/internal/workspace/domain"
	"fitkit/server/internal/workspace/service"
	"fitkit/server/internal/workspace/store"
)

const sessionCookie = "fitkit_session"
const sessionTTL = 30 * 24 * time.Hour

type Config struct {
	Service *service.Service
	Hub     *Hub
	Log     *slog.Logger
	// Shared secrets for HMAC-signed webhooks (header X-Fitkit-Signature: sha256=<hex>).
	ClientAppSecret string
	TrackerSecret   string
	// Allowed browser origin for CORS (empty = same origin only, e.g. behind the Vite proxy).
	AllowOrigin  string
	SecureCookie bool
	// WebDir is the built front-end (web/dist). When set, the server serves it and falls back to index.html for app routes.
	WebDir string
}

type server struct {
	Config
	throttle *throttle
}

// secure reports whether cookies must carry the Secure flag: forced by config, or the request came over HTTPS (directly or via a proxy).
func (s *server) secure(r *http.Request) bool {
	return s.SecureCookie || r.TLS != nil || r.Header.Get("X-Forwarded-Proto") == "https"
}

func New(cfg Config) http.Handler {
	s := &server{Config: cfg, throttle: newThrottle(10, 15*time.Minute)}
	mux := http.NewServeMux()
	mux.HandleFunc("GET /healthz", func(w http.ResponseWriter, r *http.Request) { writeJSON(w, 200, map[string]string{"status": "ok"}) })

	mux.HandleFunc("POST /api/auth/login", s.login)
	mux.HandleFunc("POST /api/auth/logout", s.logout)

	auth := func(pattern string, h func(http.ResponseWriter, *http.Request, domain.Employee)) {
		mux.HandleFunc(pattern, s.authed(h))
	}
	auth("GET /api/me", func(w http.ResponseWriter, r *http.Request, me domain.Employee) { writeJSON(w, 200, me) })
	auth("GET /api/bootstrap", s.bootstrap)
	auth("GET /api/events", func(w http.ResponseWriter, r *http.Request, _ domain.Employee) { s.Hub.serve(w, r) })

	auth("GET /api/orders", s.listOrders)
	auth("GET /api/orders/{id}", s.getOrder)
	auth("POST /api/orders/take-next", s.takeNext)
	auth("POST /api/orders/{id}/take", s.take)
	auth("POST /api/orders/{id}/status", s.setStatus)
	auth("PATCH /api/orders/{id}/items/{itemId}", s.itemAction)
	auth("PUT /api/orders/{id}/problem", s.setProblem)
	auth("POST /api/orders/{id}/notes", s.addNote)
	auth("POST /api/orders/{id}/cancel", s.cancel)
	auth("POST /api/orders/{id}/ship", s.ship)

	auth("GET /api/clients", s.listClients)
	auth("GET /api/clients/{id}", s.getClient)

	auth("GET /api/shipments", s.listShipments)
	auth("POST /api/shipments/{id}/tracking", s.setTracking)
	auth("POST /api/shipments/{id}/arrived", s.arrived)

	auth("GET /api/tickets", s.listTickets)
	auth("POST /api/tickets/{id}/messages", s.reply)
	auth("POST /api/tickets/{id}/resolve", s.resolve)

	mux.HandleFunc("POST /webhooks/client-app/orders", s.signed(s.ClientAppSecret, s.hookOrder))
	mux.HandleFunc("POST /webhooks/client-app/confirmations", s.signed(s.ClientAppSecret, s.hookConfirmation))
	mux.HandleFunc("POST /webhooks/client-app/messages", s.signed(s.ClientAppSecret, s.hookMessage))
	mux.HandleFunc("POST /webhooks/tracking", s.signed(s.TrackerSecret, s.hookTracking))

	if cfg.WebDir != "" {
		mux.Handle("GET /", spa(cfg.WebDir))
	}

	return s.recoverer(s.logger(securityHeaders(s.cors(mux))))
}

// ---------- middleware

func (s *server) logger(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		start := time.Now()
		sw := &statusWriter{ResponseWriter: w, code: 200}
		next.ServeHTTP(sw, r)
		if r.URL.Path != "/api/events" {
			s.Log.Info("http", "method", r.Method, "path", r.URL.Path, "status", sw.code, "ms", time.Since(start).Milliseconds())
		}
	})
}

type statusWriter struct {
	http.ResponseWriter
	code int
}

func (w *statusWriter) WriteHeader(c int) { w.code = c; w.ResponseWriter.WriteHeader(c) }
func (w *statusWriter) Flush() {
	if f, ok := w.ResponseWriter.(http.Flusher); ok {
		f.Flush()
	}
}

func (s *server) recoverer(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		defer func() {
			if v := recover(); v != nil {
				s.Log.Error("panic", "path", r.URL.Path, "value", v)
				writeErr(w, http.StatusInternalServerError, "Внутрішня помилка")
			}
		}()
		next.ServeHTTP(w, r)
	})
}

func (s *server) cors(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if s.AllowOrigin != "" && r.Header.Get("Origin") == s.AllowOrigin {
			h := w.Header()
			h.Set("Access-Control-Allow-Origin", s.AllowOrigin)
			h.Set("Access-Control-Allow-Credentials", "true")
			h.Set("Access-Control-Allow-Headers", "Content-Type, Authorization")
			h.Set("Access-Control-Allow-Methods", "GET, POST, PUT, PATCH, DELETE, OPTIONS")
			h.Set("Vary", "Origin")
			if r.Method == http.MethodOptions {
				w.WriteHeader(http.StatusNoContent)
				return
			}
		}
		next.ServeHTTP(w, r)
	})
}

func tokenFrom(r *http.Request) string {
	if h := r.Header.Get("Authorization"); strings.HasPrefix(h, "Bearer ") {
		return strings.TrimPrefix(h, "Bearer ")
	}
	if c, err := r.Cookie(sessionCookie); err == nil {
		return c.Value
	}
	return ""
}

func hashToken(t string) []byte {
	h := sha256.Sum256([]byte(t))
	return h[:]
}

func (s *server) authed(h func(http.ResponseWriter, *http.Request, domain.Employee)) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		tok := tokenFrom(r)
		if tok == "" {
			writeErr(w, http.StatusUnauthorized, "Увійдіть у систему")
			return
		}
		me, err := s.Service.Store.Q().SessionEmployee(r.Context(), hashToken(tok))
		if err != nil {
			writeErr(w, http.StatusUnauthorized, "Сесія завершилась — увійдіть знову")
			return
		}
		h(w, r, me)
	}
}

// signed verifies X-Fitkit-Signature: sha256=hex(HMAC-SHA256(secret, body)).
func (s *server) signed(secret string, h func(http.ResponseWriter, *http.Request, []byte)) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		body, err := io.ReadAll(http.MaxBytesReader(w, r.Body, 1<<20))
		if err != nil {
			writeErr(w, http.StatusBadRequest, "body too large")
			return
		}
		if secret == "" {
			writeErr(w, http.StatusServiceUnavailable, "webhook secret is not configured")
			return
		}
		mac := hmac.New(sha256.New, []byte(secret))
		mac.Write(body)
		want := "sha256=" + hex.EncodeToString(mac.Sum(nil))
		if !hmac.Equal([]byte(want), []byte(r.Header.Get("X-Fitkit-Signature"))) {
			writeErr(w, http.StatusUnauthorized, "bad signature")
			return
		}
		h(w, r, body)
	}
}

// ---------- helpers

func writeJSON(w http.ResponseWriter, code int, v any) {
	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	w.WriteHeader(code)
	_ = json.NewEncoder(w).Encode(v)
}

func writeErr(w http.ResponseWriter, code int, msg string) {
	writeJSON(w, code, map[string]string{"error": msg})
}

func (s *server) fail(w http.ResponseWriter, err error) {
	var se *service.Error
	switch {
	case errors.As(err, &se):
		writeErr(w, se.Code, se.Msg)
	case errors.Is(err, store.ErrNotFound):
		writeErr(w, http.StatusNotFound, "Не знайдено")
	default:
		s.Log.Error("request failed", "err", err)
		writeErr(w, http.StatusInternalServerError, "Внутрішня помилка")
	}
}

func decode(w http.ResponseWriter, r *http.Request, v any) bool {
	dec := json.NewDecoder(http.MaxBytesReader(w, r.Body, 1<<20))
	dec.DisallowUnknownFields()
	if err := dec.Decode(v); err != nil {
		writeErr(w, http.StatusBadRequest, "Некоректний JSON: "+err.Error())
		return false
	}
	return true
}

// ---------- auth

func (s *server) login(w http.ResponseWriter, r *http.Request) {
	var in struct {
		Email    string `json:"email"`
		Password string `json:"password"`
	}
	if !decode(w, r, &in) {
		return
	}
	ctx := r.Context()
	email := strings.ToLower(strings.TrimSpace(in.Email))
	key := clientIP(r) + "|" + email
	if !s.throttle.allow(key) {
		w.Header().Set("Retry-After", "900")
		writeErr(w, http.StatusTooManyRequests, "Забагато спроб входу — спробуйте за 15 хвилин")
		return
	}
	e, err := s.Service.Store.Q().EmployeeByEmail(ctx, email)
	if err != nil || bcrypt.CompareHashAndPassword([]byte(e.PasswordHash), []byte(in.Password)) != nil {
		writeErr(w, http.StatusUnauthorized, "Невірна пошта або пароль")
		return
	}
	buf := make([]byte, 32)
	_, _ = rand.Read(buf)
	tok := base64.RawURLEncoding.EncodeToString(buf)
	exp := time.Now().Add(sessionTTL)
	if err := s.Service.Store.Q().CreateSession(ctx, hashToken(tok), e.ID, exp); err != nil {
		s.fail(w, err)
		return
	}
	s.throttle.reset(key)
	http.SetCookie(w, &http.Cookie{Name: sessionCookie, Value: tok, Path: "/", Expires: exp, HttpOnly: true, Secure: s.secure(r), SameSite: http.SameSiteLaxMode})
	writeJSON(w, 200, map[string]any{"token": tok, "me": e.Employee})
}

func (s *server) logout(w http.ResponseWriter, r *http.Request) {
	if tok := tokenFrom(r); tok != "" {
		_ = s.Service.Store.Q().DeleteSession(r.Context(), hashToken(tok))
	}
	http.SetCookie(w, &http.Cookie{Name: sessionCookie, Value: "", Path: "/", MaxAge: -1, HttpOnly: true, Secure: s.secure(r), SameSite: http.SameSiteLaxMode})
	w.WriteHeader(http.StatusNoContent)
}

// ---------- reads

func (s *server) bootstrap(w http.ResponseWriter, r *http.Request, me domain.Employee) {
	snap, err := s.Service.Bootstrap(r.Context(), me)
	if err != nil {
		s.fail(w, err)
		return
	}
	writeJSON(w, 200, snap)
}

func (s *server) listOrders(w http.ResponseWriter, r *http.Request, me domain.Employee) {
	f := store.OrderFilter{ClientID: r.URL.Query().Get("client")}
	if st := r.URL.Query().Get("status"); st != "" {
		for _, x := range strings.Split(st, ",") {
			if !domain.OrderStatus(x).Valid() {
				writeErr(w, http.StatusBadRequest, "Невідомий статус: "+x)
				return
			}
			f.Statuses = append(f.Statuses, domain.OrderStatus(x))
		}
	}
	if r.URL.Query().Get("owner") == "me" {
		f.OwnerID = me.ID
	}
	list, err := s.Service.Store.Q().Orders(r.Context(), f)
	if err != nil {
		s.fail(w, err)
		return
	}
	views := make([]service.OrderView, len(list))
	for i, o := range list {
		views[i] = service.View(o)
	}
	writeJSON(w, 200, views)
}

func (s *server) getOrder(w http.ResponseWriter, r *http.Request, _ domain.Employee) {
	v, err := s.Service.Order(r.Context(), r.PathValue("id"))
	if err != nil {
		s.fail(w, err)
		return
	}
	writeJSON(w, 200, v)
}

func (s *server) respondOrder(w http.ResponseWriter, ctx context.Context, id string, err error) {
	if err != nil {
		s.fail(w, err)
		return
	}
	v, err := s.Service.Order(ctx, id)
	if err != nil {
		s.fail(w, err)
		return
	}
	writeJSON(w, 200, v)
}

func (s *server) listClients(w http.ResponseWriter, r *http.Request, _ domain.Employee) {
	list, err := s.Service.Store.Q().Clients(r.Context())
	if err != nil {
		s.fail(w, err)
		return
	}
	writeJSON(w, 200, list)
}

func (s *server) getClient(w http.ResponseWriter, r *http.Request, _ domain.Employee) {
	c, err := s.Service.Store.Q().Client(r.Context(), r.PathValue("id"))
	if err != nil {
		s.fail(w, err)
		return
	}
	writeJSON(w, 200, c)
}

func (s *server) listShipments(w http.ResponseWriter, r *http.Request, _ domain.Employee) {
	list, err := s.Service.Store.Q().Shipments(r.Context(), r.URL.Query().Get("order"))
	if err != nil {
		s.fail(w, err)
		return
	}
	writeJSON(w, 200, list)
}

func (s *server) listTickets(w http.ResponseWriter, r *http.Request, _ domain.Employee) {
	st := r.URL.Query().Get("status")
	if st != "" && st != "open" && st != "resolved" {
		writeErr(w, http.StatusBadRequest, "status: open | resolved")
		return
	}
	list, err := s.Service.Store.Q().Tickets(r.Context(), st)
	if err != nil {
		s.fail(w, err)
		return
	}
	writeJSON(w, 200, list)
}

// ---------- order actions

func (s *server) takeNext(w http.ResponseWriter, r *http.Request, me domain.Employee) {
	id, err := s.Service.TakeNext(r.Context(), me)
	s.respondOrder(w, r.Context(), id, err)
}

func (s *server) take(w http.ResponseWriter, r *http.Request, me domain.Employee) {
	id := r.PathValue("id")
	s.respondOrder(w, r.Context(), id, s.Service.Take(r.Context(), me, id))
}

func (s *server) setStatus(w http.ResponseWriter, r *http.Request, me domain.Employee) {
	var in struct {
		Status domain.OrderStatus `json:"status"`
	}
	if !decode(w, r, &in) {
		return
	}
	id := r.PathValue("id")
	s.respondOrder(w, r.Context(), id, s.Service.Move(r.Context(), me, id, in.Status))
}

func (s *server) itemAction(w http.ResponseWriter, r *http.Request, me domain.Employee) {
	var in struct {
		Action string        `json:"action"`
		Price  *domain.Money `json:"price"`
	}
	if !decode(w, r, &in) {
		return
	}
	id := r.PathValue("id")
	s.respondOrder(w, r.Context(), id, s.Service.Item(r.Context(), me, id, r.PathValue("itemId"), in.Action, in.Price))
}

func (s *server) setProblem(w http.ResponseWriter, r *http.Request, me domain.Employee) {
	var in struct {
		Problem *string `json:"problem"`
	}
	if !decode(w, r, &in) {
		return
	}
	id := r.PathValue("id")
	s.respondOrder(w, r.Context(), id, s.Service.SetProblem(r.Context(), me, id, in.Problem))
}

func (s *server) addNote(w http.ResponseWriter, r *http.Request, me domain.Employee) {
	var in struct {
		Text string `json:"text"`
	}
	if !decode(w, r, &in) {
		return
	}
	n, err := s.Service.AddNote(r.Context(), me, r.PathValue("id"), in.Text)
	if err != nil {
		s.fail(w, err)
		return
	}
	writeJSON(w, http.StatusCreated, n)
}

func (s *server) cancel(w http.ResponseWriter, r *http.Request, me domain.Employee) {
	id := r.PathValue("id")
	s.respondOrder(w, r.Context(), id, s.Service.Cancel(r.Context(), me, id))
}

func (s *server) ship(w http.ResponseWriter, r *http.Request, me domain.Employee) {
	id := r.PathValue("id")
	s.respondOrder(w, r.Context(), id, s.Service.Ship(r.Context(), me, id))
}

// ---------- shipments & tickets

func (s *server) setTracking(w http.ResponseWriter, r *http.Request, me domain.Employee) {
	var in struct {
		Carrier  string `json:"carrier"`
		Tracking string `json:"tracking"`
	}
	if !decode(w, r, &in) {
		return
	}
	if err := s.Service.SetTracking(r.Context(), me, r.PathValue("id"), in.Carrier, in.Tracking); err != nil {
		s.fail(w, err)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

func (s *server) arrived(w http.ResponseWriter, r *http.Request, me domain.Employee) {
	if err := s.Service.Arrived(r.Context(), me, r.PathValue("id")); err != nil {
		s.fail(w, err)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

func (s *server) reply(w http.ResponseWriter, r *http.Request, me domain.Employee) {
	var in struct {
		Text string `json:"text"`
	}
	if !decode(w, r, &in) {
		return
	}
	m, err := s.Service.Reply(r.Context(), me, r.PathValue("id"), in.Text)
	if err != nil {
		s.fail(w, err)
		return
	}
	writeJSON(w, http.StatusCreated, m)
}

func (s *server) resolve(w http.ResponseWriter, r *http.Request, me domain.Employee) {
	if err := s.Service.Resolve(r.Context(), me, r.PathValue("id")); err != nil {
		s.fail(w, err)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

// ---------- webhooks

func unmarshal(w http.ResponseWriter, body []byte, v any) bool {
	if err := json.Unmarshal(body, v); err != nil {
		writeErr(w, http.StatusBadRequest, "invalid JSON: "+err.Error())
		return false
	}
	return true
}

func (s *server) hookOrder(w http.ResponseWriter, r *http.Request, body []byte) {
	var in service.IncomingOrder
	if !unmarshal(w, body, &in) {
		return
	}
	id, number, err := s.Service.IngestOrder(r.Context(), in)
	if err != nil {
		s.fail(w, err)
		return
	}
	writeJSON(w, 200, map[string]string{"id": id, "number": number})
}

func (s *server) hookConfirmation(w http.ResponseWriter, r *http.Request, body []byte) {
	var in service.IncomingConfirmation
	if !unmarshal(w, body, &in) {
		return
	}
	if err := s.Service.ClientConfirmation(r.Context(), in); err != nil {
		s.fail(w, err)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

func (s *server) hookMessage(w http.ResponseWriter, r *http.Request, body []byte) {
	var in service.IncomingMessage
	if !unmarshal(w, body, &in) {
		return
	}
	id, err := s.Service.ClientMessage(r.Context(), in)
	if err != nil {
		s.fail(w, err)
		return
	}
	writeJSON(w, 200, map[string]string{"ticketId": id})
}

func (s *server) hookTracking(w http.ResponseWriter, r *http.Request, body []byte) {
	var in service.TrackingUpdate
	if !unmarshal(w, body, &in) {
		return
	}
	if err := s.Service.Tracking(r.Context(), in); err != nil {
		s.fail(w, err)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}
