package main

import (
	"fmt"
	"log/slog"
	"net"
	"net/http"
	"net/netip"
	"slices"
	"strings"

	"github.com/hammondus/nitrokit"
)

// server routes a request to its site by Host header. It is built once
// at startup and never mutated.
type server struct {
	exact     map[string]http.Handler
	wildcards []wildcardSite
	catchAll  http.Handler // set for zero-config and single-site configs
}

type wildcardSite struct {
	suffix  string // ".example.com"
	handler http.Handler
}

// buildServer turns a validated Config into the request-routing handler.
func buildServer(cfg *Config, logger *slog.Logger) (*server, error) {
	trust := cfg.Trusted
	if trust == nil {
		trust = &nitrokit.ProxyTrust{} // trusts no one: peer is the client
	}

	s := &server{exact: make(map[string]http.Handler)}
	for _, site := range cfg.Sites {
		h, err := buildSite(site, trust, logger)
		if err != nil {
			return nil, err
		}
		switch {
		case wildcardHost(site.Host):
			s.wildcards = append(s.wildcards, wildcardSite{site.Host[1:], h})
		case site.Host != "":
			s.exact[site.Host] = h
		}
		// A single site serves every request, whatever its host: the
		// config named one site, so there is nothing to disambiguate.
		if len(cfg.Sites) == 1 {
			s.catchAll = h
		}
	}
	return s, nil
}

func (s *server) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	// The liveness probe answers before routing, for loopback peers
	// only: Docker's HEALTHCHECK probes 127.0.0.1, and a remote client
	// asking for /healthz falls through to the sites like any path.
	if r.URL.Path == "/healthz" && peerIsLoopback(r) {
		nitrokit.Healthz(w, r)
		return
	}
	host := strings.ToLower(r.Host)
	if h, _, err := net.SplitHostPort(r.Host); err == nil {
		host = strings.ToLower(h)
	}
	if h, ok := s.exact[host]; ok {
		h.ServeHTTP(w, r)
		return
	}
	for _, wc := range s.wildcards {
		// One label only: a.example.com matches *.example.com,
		// a.b.example.com does not — certificate wildcard semantics.
		if rest, ok := strings.CutSuffix(host, wc.suffix); ok && rest != "" && !strings.Contains(rest, ".") {
			wc.handler.ServeHTTP(w, r)
			return
		}
	}
	if s.catchAll != nil {
		s.catchAll.ServeHTTP(w, r)
		return
	}
	http.Error(w, "misdirected request", http.StatusMisdirectedRequest)
}

func peerIsLoopback(r *http.Request) bool {
	ap, err := netip.ParseAddrPort(r.RemoteAddr)
	return err == nil && ap.Addr().IsLoopback()
}

// siteHandler runs one site: longest-prefix route match over redirects
// and proxies, falling through to static files.
type siteHandler struct {
	routes  []Route
	proxies map[string]http.Handler // proxy route prefix → reverse proxy
	static  http.Handler            // nil for proxy- or redirect-only sites
}

func buildSite(site *Site, trust *nitrokit.ProxyTrust, logger *slog.Logger) (http.Handler, error) {
	sh := &siteHandler{
		// Longest prefix first, so matching is a scan to the first hit.
		routes:  slices.SortedFunc(slices.Values(site.Routes), func(a, b Route) int { return len(b.Prefix) - len(a.Prefix) }),
		proxies: make(map[string]http.Handler),
	}
	for _, rt := range site.Routes {
		if rt.Kind == routeProxy {
			p, err := newProxy(rt.Target, trust, logger)
			if err != nil {
				return nil, fmt.Errorf("site %s: %w", site.Host, err)
			}
			sh.proxies[rt.Prefix] = p
		}
	}
	if site.Root != "" {
		static, err := newStaticHandler(site.Root, site.Listing, site.SPA)
		if err != nil {
			return nil, fmt.Errorf("site %s: %w", site.Host, err)
		}
		sh.static = static
	}

	h := http.Handler(sh)
	if site.HSTS {
		h = nitrokit.HSTS(h)
	}
	if len(site.Headers) > 0 {
		headers, next := site.Headers, h
		h = http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			for _, hdr := range headers {
				w.Header().Set(hdr.Name, hdr.Value)
			}
			next.ServeHTTP(w, r)
		})
	}
	return h, nil
}

func (s *siteHandler) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	for _, rt := range s.routes {
		if !prefixMatch(rt.Prefix, r.URL.Path) {
			continue
		}
		switch rt.Kind {
		case routeRedirect:
			redirectRoute(w, r, rt)
		case routeProxy:
			s.proxies[rt.Prefix].ServeHTTP(w, r)
		}
		return
	}
	if s.static != nil {
		s.static.ServeHTTP(w, r)
		return
	}
	http.NotFound(w, r)
}

// prefixMatch matches on segment boundaries: /old matches /old and
// /old/x but never /older. A prefix written with a trailing slash
// matches only below itself.
func prefixMatch(prefix, p string) bool {
	rest, ok := strings.CutPrefix(p, prefix)
	if !ok {
		return false
	}
	return rest == "" || strings.HasSuffix(prefix, "/") || rest[0] == '/'
}

// redirectRoute sends the client to the route's target with the
// unmatched path suffix and the query string carried over.
func redirectRoute(w http.ResponseWriter, r *http.Request, rt Route) {
	suffix := r.URL.Path[len(rt.Prefix):]
	loc := rt.Target
	if suffix != "" {
		loc = strings.TrimSuffix(loc, "/")
		if !strings.HasPrefix(suffix, "/") {
			suffix = "/" + suffix
		}
		loc += suffix
	}
	if r.URL.RawQuery != "" {
		loc += "?" + r.URL.RawQuery
	}
	http.Redirect(w, r, loc, rt.Code)
}
