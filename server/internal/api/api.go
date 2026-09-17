// Package api exposes Fitkit's JSON HTTP API.
package api

import (
	"encoding/json"
	"errors"
	"log/slog"
	"net"
	"net/http"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"time"

	"fitkit/server/internal/analyze"
	"fitkit/server/internal/auth"
	"fitkit/server/internal/importer"
	"fitkit/server/internal/pinterest"
	"fitkit/server/internal/store"
)

type Server struct {
	Store      *store.Store
	Importer   *importer.Importer
	Analysis   *analyze.Service
	MediaDir   string
	SessionTTL time.Duration
	Log        *slog.Logger

	limiter *rateLimiter
}

func (s *Server) Handler() http.Handler {
	s.limiter = &rateLimiter{window: time.Minute, max: 20, hits: map[string][]time.Time{}}
	mux := http.NewServeMux()
	mux.HandleFunc("GET /healthz", func(w http.ResponseWriter, r *http.Request) { writeJSON(w, 200, map[string]bool{"ok": true}) })

	mux.HandleFunc("POST /v1/auth/signup", s.limited(s.signup))
	mux.HandleFunc("POST /v1/auth/login", s.limited(s.login))
	mux.HandleFunc("POST /v1/auth/logout", s.authed(s.logout))

	mux.HandleFunc("GET /v1/me", s.authed(s.me))
	mux.HandleFunc("DELETE /v1/me", s.authed(s.deleteAccount))
	mux.HandleFunc("PUT /v1/me/referral-source", s.authed(s.setReferral))
	mux.HandleFunc("PUT /v1/me/pinterest", s.authed(s.setPinterest))

	mux.HandleFunc("POST /v1/imports", s.authed(s.startImport))
	mux.HandleFunc("GET /v1/imports/latest", s.authed(s.latestImport))
	mux.HandleFunc("GET /v1/imports/{id}", s.authed(s.getImport))

	mux.HandleFunc("GET /v1/pins", s.authed(s.listPins))
	mux.HandleFunc("PUT /v1/pins/{id}/hidden", s.authed(s.hidePin(true)))
	mux.HandleFunc("DELETE /v1/pins/{id}/hidden", s.authed(s.hidePin(false)))
	mux.HandleFunc("GET /v1/cart", s.authed(s.listCart))
	mux.HandleFunc("PUT /v1/cart/{id}", s.authed(s.addToCart))
	mux.HandleFunc("DELETE /v1/cart/{id}", s.authed(s.removeFromCart))

	mux.HandleFunc("GET /v1/pins/{id}/analysis", s.authed(s.getAnalysis))
	mux.HandleFunc("POST /v1/pins/{id}/analysis", s.authed(s.startAnalysis))

	mux.Handle("GET /media/", http.StripPrefix("/media/", noDirListing(http.FileServer(http.Dir(s.MediaDir)))))
	return s.logRequests(mux)
}

// ---- helpers ----

type apiError struct {
	Code    string `json:"code"`
	Message string `json:"message"`
}

func writeJSON(w http.ResponseWriter, status int, v any) {
	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(v)
}

func writeError(w http.ResponseWriter, status int, code, message string) {
	writeJSON(w, status, map[string]apiError{"error": {Code: code, Message: message}})
}

func decode(w http.ResponseWriter, r *http.Request, v any) bool {
	dec := json.NewDecoder(http.MaxBytesReader(w, r.Body, 1<<20))
	if err := dec.Decode(v); err != nil {
		writeError(w, http.StatusBadRequest, "invalid_json", "Request body must be valid JSON.")
		return false
	}
	return true
}

func (s *Server) internal(w http.ResponseWriter, r *http.Request, err error) {
	s.Log.Error("internal error", "path", r.URL.Path, "err", err)
	writeError(w, http.StatusInternalServerError, "internal", "Something went wrong. Try again.")
}

type ctxKey struct{}

type session struct {
	user      store.User
	tokenHash string
}

func (s *Server) authed(next func(http.ResponseWriter, *http.Request, session)) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		token, ok := strings.CutPrefix(r.Header.Get("Authorization"), "Bearer ")
		if !ok || token == "" {
			writeError(w, http.StatusUnauthorized, "unauthorized", "Sign in to continue.")
			return
		}
		hash := auth.HashToken(token)
		user, err := s.Store.UserBySession(r.Context(), hash)
		if errors.Is(err, store.ErrNotFound) {
			writeError(w, http.StatusUnauthorized, "unauthorized", "Your session expired. Sign in again.")
			return
		}
		if err != nil {
			s.internal(w, r, err)
			return
		}
		next(w, r, session{user: user, tokenHash: hash})
	}
}

func (s *Server) limited(next http.HandlerFunc) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		host, _, err := net.SplitHostPort(r.RemoteAddr)
		if err != nil {
			host = r.RemoteAddr
		}
		if !s.limiter.allow(host + r.URL.Path) {
			writeError(w, http.StatusTooManyRequests, "rate_limited", "Too many attempts. Wait a minute and try again.")
			return
		}
		next(w, r)
	}
}

type rateLimiter struct {
	mu     sync.Mutex
	window time.Duration
	max    int
	hits   map[string][]time.Time
}

func (l *rateLimiter) allow(key string) bool {
	l.mu.Lock()
	defer l.mu.Unlock()
	now := time.Now()
	recent := l.hits[key][:0]
	for _, t := range l.hits[key] {
		if now.Sub(t) < l.window {
			recent = append(recent, t)
		}
	}
	if len(recent) >= l.max {
		l.hits[key] = recent
		return false
	}
	l.hits[key] = append(recent, now)
	return true
}

func noDirListing(h http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "" || strings.HasSuffix(r.URL.Path, "/") {
			http.NotFound(w, r)
			return
		}
		w.Header().Set("Cache-Control", "public, max-age=31536000, immutable")
		h.ServeHTTP(w, r)
	})
}

type statusRecorder struct {
	http.ResponseWriter
	status int
}

func (r *statusRecorder) WriteHeader(code int) {
	r.status = code
	r.ResponseWriter.WriteHeader(code)
}

func (s *Server) logRequests(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		start := time.Now()
		rec := &statusRecorder{ResponseWriter: w, status: 200}
		next.ServeHTTP(rec, r)
		if !strings.HasPrefix(r.URL.Path, "/media/") {
			s.Log.Info("request", "method", r.Method, "path", r.URL.Path, "status", rec.status, "dur", time.Since(start).Round(time.Millisecond))
		}
	})
}

// ---- auth ----

type credentials struct {
	Email    string `json:"email"`
	Password string `json:"password"`
}

type userJSON struct {
	ID                string    `json:"id"`
	Email             string    `json:"email"`
	ReferralSource    *string   `json:"referralSource"`
	PinterestUsername *string   `json:"pinterestUsername"`
	CreatedAt         time.Time `json:"createdAt"`
}

func toUserJSON(u store.User) userJSON {
	j := userJSON{ID: u.ID, Email: u.Email, CreatedAt: u.CreatedAt}
	if u.ReferralSource != "" {
		j.ReferralSource = &u.ReferralSource
	}
	if u.PinterestUsername != "" {
		j.PinterestUsername = &u.PinterestUsername
	}
	return j
}

func (s *Server) issueSession(w http.ResponseWriter, r *http.Request, status int, u store.User) {
	token, hash := auth.NewToken()
	if err := s.Store.CreateSession(r.Context(), hash, u.ID, s.SessionTTL); err != nil {
		s.internal(w, r, err)
		return
	}
	writeJSON(w, status, map[string]any{"token": token, "user": toUserJSON(u)})
}

func (s *Server) signup(w http.ResponseWriter, r *http.Request) {
	var c credentials
	if !decode(w, r, &c) {
		return
	}
	email, err := auth.NormalizeEmail(c.Email)
	if err != nil {
		writeError(w, http.StatusUnprocessableEntity, "invalid_email", err.Error())
		return
	}
	hash, err := auth.HashPassword(c.Password)
	if err != nil {
		writeError(w, http.StatusUnprocessableEntity, "invalid_password", err.Error())
		return
	}
	u, err := s.Store.CreateUser(r.Context(), email, hash)
	if errors.Is(err, store.ErrConflict) {
		writeError(w, http.StatusConflict, "email_taken", "An account with this email already exists. Sign in instead.")
		return
	}
	if err != nil {
		s.internal(w, r, err)
		return
	}
	s.issueSession(w, r, http.StatusCreated, u)
}

func (s *Server) login(w http.ResponseWriter, r *http.Request) {
	var c credentials
	if !decode(w, r, &c) {
		return
	}
	email, _ := auth.NormalizeEmail(c.Email)
	u, err := s.Store.UserByEmail(r.Context(), email)
	if err != nil && !errors.Is(err, store.ErrNotFound) {
		s.internal(w, r, err)
		return
	}
	if err != nil {
		auth.BurnPasswordCheck(c.Password)
	}
	if err != nil || !auth.CheckPassword(u.PasswordHash, c.Password) {
		writeError(w, http.StatusUnauthorized, "invalid_credentials", "Email or password is incorrect.")
		return
	}
	s.issueSession(w, r, http.StatusOK, u)
}

func (s *Server) logout(w http.ResponseWriter, r *http.Request, sess session) {
	if err := s.Store.DeleteSession(r.Context(), sess.tokenHash); err != nil {
		s.internal(w, r, err)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

// ---- account & onboarding ----

func (s *Server) me(w http.ResponseWriter, r *http.Request, sess session) {
	writeJSON(w, http.StatusOK, toUserJSON(sess.user))
}

func (s *Server) deleteAccount(w http.ResponseWriter, r *http.Request, sess session) {
	if err := s.Store.DeleteUser(r.Context(), sess.user.ID); err != nil {
		s.internal(w, r, err)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

var ReferralSources = []string{"instagram", "tiktok", "pinterest", "youtube", "friend", "app_store", "search", "other"}

func (s *Server) setReferral(w http.ResponseWriter, r *http.Request, sess session) {
	var body struct {
		Source string `json:"source"`
	}
	if !decode(w, r, &body) {
		return
	}
	source := strings.ToLower(strings.TrimSpace(body.Source))
	valid := false
	for _, v := range ReferralSources {
		valid = valid || v == source
	}
	if !valid {
		writeError(w, http.StatusUnprocessableEntity, "invalid_source", "Pick one of the listed options.")
		return
	}
	if err := s.Store.SetReferralSource(r.Context(), sess.user.ID, source); err != nil {
		s.internal(w, r, err)
		return
	}
	sess.user.ReferralSource = source
	writeJSON(w, http.StatusOK, toUserJSON(sess.user))
}

func (s *Server) setPinterest(w http.ResponseWriter, r *http.Request, sess session) {
	var body struct {
		Handle string `json:"handle"`
	}
	if !decode(w, r, &body) {
		return
	}
	username, err := pinterest.NormalizeHandle(body.Handle)
	if err != nil {
		writeError(w, http.StatusUnprocessableEntity, "invalid_handle", err.Error())
		return
	}
	if err := s.Store.SetPinterestUsername(r.Context(), sess.user.ID, username); err != nil {
		s.internal(w, r, err)
		return
	}
	sess.user.PinterestUsername = username
	writeJSON(w, http.StatusOK, toUserJSON(sess.user))
}

// ---- imports ----

type importJSON struct {
	ID                string     `json:"id"`
	Status            string     `json:"status"`
	PinterestUsername string     `json:"pinterestUsername"`
	Discovered        int        `json:"discovered"`
	Imported          int        `json:"imported"`
	Reused            int        `json:"reused"`
	Failed            int        `json:"failed"`
	Error             *jobError  `json:"error"`
	CreatedAt         time.Time  `json:"createdAt"`
	UpdatedAt         time.Time  `json:"updatedAt"`
	FinishedAt        *time.Time `json:"finishedAt"`
}

type jobError struct {
	Code      string `json:"code"`
	Message   string `json:"message"`
	Retryable bool   `json:"retryable"`
}

func toImportJSON(j store.ImportJob) importJSON {
	out := importJSON{
		ID: j.ID, Status: string(j.Status), PinterestUsername: j.PinterestUsername,
		Discovered: j.Discovered, Imported: j.Imported, Reused: j.Reused, Failed: j.Failed,
		CreatedAt: j.CreatedAt, UpdatedAt: j.UpdatedAt, FinishedAt: j.FinishedAt,
	}
	if j.ErrorCode != "" {
		out.Error = &jobError{Code: j.ErrorCode, Message: j.ErrorMessage, Retryable: j.Retryable}
	}
	return out
}

func (s *Server) startImport(w http.ResponseWriter, r *http.Request, sess session) {
	job, err := s.Importer.Enqueue(r.Context(), sess.user)
	if errors.Is(err, importer.ErrNoPinterestHandle) {
		writeError(w, http.StatusUnprocessableEntity, "missing_pinterest_handle", err.Error())
		return
	}
	if err != nil {
		s.internal(w, r, err)
		return
	}
	writeJSON(w, http.StatusAccepted, toImportJSON(job))
}

func (s *Server) latestImport(w http.ResponseWriter, r *http.Request, sess session) {
	job, err := s.Store.LatestImportJob(r.Context(), sess.user.ID)
	if errors.Is(err, store.ErrNotFound) {
		writeError(w, http.StatusNotFound, "not_found", "No imports yet.")
		return
	}
	if err != nil {
		s.internal(w, r, err)
		return
	}
	writeJSON(w, http.StatusOK, toImportJSON(job))
}

func (s *Server) getImport(w http.ResponseWriter, r *http.Request, sess session) {
	job, err := s.Store.ImportJob(r.Context(), r.PathValue("id"))
	if errors.Is(err, store.ErrNotFound) || (err == nil && job.UserID != sess.user.ID) {
		writeError(w, http.StatusNotFound, "not_found", "Import not found.")
		return
	}
	if err != nil {
		s.internal(w, r, err)
		return
	}
	writeJSON(w, http.StatusOK, toImportJSON(job))
}

// ---- pins ----

type pinJSON struct {
	ID            string    `json:"id"`
	PinterestID   string    `json:"pinterestId"`
	Title         string    `json:"title"`
	Description   string    `json:"description"`
	Link          string    `json:"link"`
	PinterestURL  string    `json:"pinterestUrl"`
	ImageURL      string    `json:"imageUrl"`
	Width         int       `json:"width"`
	Height        int       `json:"height"`
	DominantColor string    `json:"dominantColor"`
	SavedAt       time.Time `json:"savedAt"`
}

func pinsJSON(pins []store.Pin) []pinJSON {
	out := make([]pinJSON, len(pins))
	for i, p := range pins {
		out[i] = pinJSON{
			ID: p.ID, PinterestID: p.PinterestID, Title: p.Title, Description: p.Description, Link: p.Link,
			PinterestURL: "https://www.pinterest.com/pin/" + p.PinterestID + "/",
			ImageURL:     "/media/pins/" + p.ImageFile, Width: p.Width, Height: p.Height,
			DominantColor: p.DominantColor, SavedAt: p.SavedAt,
		}
	}
	return out
}

func (s *Server) listPins(w http.ResponseWriter, r *http.Request, sess session) {
	limit, _ := strconv.Atoi(r.URL.Query().Get("limit"))
	if limit <= 0 || limit > 100 {
		limit = 50
	}
	before, _ := strconv.ParseInt(r.URL.Query().Get("cursor"), 10, 64)
	pins, err := s.Store.SavedPins(r.Context(), sess.user.ID, before, limit)
	if err != nil {
		s.internal(w, r, err)
		return
	}
	out := pinsJSON(pins)
	var next *string
	if len(pins) == limit {
		c := strconv.FormatInt(pins[len(pins)-1].SavedAt.UnixMilli(), 10)
		next = &c
	}
	writeJSON(w, http.StatusOK, map[string]any{"pins": out, "nextCursor": next})
}

// hidePin removes a pin from the user's grid without deleting it.
func (s *Server) hidePin(hidden bool) func(http.ResponseWriter, *http.Request, session) {
	return func(w http.ResponseWriter, r *http.Request, sess session) {
		err := s.Store.SetPinHidden(r.Context(), sess.user.ID, r.PathValue("id"), hidden)
		if errors.Is(err, store.ErrNotFound) {
			writeError(w, http.StatusNotFound, "not_found", "Pin not found.")
			return
		}
		if err != nil {
			s.internal(w, r, err)
			return
		}
		w.WriteHeader(http.StatusNoContent)
	}
}

// listCart returns the pins the user put aside to buy, newest first. The
// shape matches GET /v1/pins so the app reuses one decoder.
func (s *Server) listCart(w http.ResponseWriter, r *http.Request, sess session) {
	pins, err := s.Store.CartPins(r.Context(), sess.user.ID)
	if err != nil {
		s.internal(w, r, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"pins": pinsJSON(pins), "nextCursor": nil})
}

func (s *Server) addToCart(w http.ResponseWriter, r *http.Request, sess session) {
	err := s.Store.AddToCart(r.Context(), sess.user.ID, r.PathValue("id"))
	if errors.Is(err, store.ErrNotFound) {
		writeError(w, http.StatusNotFound, "not_found", "Pin not found.")
		return
	}
	if err != nil {
		s.internal(w, r, err)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

func (s *Server) removeFromCart(w http.ResponseWriter, r *http.Request, sess session) {
	if err := s.Store.RemoveFromCart(r.Context(), sess.user.ID, r.PathValue("id")); err != nil {
		s.internal(w, r, err)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

func (s *Server) savedPin(w http.ResponseWriter, r *http.Request, sess session) (store.Pin, bool) {
	pin, err := s.Store.PinByID(r.Context(), r.PathValue("id"))
	saved := false
	if err == nil {
		saved, err = s.Store.IsPinSaved(r.Context(), sess.user.ID, pin.ID)
	}
	if errors.Is(err, store.ErrNotFound) || (err == nil && !saved) {
		writeError(w, http.StatusNotFound, "not_found", "Pin not found.")
		return store.Pin{}, false
	}
	if err != nil {
		s.internal(w, r, err)
		return store.Pin{}, false
	}
	return pin, true
}

func writeAnalysis(w http.ResponseWriter, status int, pinID string, st analyze.Status) {
	body := map[string]any{"pinId": pinID, "status": st.Status, "result": st.Result, "error": nil}
	if st.ErrorCode != "" {
		body["error"] = apiError{Code: st.ErrorCode, Message: st.ErrorMessage}
	}
	writeJSON(w, status, body)
}

func (s *Server) getAnalysis(w http.ResponseWriter, r *http.Request, sess session) {
	pin, ok := s.savedPin(w, r, sess)
	if !ok {
		return
	}
	st, err := s.Analysis.Get(r.Context(), pin.ID)
	if errors.Is(err, store.ErrNotFound) {
		writeAnalysis(w, http.StatusOK, pin.ID, analyze.Status{Status: "none"})
		return
	}
	if err != nil {
		s.internal(w, r, err)
		return
	}
	writeAnalysis(w, http.StatusOK, pin.ID, st)
}

func (s *Server) startAnalysis(w http.ResponseWriter, r *http.Request, sess session) {
	pin, ok := s.savedPin(w, r, sess)
	if !ok {
		return
	}
	force := r.URL.Query().Get("force") == "true"
	st, err := s.Analysis.Start(r.Context(), pin, force)
	if err != nil {
		s.internal(w, r, err)
		return
	}
	writeAnalysis(w, http.StatusAccepted, pin.ID, st)
}

// EnsureMediaDirs creates the directories served under /media.
func EnsureMediaDirs(mediaDir string) error {
	for _, d := range []string{"pins", "crops"} {
		if err := os.MkdirAll(filepath.Join(mediaDir, d), 0o755); err != nil {
			return err
		}
	}
	return nil
}
