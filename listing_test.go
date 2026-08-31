package main

import (
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

// listingFixture: a directory with mixed sizes, dates, a subdirectory,
// and a dotfile that must not appear.
func listingFixture(t *testing.T) *staticHandler {
	t.Helper()
	dir := t.TempDir()
	if err := os.MkdirAll(filepath.Join(dir, "docs"), 0o755); err != nil {
		t.Fatal(err)
	}
	for name, size := range map[string]int{"big.bin": 5000, "small.txt": 10, ".hidden": 1} {
		if err := os.WriteFile(filepath.Join(dir, name), make([]byte, size), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	old := time.Date(2020, 1, 2, 3, 4, 5, 0, time.UTC)
	if err := os.Chtimes(filepath.Join(dir, "big.bin"), old, old); err != nil {
		t.Fatal(err)
	}
	h, err := newStaticHandler(dir, true, false)
	if err != nil {
		t.Fatal(err)
	}
	return h
}

func TestListingRenders(t *testing.T) {
	h := listingFixture(t)
	rec := get(t, h, "/")
	if rec.Code != http.StatusOK {
		t.Fatalf("GET / = %d", rec.Code)
	}
	body := rec.Body.String()
	for _, want := range []string{"docs/", "big.bin", "small.txt", "4.9 KB", "10 B", "2020-01-02"} {
		if !strings.Contains(body, want) {
			t.Errorf("listing does not contain %q", want)
		}
	}
	if strings.Contains(body, ".hidden") {
		t.Error("listing shows a dotfile")
	}
	if cc := rec.Header().Get("Cache-Control"); cc != "no-cache" {
		t.Errorf("Cache-Control = %q", cc)
	}
	// Directories sort ahead of files regardless of name.
	if strings.Index(body, "docs/") > strings.Index(body, "big.bin") {
		t.Error("directory listed after files")
	}
}

func TestListingSorts(t *testing.T) {
	h := listingFixture(t)

	body := get(t, h, "/?sort=size").Body.String()
	if strings.Index(body, "small.txt") > strings.Index(body, "big.bin") {
		t.Error("sort=size: small.txt should come first")
	}
	body = get(t, h, "/?sort=size&desc=1").Body.String()
	if strings.Index(body, "big.bin") > strings.Index(body, "small.txt") {
		t.Error("sort=size desc: big.bin should come first")
	}
	body = get(t, h, "/?sort=time").Body.String()
	if strings.Index(body, "big.bin") > strings.Index(body, "small.txt") {
		t.Error("sort=time: the 2020 file should come first")
	}
	// Nonsense sort values fall back to the name sort, no error.
	if rec := get(t, h, "/?sort=bogus"); rec.Code != http.StatusOK {
		t.Errorf("sort=bogus = %d", rec.Code)
	}
}

func TestListingBreadcrumbs(t *testing.T) {
	h := listingFixture(t)
	body := get(t, h, "/docs/").Body.String()
	if !strings.Contains(body, `<a href="/">/</a>`) {
		t.Error("no root breadcrumb")
	}
	if !strings.Contains(body, `<a href="/docs/">docs</a>`) {
		t.Error("no docs breadcrumb")
	}
	if !strings.Contains(body, "Empty directory") {
		t.Error("empty directory message missing")
	}
}

func TestBreadcrumbEscaping(t *testing.T) {
	crumbs := breadcrumbs("/a b/c/")
	if len(crumbs) != 3 {
		t.Fatalf("crumbs: %+v", crumbs)
	}
	if crumbs[1].Href != "/a%20b/" || crumbs[2].Href != "/a%20b/c/" {
		t.Errorf("escaped hrefs: %+v", crumbs)
	}
}

func TestHumanSize(t *testing.T) {
	tests := []struct {
		n    int64
		want string
	}{
		{0, "0 B"}, {1023, "1023 B"}, {1024, "1.0 KB"},
		{4404019, "4.2 MB"}, {5 << 30, "5.0 GB"},
	}
	for _, tt := range tests {
		if got := humanSize(tt.n); got != tt.want {
			t.Errorf("humanSize(%d) = %q, want %q", tt.n, got, tt.want)
		}
	}
}
