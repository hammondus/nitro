package main

import (
	"fmt"
	"os"
	"strings"

	"github.com/hammondus/nitrokit"
)

// Config is one whole nitro configuration: either parsed from a config
// file or built by zeroConfig. It is immutable after validate; a change
// means a restart.
type Config struct {
	Listen  string // host:port to bind
	Log     string // "stdout", "off", or a file path
	Trusted *nitrokit.ProxyTrust
	TLS     *TLSConfig // nil means plain HTTP
	Sites   []*Site
}

// TLSConfig carries the tls{} block: nitro terminates TLS itself with
// certificates from the ACME CA, via nitrokit.RunTLS.
type TLSConfig struct {
	CacheDir    string // certificate cache, required
	Email       string // CA expiry notices, optional
	Route53Zone string // hosted zone for DNS-01 wildcard issuance, optional
}

// Site is one virtual host. Host is empty only for the catch-all site in
// zero-config mode; a single-site config also matches every request.
type Site struct {
	Host    string // lowercased, port-free; may be "*.example.com"
	Root    string // static file root; empty for proxy- or redirect-only sites
	Listing bool
	SPA     bool
	HSTS    bool
	Headers []Header
	Routes  []Route
}

// Header is one extra response header a site sets on every response.
type Header struct{ Name, Value string }

type routeKind int

const (
	routeRedirect routeKind = iota
	routeProxy
)

// Route maps a path prefix to a redirect or an upstream. Prefixes match
// on segment boundaries: /old matches /old and /old/x, never /older.
type Route struct {
	Prefix string
	Kind   routeKind
	Target string
	Code   int // redirect status; unused for proxies
}

// zeroConfig is the configuration nitro runs with when no config file
// exists: one catch-all site serving root, loopback-only unless -addr
// widens it deliberately.
func zeroConfig(addr, root string, listing bool) *Config {
	return &Config{
		Listen: addr,
		Log:    "stdout",
		Sites:  []*Site{{Root: root, Listing: listing}},
	}
}

// wildcardHost reports whether host is a *.domain wildcard pattern.
func wildcardHost(host string) bool {
	return strings.HasPrefix(host, "*.")
}

// validate applies the whole-config rules that single directives cannot
// check, fills defaults, and verifies every root exists. It runs once at
// startup: a config mistake stops the server rather than surfacing per
// request.
func (c *Config) validate() error {
	if len(c.Sites) == 0 {
		return fmt.Errorf("config has no site blocks")
	}
	if c.Log == "" {
		c.Log = "stdout"
	}
	if c.Listen == "" {
		if c.TLS != nil {
			c.Listen = ":443"
		} else {
			c.Listen = ":8080"
		}
	}
	if c.TLS != nil && c.TLS.CacheDir == "" {
		return fmt.Errorf("tls block needs a cache directory: certificates must persist across restarts")
	}

	seen := make(map[string]bool)
	for _, s := range c.Sites {
		name := s.Host
		if name == "" {
			name = "the catch-all site"
		}
		if seen[s.Host] {
			return fmt.Errorf("site %s is defined twice", s.Host)
		}
		seen[s.Host] = true

		if s.Root == "" && len(s.Routes) == 0 {
			return fmt.Errorf("%s has no root, no proxy, and no redirect: it would serve nothing", name)
		}
		if s.Root == "" && s.Listing {
			return fmt.Errorf("%s has listing on but no root", name)
		}
		if s.Root == "" && s.SPA {
			return fmt.Errorf("%s has spa but no root", name)
		}
		if s.HSTS && c.TLS == nil {
			return fmt.Errorf("%s has hsts but there is no tls block: only a TLS terminator may set Strict-Transport-Security", name)
		}
		if wildcardHost(s.Host) && c.TLS != nil && c.TLS.Route53Zone == "" {
			return fmt.Errorf("site %s needs a dns solver in the tls block: wildcard certificates are only issued through DNS-01", s.Host)
		}
		prefixes := make(map[string]bool)
		for _, rt := range s.Routes {
			if prefixes[rt.Prefix] {
				return fmt.Errorf("%s has two routes for prefix %s", name, rt.Prefix)
			}
			prefixes[rt.Prefix] = true
		}
		if s.Root != "" {
			info, err := os.Stat(s.Root)
			if err != nil {
				return fmt.Errorf("%s: root %s: %w", name, s.Root, err)
			}
			if !info.IsDir() {
				return fmt.Errorf("%s: root %s is not a directory", name, s.Root)
			}
		}
	}
	return nil
}

// tlsHosts collects the hostnames to obtain certificates for: every
// site's host, wildcards included. Called only when c.TLS is set, after
// validate, so every site has a non-empty host.
func (c *Config) tlsHosts() []string {
	hosts := make([]string, 0, len(c.Sites))
	for _, s := range c.Sites {
		hosts = append(hosts, s.Host)
	}
	return hosts
}
