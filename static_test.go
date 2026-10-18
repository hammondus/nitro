package main

import (
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// staticFixture builds a tree covering every static-handler behaviour:
//
//	index.html, myindex.html, style.css, download.bin
//	sub/page.html, sub/note.txt
//	bare/            (no index)
//	.secret, .git/config
func staticFixture(t *testing.T) string {
	t.Helper()
	dir := t.TempDir()
	files := map[string]string{
		"index.html":    "<h1>home</h1>",
		"myindex.html":  "<h1>not the index</h1>",
		"style.css":     "body{}",
		"download.bin":  "binary",
		"sub/page.html": "<h1>sub</h1>",
		"sub/note.txt":  "note",
		".secret":       "sssh",
		".git/config":   "[core]",
	}
	for name, content := range files {
		path := filepath.Join(dir, filepath.FromSlash(name))
		if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	if err := os.MkdirAll(filepath.Join(dir, "bare"), 0o755); err != nil {
		t.Fatal(err)
	}
	return dir
}

func get(t *testing.T, h http.Handler, target string) *httptest.ResponseRecorder {
	t.Helper()
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, target, nil))
	return rec
}

func TestStaticServing(t *testing.T) {
	h, err := newStaticHandler(staticFixture(t), false, false)
	if err != nil {
		t.Fatal(err)
	}

	tests := []struct {
		path     string
		status   int
		body     string // substring, checked when non-empty
		cache    string
		location string
	}{
		{path: "/", status: 200, body: "home", cache: "no-cache"},
		{path: "/style.css", status: 200, body: "body{}", cache: "no-cache"},
		{path: "/sub/page.html", status: 200, body: "sub", cache: "no-cache"},
		{path: "/sub", status: 301, location: "/sub/"},
		{path: "/sub/", status: 404}, // no index, no listing
		{path: "/bare/", status: 404},
		{path: "/style.css/", status: 301, location: "/style.css"},
		{path: "/missing", status: 404},
		{path: "//style.css", status: 301, location: "/style.css"},
		{path: "/sub/../style.css", status: 301, location: "/style.css"},
		{path: "/.secret", status: 404},
		{path: "/.git/config", status: 404},
		{path: "/sub/%2e%2e/index.html", status: 301, location: "/index.html"},
		// index.html is the directory's content, so the directory is the
		// canonical URL. Checked before the filesystem, so a missing
		// index.html redirects too and lands on the directory's own answer.
		{path: "/index.html", status: 301, location: "/"},
		{path: "/sub/index.html", status: 301, location: "/sub/"},
		{path: "/bare/index.html", status: 301, location: "/bare/"},
		// Only a whole final segment counts: this is a real file.
		{path: "/myindex.html", status: 200, body: "not the index", cache: "no-cache"},
		// Still an ordinary 404: no redirect confirms the directory exists.
		{path: "/.git/index.html", status: 404},
	}
	for _, tt := range tests {
		rec := get(t, h, tt.path)
		if rec.Code != tt.status {
			t.Errorf("GET %s = %d, want %d", tt.path, rec.Code, tt.status)
			continue
		}
		if tt.body != "" && !strings.Contains(rec.Body.String(), tt.body) {
			t.Errorf("GET %s body %q does not contain %q", tt.path, rec.Body.String(), tt.body)
		}
		if tt.cache != "" && rec.Header().Get("Cache-Control") != tt.cache {
			t.Errorf("GET %s Cache-Control = %q, want %q", tt.path, rec.Header().Get("Cache-Control"), tt.cache)
		}
		if tt.location != "" && rec.Header().Get("Location") != tt.location {
			t.Errorf("GET %s Location = %q, want %q", tt.path, rec.Header().Get("Location"), tt.location)
		}
	}
}

func TestStaticMIMETypes(t *testing.T) {
	dir := t.TempDir()
	tests := map[string]string{
		"app.webmanifest": "application/manifest+json",
		"font.woff2":      "font/woff2",
		"font.woff":       "font/woff",
		"font.ttf":        "font/ttf",
		"font.otf":        "font/otf",
	}
	for name := range tests {
		writeFile(t, dir, name, "{}")
	}
	h, err := newStaticHandler(dir, false, false)
	if err != nil {
		t.Fatal(err)
	}
	for name, want := range tests {
		if got := get(t, h, "/"+name).Header().Get("Content-Type"); got != want {
			t.Errorf("GET /%s Content-Type = %q, want %q", name, got, want)
		}
	}
}

func TestStaticRedirectKeepsQuery(t *testing.T) {
	h, err := newStaticHandler(staticFixture(t), false, false)
	if err != nil {
		t.Fatal(err)
	}
	rec := get(t, h, "/sub?a=1")
	if loc := rec.Header().Get("Location"); loc != "/sub/?a=1" {
		t.Errorf("Location = %q, want /sub/?a=1", loc)
	}
}

func TestStaticMethodNotAllowed(t *testing.T) {
	h, err := newStaticHandler(staticFixture(t), false, false)
	if err != nil {
		t.Fatal(err)
	}
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, httptest.NewRequest(http.MethodPost, "/", nil))
	if rec.Code != http.StatusMethodNotAllowed {
		t.Errorf("POST / = %d, want 405", rec.Code)
	}
	if allow := rec.Header().Get("Allow"); allow != "GET, HEAD" {
		t.Errorf("Allow = %q", allow)
	}
}

func TestStaticHead(t *testing.T) {
	h, err := newStaticHandler(staticFixture(t), false, false)
	if err != nil {
		t.Fatal(err)
	}
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, httptest.NewRequest(http.MethodHead, "/style.css", nil))
	if rec.Code != 200 || rec.Body.Len() != 0 {
		t.Errorf("HEAD = %d with %d body bytes", rec.Code, rec.Body.Len())
	}
}

func TestStaticSymlinkEscape(t *testing.T) {
	dir := t.TempDir()
	outside := t.TempDir()
	if err := os.WriteFile(filepath.Join(outside, "leak.txt"), []byte("leak"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(filepath.Join(outside, "leak.txt"), filepath.Join(dir, "link.txt")); err != nil {
		t.Skipf("symlink: %v", err)
	}
	h, err := newStaticHandler(dir, false, false)
	if err != nil {
		t.Fatal(err)
	}
	if rec := get(t, h, "/link.txt"); rec.Code != 404 {
		t.Errorf("symlink escape served: %d %q", rec.Code, rec.Body.String())
	}
}

func TestSPAFallback(t *testing.T) {
	dir := staticFixture(t)
	h, err := newStaticHandler(dir, false, true)
	if err != nil {
		t.Fatal(err)
	}
	rec := get(t, h, "/client/route")
	if rec.Code != 200 || !strings.Contains(rec.Body.String(), "home") {
		t.Errorf("SPA route = %d %q, want the app shell", rec.Code, rec.Body.String())
	}
	if cc := rec.Header().Get("Cache-Control"); cc != "no-cache" {
		t.Errorf("SPA shell Cache-Control = %q", cc)
	}
	// A missing asset with an extension is a real 404, not the shell.
	if rec := get(t, h, "/missing.js"); rec.Code != 404 {
		t.Errorf("missing asset = %d, want 404", rec.Code)
	}
}

// The SPA shell is served by path, not by URL, so canonicalising
// /index.html to / cannot bounce a request between the two.
func TestSPAIndexCanonicalDoesNotLoop(t *testing.T) {
	h, err := newStaticHandler(staticFixture(t), false, true)
	if err != nil {
		t.Fatal(err)
	}
	rec := get(t, h, "/index.html")
	if rec.Code != 301 || rec.Header().Get("Location") != "/" {
		t.Fatalf("GET /index.html = %d -> %q, want 301 -> /", rec.Code, rec.Header().Get("Location"))
	}
	if rec := get(t, h, "/"); rec.Code != 200 || !strings.Contains(rec.Body.String(), "home") {
		t.Errorf("GET / = %d %q, want the page", rec.Code, rec.Body.String())
	}
}
