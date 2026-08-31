package main

import (
	"errors"
	"io/fs"
	"net/http"
	"os"
	"path"
	"strings"
	"time"

	"github.com/hammondus/nitrokit"
)

// writeBudget bounds how long a single write to the client may stall.
// The server's global WriteTimeout is 0 (a proxied stream must be able
// to outlive any constant), so static responses arm their own rolling
// deadline instead: each write buys another budget, like nginx
// send_timeout, so a slow download survives while a stalled client is
// cut.
const writeBudget = 30 * time.Second

// staticHandler serves one site's files from an os.Root, which refuses
// path escapes — including through symlinks — by construction.
type staticHandler struct {
	root    *os.Root
	listing bool
	spa     bool
}

func newStaticHandler(dir string, listing, spa bool) (*staticHandler, error) {
	root, err := os.OpenRoot(dir)
	if err != nil {
		return nil, err
	}
	return &staticHandler{root: root, listing: listing, spa: spa}, nil
}

func (h *staticHandler) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet && r.Method != http.MethodHead {
		w.Header().Set("Allow", "GET, HEAD")
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}
	w = newDeadlineWriter(w)

	// Canonicalise before touching the filesystem: /a//b, /a/./b and
	// /a/../b all redirect to the clean form, so every file has one URL.
	canon := path.Clean(r.URL.Path)
	if r.URL.Path != canon && r.URL.Path != canon+"/" {
		redirect(w, r, canon)
		return
	}
	if hasDotSegment(canon) {
		// The same 404 an absent file gets: probing for .git or .env
		// reveals nothing, not even that dotfiles are special.
		http.NotFound(w, r)
		return
	}
	// index.html is the directory's content, not a page beside it, so the
	// directory is its canonical URL. Cutting "/index.html" rather than
	// "index.html" keeps a real file named myindex.html intact. This sits
	// after the dotfile check so /.git/index.html stays an ordinary 404
	// instead of a redirect that confirms the directory is special.
	if dir, ok := strings.CutSuffix(canon, "/index.html"); ok {
		redirect(w, r, dir+"/")
		return
	}

	rel := strings.TrimPrefix(canon, "/")
	if rel == "" {
		rel = "."
	}
	info, err := h.root.Stat(rel)
	if err != nil {
		h.notFound(w, r, canon)
		return
	}

	switch {
	case info.IsDir():
		// Directories live at the trailing-slash URL: relative links in
		// index pages and listings depend on it.
		if !strings.HasSuffix(r.URL.Path, "/") {
			redirect(w, r, canon+"/")
			return
		}
		index := path.Join(rel, "index.html")
		if _, err := h.root.Stat(index); err == nil {
			h.serveFile(w, r, index)
			return
		}
		if h.listing {
			h.serveListing(w, r, rel, canon)
			return
		}
		http.NotFound(w, r)
	case info.Mode().IsRegular():
		if strings.HasSuffix(r.URL.Path, "/") {
			redirect(w, r, canon)
			return
		}
		h.serveFile(w, r, rel)
	default:
		// Sockets, devices, and the like are not content.
		http.NotFound(w, r)
	}
}

// notFound is the miss path: under spa an extensionless path is a
// client-side route and gets the app shell instead of a 404.
func (h *staticHandler) notFound(w http.ResponseWriter, r *http.Request, canon string) {
	if h.spa && path.Ext(canon) == "" {
		h.serveFile(w, r, "index.html")
		return
	}
	http.NotFound(w, r)
}

func (h *staticHandler) serveFile(w http.ResponseWriter, r *http.Request, rel string) {
	f, err := h.root.Open(rel)
	if err != nil {
		if errors.Is(err, fs.ErrNotExist) {
			http.NotFound(w, r)
		} else {
			http.Error(w, "internal error", http.StatusInternalServerError)
		}
		return
	}
	defer f.Close()
	info, err := f.Stat()
	if err != nil {
		http.Error(w, "internal error", http.StatusInternalServerError)
		return
	}
	setCachePolicy(w, rel)
	// ServeContent brings Last-Modified/If-Modified-Since, ranges, HEAD,
	// and Content-Type sniffing.
	http.ServeContent(w, r, path.Base(rel), info.ModTime(), f)
}

// setCachePolicy is the house policy at the chokepoint: pages revalidate
// every time, assets cache briefly. These are unhashed disk files whose
// content can change under the URL, so nothing is ever immutable.
func setCachePolicy(w http.ResponseWriter, rel string) {
	switch path.Ext(rel) {
	case ".html", ".htm", "":
		nitrokit.NoCache(w)
	default:
		w.Header().Set("Cache-Control", "public, max-age=3600")
	}
}

// hasDotSegment reports whether any path segment starts with a dot.
func hasDotSegment(p string) bool {
	for seg := range strings.SplitSeq(p, "/") {
		if strings.HasPrefix(seg, ".") {
			return true
		}
	}
	return false
}

// redirect points the client at loc, keeping the query string. 301: the
// canonical URL of a file is permanent knowledge.
func redirect(w http.ResponseWriter, r *http.Request, loc string) {
	if r.URL.RawQuery != "" {
		loc += "?" + r.URL.RawQuery
	}
	http.Redirect(w, r, loc, http.StatusMovedPermanently)
}

// deadlineWriter renews the connection's write deadline as writes make
// progress, at most once a second. Unwrap keeps http.ResponseController
// working through it.
type deadlineWriter struct {
	http.ResponseWriter
	rc   *http.ResponseController
	last time.Time
}

func newDeadlineWriter(w http.ResponseWriter) *deadlineWriter {
	d := &deadlineWriter{ResponseWriter: w, rc: http.NewResponseController(w)}
	d.bump()
	return d
}

func (d *deadlineWriter) bump() {
	now := time.Now()
	if now.Sub(d.last) >= time.Second {
		// Ignored error: a connection that cannot take a deadline (an
		// httptest recorder) just runs without one.
		d.rc.SetWriteDeadline(now.Add(writeBudget))
		d.last = now
	}
}

func (d *deadlineWriter) Write(b []byte) (int, error) {
	d.bump()
	return d.ResponseWriter.Write(b)
}

func (d *deadlineWriter) Unwrap() http.ResponseWriter { return d.ResponseWriter }
