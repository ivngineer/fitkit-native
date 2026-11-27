package httpapi

import (
	"crypto/rand"
	"crypto/subtle"
	"encoding/base64"
	"log/slog"
	"net/http"
	"strings"
	"time"

	"fitkit/server/internal/workspace/service"
)

const adminCookie = "fitkit_admin"
const adminSessionTTL = 12 * time.Hour

// AdminConfig configures the admin panel: its API under /admin/api and, when WebDir
// is set, its front-end at /admin/.
type AdminConfig struct {
	Service *service.Service
	Log     *slog.Logger
	// Admins maps login → password; a fixed list from the environment (no sign-up).
	Admins       map[string]string
	SecureCookie bool
	WebDir       string
}

type admin struct {
	AdminConfig
	throttle *throttle
}

// NewAdmin returns the handler for everything under /admin/.
func NewAdmin(cfg AdminConfig) http.Handler {
	a := &admin{AdminConfig: cfg, throttle: newThrottle(10, 15*time.Minute)}
	mux := http.NewServeMux()
	mux.HandleFunc("POST /admin/api/auth/login", a.login)
	mux.HandleFunc("POST /admin/api/auth/logout", a.logout)
	mux.HandleFunc("GET /admin/api/me", a.authed(func(w http.ResponseWriter, r *http.Request, login string) {
		writeJSON(w, 200, map[string]string{"login": login})
	}))
	mux.HandleFunc("GET /admin/api/analytics", a.authed(a.analytics))
	mux.HandleFunc("GET /admin/api/employees", a.authed(a.listEmployees))
	mux.HandleFunc("POST /admin/api/employees", a.authed(a.createEmployee))
	mux.HandleFunc("GET /admin/api/employees/{id}", a.authed(a.getEmployee))
	mux.HandleFunc("PATCH /admin/api/employees/{id}", a.authed(a.updateEmployee))
	mux.HandleFunc("PUT /admin/api/employees/{id}/password", a.authed(a.setPassword))
	mux.HandleFunc("DELETE /admin/api/employees/{id}", a.authed(a.deleteEmployee))
	if cfg.WebDir != "" {
		mux.Handle("GET /admin/", http.StripPrefix("/admin", spa(cfg.WebDir)))
		mux.Handle("GET /admin", http.RedirectHandler("/admin/", http.StatusMovedPermanently))
	}
	srv := &server{Config: Config{Log: cfg.Log}}
	return srv.recoverer(srv.logger(securityHeaders(mux)))
}

func (a *admin) secure(r *http.Request) bool {
	return a.SecureCookie || r.TLS != nil || r.Header.Get("X-Forwarded-Proto") == "https"
}

func (a *admin) fail(w http.ResponseWriter, err error) {
	(&server{Config: Config{Log: a.Log}}).fail(w, err)
}

// checkAdmin compares in constant time and always walks the whole list.
func (a *admin) checkAdmin(login, password string) bool {
	ok := false
	for l, p := range a.Admins {
		lm := subtle.ConstantTimeCompare([]byte(l), []byte(login))
		pm := subtle.ConstantTimeCompare([]byte(p), []byte(password))
		if lm&pm == 1 {
			ok = true
		}
	}
	return ok
}

func (a *admin) login(w http.ResponseWriter, r *http.Request) {
	if len(a.Admins) == 0 {
		writeErr(w, http.StatusServiceUnavailable, "Адмінів не налаштовано: задайте FITKIT_ADMINS на сервері")
		return
	}
	var in struct {
		Login    string `json:"login"`
		Password string `json:"password"`
	}
	if !decode(w, r, &in) {
		return
	}
	login := strings.TrimSpace(in.Login)
	key := clientIP(r) + "|admin|" + strings.ToLower(login)
	if !a.throttle.allow(key) {
		w.Header().Set("Retry-After", "900")
		writeErr(w, http.StatusTooManyRequests, "Забагато спроб входу — спробуйте за 15 хвилин")
		return
	}
	if !a.checkAdmin(login, in.Password) {
		writeErr(w, http.StatusUnauthorized, "Невірний логін або пароль")
		return
	}
	buf := make([]byte, 32)
	_, _ = rand.Read(buf)
	tok := base64.RawURLEncoding.EncodeToString(buf)
	exp := time.Now().Add(adminSessionTTL)
	if err := a.Service.Store.Q().CreateAdminSession(r.Context(), hashToken(tok), login, exp); err != nil {
		a.fail(w, err)
		return
	}
	a.throttle.reset(key)
	http.SetCookie(w, &http.Cookie{Name: adminCookie, Value: tok, Path: "/admin", Expires: exp, HttpOnly: true, Secure: a.secure(r), SameSite: http.SameSiteStrictMode})
	writeJSON(w, 200, map[string]string{"login": login, "token": tok})
}

func (a *admin) token(r *http.Request) string {
	if h := r.Header.Get("Authorization"); strings.HasPrefix(h, "Bearer ") {
		return strings.TrimPrefix(h, "Bearer ")
	}
	if c, err := r.Cookie(adminCookie); err == nil {
		return c.Value
	}
	return ""
}

func (a *admin) logout(w http.ResponseWriter, r *http.Request) {
	if tok := a.token(r); tok != "" {
		_ = a.Service.Store.Q().DeleteAdminSession(r.Context(), hashToken(tok))
	}
	http.SetCookie(w, &http.Cookie{Name: adminCookie, Value: "", Path: "/admin", MaxAge: -1, HttpOnly: true, Secure: a.secure(r), SameSite: http.SameSiteStrictMode})
	w.WriteHeader(http.StatusNoContent)
}

func (a *admin) authed(h func(http.ResponseWriter, *http.Request, string)) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		tok := a.token(r)
		if tok == "" {
			writeErr(w, http.StatusUnauthorized, "Увійдіть в адмінку")
			return
		}
		login, err := a.Service.Store.Q().AdminSession(r.Context(), hashToken(tok))
		if err != nil {
			writeErr(w, http.StatusUnauthorized, "Сесія завершилась — увійдіть знову")
			return
		}
		// A login removed from FITKIT_ADMINS loses access at once.
		if _, ok := a.Admins[login]; !ok {
			writeErr(w, http.StatusUnauthorized, "Доступ закрито")
			return
		}
		h(w, r, login)
	}
}

func (a *admin) analytics(w http.ResponseWriter, r *http.Request, _ string) {
	out, err := a.Service.Analytics(r.Context())
	if err != nil {
		a.fail(w, err)
		return
	}
	writeJSON(w, 200, out)
}

func (a *admin) listEmployees(w http.ResponseWriter, r *http.Request, _ string) {
	list, err := a.Service.EmployeesWithStats(r.Context())
	if err != nil {
		a.fail(w, err)
		return
	}
	writeJSON(w, 200, list)
}

func (a *admin) getEmployee(w http.ResponseWriter, r *http.Request, _ string) {
	d, err := a.Service.EmployeeDetail(r.Context(), r.PathValue("id"))
	if err != nil {
		a.fail(w, err)
		return
	}
	writeJSON(w, 200, d)
}

func (a *admin) createEmployee(w http.ResponseWriter, r *http.Request, _ string) {
	var in service.EmployeeInput
	if !decode(w, r, &in) {
		return
	}
	e, err := a.Service.CreateEmployee(r.Context(), in)
	if err != nil {
		a.fail(w, err)
		return
	}
	a.Log.Info("admin: employee created", "email", e.Email)
	writeJSON(w, http.StatusCreated, e)
}

func (a *admin) updateEmployee(w http.ResponseWriter, r *http.Request, _ string) {
	var in service.EmployeeInput
	if !decode(w, r, &in) {
		return
	}
	in.Password = ""
	e, err := a.Service.UpdateEmployee(r.Context(), r.PathValue("id"), in)
	if err != nil {
		a.fail(w, err)
		return
	}
	writeJSON(w, 200, e)
}

func (a *admin) setPassword(w http.ResponseWriter, r *http.Request, _ string) {
	var in struct {
		Password string `json:"password"`
	}
	if !decode(w, r, &in) {
		return
	}
	if err := a.Service.SetEmployeePassword(r.Context(), r.PathValue("id"), in.Password); err != nil {
		a.fail(w, err)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

func (a *admin) deleteEmployee(w http.ResponseWriter, r *http.Request, login string) {
	if err := a.Service.DeleteEmployee(r.Context(), login, r.PathValue("id")); err != nil {
		a.fail(w, err)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}
