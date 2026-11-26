package httpapi

import (
	"net"
	"net/http"
	"os"
	"path"
	"path/filepath"
	"regexp"
	"strings"
	"sync"
	"time"
)

// spa serves the built front-end. Real files are served as-is; any other path that is not an
// API or webhook route gets index.html so client-side routes (/orders/…) survive a reload.
func spa(dir string) http.Handler {
	files := http.FileServer(http.Dir(dir))
	hashed := regexp.MustCompile(`-[A-Za-z0-9_-]{8,}\.(js|css)$`)
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		p := path.Clean("/" + r.URL.Path)
		if strings.HasPrefix(p, "/api/") || strings.HasPrefix(p, "/webhooks/") {
			writeErr(w, http.StatusNotFound, "Не знайдено")
			return
		}
		full := filepath.Join(dir, filepath.FromSlash(p))
		if st, err := os.Stat(full); err == nil && !st.IsDir() {
			switch {
			case hashed.MatchString(p):
				w.Header().Set("Cache-Control", "public, max-age=31536000, immutable")
			case p == "/index.html":
				w.Header().Set("Cache-Control", "no-cache")
			default:
				w.Header().Set("Cache-Control", "public, max-age=86400")
			}
			files.ServeHTTP(w, r)
			return
		}
		if path.Ext(p) != "" { // a missing asset must be a 404, not an HTML page
			http.NotFound(w, r)
			return
		}
		w.Header().Set("Cache-Control", "no-cache")
		http.ServeFile(w, r, filepath.Join(dir, "index.html"))
	})
}

func securityHeaders(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		h := w.Header()
		h.Set("X-Content-Type-Options", "nosniff")
		h.Set("X-Frame-Options", "DENY")
		h.Set("Referrer-Policy", "same-origin")
		if r.TLS != nil || r.Header.Get("X-Forwarded-Proto") == "https" {
			h.Set("Strict-Transport-Security", "max-age=31536000")
		}
		next.ServeHTTP(w, r)
	})
}

// clientIP is the caller's address; behind a proxy it is the first X-Forwarded-For entry.
func clientIP(r *http.Request) string {
	if xff := r.Header.Get("X-Forwarded-For"); xff != "" {
		return strings.TrimSpace(strings.Split(xff, ",")[0])
	}
	host, _, err := net.SplitHostPort(r.RemoteAddr)
	if err != nil {
		return r.RemoteAddr
	}
	return host
}

// throttle limits failed sign-ins per (IP, e-mail): `max` attempts per `window`. In memory — enough for one instance.
type throttle struct {
	mu     sync.Mutex
	max    int
	window time.Duration
	hits   map[string][]time.Time
}

func newThrottle(max int, window time.Duration) *throttle {
	return &throttle{max: max, window: window, hits: map[string][]time.Time{}}
}

// allow records an attempt and reports whether it is within the limit.
func (t *throttle) allow(key string) bool {
	t.mu.Lock()
	defer t.mu.Unlock()
	now := time.Now()
	recent := t.hits[key][:0]
	for _, at := range t.hits[key] {
		if now.Sub(at) < t.window {
			recent = append(recent, at)
		}
	}
	if len(recent) >= t.max {
		t.hits[key] = recent
		return false
	}
	t.hits[key] = append(recent, now)
	if len(t.hits) > 10000 { // keep memory bounded
		for k, v := range t.hits {
			if len(v) == 0 || now.Sub(v[len(v)-1]) >= t.window {
				delete(t.hits, k)
			}
		}
	}
	return true
}

func (t *throttle) reset(key string) {
	t.mu.Lock()
	delete(t.hits, key)
	t.mu.Unlock()
}
