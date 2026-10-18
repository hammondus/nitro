package main

import (
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func discardLogger() *slog.Logger {
	return slog.New(slog.NewTextHandler(io.Discard, nil))
}

func writeFile(t *testing.T, dir, name, content string) {
	t.Helper()
	if err := os.WriteFile(filepath.Join(dir, name), []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}
}

func testServer(t *testing.T, cfg *Config) *server {
	t.Helper()
	if err := cfg.validate(); err != nil {
		t.Fatal(err)
	}
	s, err := buildServer(cfg, discardLogger())
	if err != nil {
		t.Fatal(err)
	}
	return s
}

func hostGet(t *testing.T, h http.Handler, host, path string) *httptest.ResponseRecorder {
	t.Helper()
	req := httptest.NewRequest(http.MethodGet, path, nil)
	req.Host = host
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)
	return rec
}

func TestVhostRouting(t *testing.T) {
	a, b := t.TempDir(), t.TempDir()
	writeFile(t, a, "index.html", "site a")
	writeFile(t, b, "index.html", "site b")
	s := testServer(t, &Config{
		Listen: ":0",
		Sites: []*Site{
			{Host: "a.com", Root: a},
			{Host: "b.com", Root: b},
			{Host: "*.a.com", Routes: []Route{{Prefix: "/", Kind: routeRedirect, Target: "https://a.com/", Code: 308}}},
		},
	})

	if body := hostGet(t, s, "a.com", "/").Body.String(); !strings.Contains(body, "site a") {
		t.Errorf("a.com got %q", body)
	}
	if body := hostGet(t, s, "b.com:8080", "/").Body.String(); !strings.Contains(body, "site b") {
		t.Errorf("b.com with port got %q", body)
	}
	if body := hostGet(t, s, "A.COM", "/").Body.String(); !strings.Contains(body, "site a") {
		t.Errorf("host matching is case-sensitive: %q", body)
	}
	if rec := hostGet(t, s, "www.a.com", "/x"); rec.Code != 308 || rec.Header().Get("Location") != "https://a.com/x" {
		t.Errorf("wildcard: %d %q", rec.Code, rec.Header().Get("Location"))
	}
	// One label only: deeper names do not match the wildcard.
	if rec := hostGet(t, s, "x.y.a.com", "/"); rec.Code != http.StatusMisdirectedRequest {
		t.Errorf("two-label wildcard match = %d, want 421", rec.Code)
	}
	if rec := hostGet(t, s, "other.com", "/"); rec.Code != http.StatusMisdirectedRequest {
		t.Errorf("unknown host = %d, want 421", rec.Code)
	}
}

func TestSingleSiteCatchesAllHosts(t *testing.T) {
	dir := t.TempDir()
	writeFile(t, dir, "index.html", "only site")
	s := testServer(t, &Config{Listen: ":0", Sites: []*Site{{Host: "a.com", Root: dir}}})
	if body := hostGet(t, s, "whatever.example", "/").Body.String(); !strings.Contains(body, "only site") {
		t.Errorf("single site should catch every host, got %q", body)
	}
}

func TestHealthzLoopbackOnly(t *testing.T) {
	dir := t.TempDir()
	s := testServer(t, &Config{Listen: ":0", Sites: []*Site{{Host: "a.com", Root: dir}}})

	req := httptest.NewRequest(http.MethodGet, "/healthz", nil)
	req.RemoteAddr = "127.0.0.1:5555"
	rec := httptest.NewRecorder()
	s.ServeHTTP(rec, req)
	if rec.Code != 200 || rec.Body.String() != "ok\n" {
		t.Errorf("loopback /healthz = %d %q", rec.Code, rec.Body.String())
	}

	req = httptest.NewRequest(http.MethodGet, "/healthz", nil)
	req.RemoteAddr = "203.0.113.9:5555"
	rec = httptest.NewRecorder()
	s.ServeHTTP(rec, req)
	if rec.Code != 404 {
		t.Errorf("remote /healthz = %d, want the site's own 404", rec.Code)
	}
}

func TestRedirectRoutes(t *testing.T) {
	s := testServer(t, &Config{Listen: ":0", Sites: []*Site{{
		Host: "a.com",
		Routes: []Route{
			{Prefix: "/old", Kind: routeRedirect, Target: "/new", Code: 301},
			{Prefix: "/gone/", Kind: routeRedirect, Target: "https://b.com/", Code: 308},
		},
	}}})

	tests := []struct {
		path, location string
		code           int
	}{
		{"/old", "/new", 301},
		{"/old/sub/page?q=1", "/new/sub/page?q=1", 301},
		{"/older", "", 404}, // segment boundary: /old must not match /older
		{"/gone/x", "https://b.com/x", 308},
	}
	for _, tt := range tests {
		rec := hostGet(t, s, "a.com", tt.path)
		if tt.location == "" {
			if rec.Code != tt.code {
				t.Errorf("GET %s = %d, want %d", tt.path, rec.Code, tt.code)
			}
			continue
		}
		if rec.Code != tt.code || rec.Header().Get("Location") != tt.location {
			t.Errorf("GET %s = %d %q, want %d %q", tt.path, rec.Code, rec.Header().Get("Location"), tt.code, tt.location)
		}
	}
}

func TestLongestPrefixWins(t *testing.T) {
	s := testServer(t, &Config{Listen: ":0", Sites: []*Site{{
		Host: "a.com",
		Routes: []Route{
			{Prefix: "/api", Kind: routeRedirect, Target: "/short", Code: 302},
			{Prefix: "/api/v2", Kind: routeRedirect, Target: "/long", Code: 302},
		},
	}}})
	if loc := hostGet(t, s, "a.com", "/api/v2/users").Header().Get("Location"); loc != "/long/users" {
		t.Errorf("Location = %q, want /long/users", loc)
	}
	if loc := hostGet(t, s, "a.com", "/api/v1").Header().Get("Location"); loc != "/short/v1" {
		t.Errorf("Location = %q, want /short/v1", loc)
	}
}

func TestSiteHeadersAndHSTS(t *testing.T) {
	dir := t.TempDir()
	writeFile(t, dir, "index.html", "x")
	s := testServer(t, &Config{
		Listen: ":0",
		TLS:    &TLSConfig{CacheDir: t.TempDir()},
		Sites: []*Site{{
			Host: "a.com", Root: dir, HSTS: true,
			Headers: []Header{{"X-Frame-Options", "DENY"}},
		}},
	})
	rec := hostGet(t, s, "a.com", "/")
	if got := rec.Header().Get("X-Frame-Options"); got != "DENY" {
		t.Errorf("X-Frame-Options = %q", got)
	}
	if got := rec.Header().Get("Strict-Transport-Security"); got != "max-age=31536000" {
		t.Errorf("HSTS = %q", got)
	}
}

// A site's own Cache-Control is deliberate, so the static default must
// not overwrite it; without one, the default applies.
func TestSiteCacheControlOverridesDefault(t *testing.T) {
	dir := t.TempDir()
	writeFile(t, dir, "index.html", "x")
	writeFile(t, dir, "style.css", "body{}")
	s := testServer(t, &Config{
		Listen: ":0",
		Sites: []*Site{
			{Host: "a.com", Root: dir, Headers: []Header{{"Cache-Control", "public, max-age=600"}}},
			{Host: "b.com", Root: dir},
		},
	})
	for _, p := range []string{"/", "/style.css"} {
		if got := hostGet(t, s, "a.com", p).Header().Get("Cache-Control"); got != "public, max-age=600" {
			t.Errorf("a.com%s Cache-Control = %q, want the site's", p, got)
		}
		if got := hostGet(t, s, "b.com", p).Header().Get("Cache-Control"); got != "no-cache" {
			t.Errorf("b.com%s Cache-Control = %q, want no-cache", p, got)
		}
	}
}

func TestPrefixMatch(t *testing.T) {
	tests := []struct {
		prefix, path string
		want         bool
	}{
		{"/", "/", true},
		{"/", "/anything", true},
		{"/old", "/old", true},
		{"/old", "/old/x", true},
		{"/old", "/older", false},
		{"/api/", "/api/x", true},
		{"/api/", "/api", false},
		{"/api/", "/apix", false},
	}
	for _, tt := range tests {
		if got := prefixMatch(tt.prefix, tt.path); got != tt.want {
			t.Errorf("prefixMatch(%q, %q) = %v, want %v", tt.prefix, tt.path, got, tt.want)
		}
	}
}
