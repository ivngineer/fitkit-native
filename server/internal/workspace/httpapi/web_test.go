package httpapi

import (
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func TestSPA(t *testing.T) {
	dir := t.TempDir()
	must := func(err error) {
		if err != nil {
			t.Fatal(err)
		}
	}
	must(os.MkdirAll(filepath.Join(dir, "assets"), 0o755))
	must(os.WriteFile(filepath.Join(dir, "index.html"), []byte("<!doctype html><div id=root>"), 0o644))
	must(os.WriteFile(filepath.Join(dir, "assets", "index-AbCd1234.js"), []byte("console.log(1)"), 0o644))
	must(os.WriteFile(filepath.Join(dir, "assets", "logo-mark.png"), []byte("png"), 0o644))
	h := securityHeaders(spa(dir))

	get := func(path string) (*http.Response, string) {
		rec := httptest.NewRecorder()
		h.ServeHTTP(rec, httptest.NewRequest("GET", path, nil))
		res := rec.Result()
		b, _ := io.ReadAll(res.Body)
		return res, string(b)
	}
	cases := []struct {
		path, wantBody, wantCache string
		code                      int
	}{
		{"/", "<!doctype", "no-cache", 200},
		{"/orders/7f1e", "<!doctype", "no-cache", 200}, // client-side route
		{"/assets/index-AbCd1234.js", "console", "immutable", 200},
		{"/assets/logo-mark.png", "png", "max-age=86400", 200},
		{"/assets/missing.js", "", "", 404},
		{"/api/unknown", `"error"`, "", 404},
		{"/../../etc/passwd", "invalid URL path", "", 400}, // path traversal is rejected
	}
	for _, c := range cases {
		res, body := get(c.path)
		if res.StatusCode != c.code || !strings.Contains(body, c.wantBody) || !strings.Contains(res.Header.Get("Cache-Control"), c.wantCache) {
			t.Errorf("%s: %d %q cache=%q", c.path, res.StatusCode, body, res.Header.Get("Cache-Control"))
		}
		if res.Header.Get("X-Content-Type-Options") != "nosniff" {
			t.Errorf("%s: no security headers", c.path)
		}
	}
}

func TestThrottle(t *testing.T) {
	th := newThrottle(3, time.Minute)
	for i := 0; i < 3; i++ {
		if !th.allow("ip|a") {
			t.Fatalf("attempt %d blocked", i+1)
		}
	}
	if th.allow("ip|a") {
		t.Fatal("4th attempt allowed")
	}
	if !th.allow("ip|b") {
		t.Fatal("other e-mail blocked")
	}
	th.reset("ip|a")
	if !th.allow("ip|a") {
		t.Fatal("reset did not clear")
	}
}

func TestClientIP(t *testing.T) {
	r := httptest.NewRequest("GET", "/", nil)
	r.RemoteAddr = "10.0.0.5:4431"
	if got := clientIP(r); got != "10.0.0.5" {
		t.Fatal(got)
	}
	r.Header.Set("X-Forwarded-For", "203.0.113.7, 10.0.0.1")
	if got := clientIP(r); got != "203.0.113.7" {
		t.Fatal(got)
	}
}
