package main

import (
	"fmt"
	"net/url"
	"strconv"
	"strings"

	"github.com/hammondus/nitrokit"
)

// parseConfig turns config file text into a Config. name appears in
// error messages ("nitro.conf:12: ..."). Syntax and per-directive values
// are checked here with line numbers; whole-config rules live in
// Config.validate.
//
// The format is line-based: one directive with its arguments per line,
// `#` starts a comment, double quotes group an argument containing
// spaces, and `site` and `tls` open a `{ ... }` block with the brace on
// the same line.
func parseConfig(name string, data []byte) (*Config, error) {
	p := &parser{name: name, cfg: &Config{}}
	for i, raw := range strings.Split(string(data), "\n") {
		p.line = i + 1
		fields, err := splitFields(raw)
		if err != nil {
			return nil, p.errf("%v", err)
		}
		if len(fields) == 0 {
			continue
		}
		if err := p.directive(fields); err != nil {
			return nil, err
		}
	}
	if p.block != "" {
		return nil, fmt.Errorf("%s: %s block is never closed", p.name, p.block)
	}
	return p.cfg, nil
}

type parser struct {
	name  string
	line  int
	cfg   *Config
	block string // "", "site", or "tls"
	site  *Site  // the open site block
}

func (p *parser) errf(format string, args ...any) error {
	return fmt.Errorf("%s:%d: %s", p.name, p.line, fmt.Sprintf(format, args...))
}

// wants enforces an exact argument count for a directive.
func (p *parser) wants(fields []string, n int, usage string) error {
	if len(fields)-1 != n {
		return p.errf("%s takes %d argument(s): %s", fields[0], n, usage)
	}
	return nil
}

func (p *parser) directive(fields []string) error {
	// Closing brace ends whichever block is open.
	if fields[0] == "}" {
		if len(fields) > 1 {
			return p.errf("unexpected %q after }", fields[1])
		}
		if p.block == "" {
			return p.errf("} without an open block")
		}
		p.block, p.site = "", nil
		return nil
	}
	switch p.block {
	case "site":
		return p.siteDirective(fields)
	case "tls":
		return p.tlsDirective(fields)
	}
	return p.globalDirective(fields)
}

func (p *parser) globalDirective(fields []string) error {
	switch fields[0] {
	case "listen":
		if err := p.wants(fields, 1, "listen :8080"); err != nil {
			return err
		}
		p.cfg.Listen = fields[1]
	case "log":
		if err := p.wants(fields, 1, "log stdout | off | /path/to/file"); err != nil {
			return err
		}
		p.cfg.Log = fields[1]
	case "trusted-proxies":
		if len(fields) < 2 {
			return p.errf("trusted-proxies takes one or more CIDR blocks")
		}
		trust, err := nitrokit.ParseTrustedProxies(strings.Join(fields[1:], ","))
		if err != nil {
			return p.errf("trusted-proxies: %v", err)
		}
		p.cfg.Trusted = trust
	case "tls":
		if len(fields) != 2 || fields[1] != "{" {
			return p.errf(`tls opens a block: tls {`)
		}
		if p.cfg.TLS != nil {
			return p.errf("tls block is defined twice")
		}
		p.cfg.TLS = &TLSConfig{}
		p.block = "tls"
	case "site":
		if len(fields) != 3 || fields[2] != "{" {
			return p.errf(`site opens a block: site example.com {`)
		}
		host := strings.ToLower(fields[1])
		if err := checkHost(host); err != nil {
			return p.errf("site %s: %v", fields[1], err)
		}
		p.site = &Site{Host: host}
		p.cfg.Sites = append(p.cfg.Sites, p.site)
		p.block = "site"
	default:
		return p.unknown(fields[0], []string{"listen", "log", "trusted-proxies", "tls", "site"})
	}
	return nil
}

func (p *parser) tlsDirective(fields []string) error {
	switch fields[0] {
	case "cache":
		if err := p.wants(fields, 1, "cache /data/certs"); err != nil {
			return err
		}
		p.cfg.TLS.CacheDir = fields[1]
	case "email":
		if err := p.wants(fields, 1, "email you@example.com"); err != nil {
			return err
		}
		p.cfg.TLS.Email = fields[1]
	case "dns":
		if err := p.wants(fields, 2, "dns route53 ZONEID"); err != nil {
			return err
		}
		if fields[1] != "route53" {
			return p.errf("dns: unknown provider %q (route53 is the only built-in solver)", fields[1])
		}
		p.cfg.TLS.Route53Zone = fields[2]
	default:
		return p.unknown(fields[0], []string{"cache", "email", "dns"})
	}
	return nil
}

func (p *parser) siteDirective(fields []string) error {
	switch fields[0] {
	case "root":
		if err := p.wants(fields, 1, "root /srv/example"); err != nil {
			return err
		}
		p.site.Root = fields[1]
	case "listing":
		if err := p.wants(fields, 1, "listing on | off"); err != nil {
			return err
		}
		switch fields[1] {
		case "on":
			p.site.Listing = true
		case "off":
			p.site.Listing = false
		default:
			return p.errf("listing: %q is not on or off", fields[1])
		}
	case "spa":
		if len(fields) != 1 {
			return p.errf("spa takes no arguments")
		}
		p.site.SPA = true
	case "hsts":
		if len(fields) != 1 {
			return p.errf("hsts takes no arguments")
		}
		p.site.HSTS = true
	case "header":
		if err := p.wants(fields, 2, `header X-Frame-Options DENY (quote a value containing spaces)`); err != nil {
			return err
		}
		p.site.Headers = append(p.site.Headers, Header{fields[1], fields[2]})
	case "redirect":
		if len(fields) < 3 || len(fields) > 4 {
			return p.errf("redirect takes a prefix, a target, and an optional status: redirect /old /new 301")
		}
		r := Route{Prefix: fields[1], Kind: routeRedirect, Target: fields[2], Code: 308}
		if err := checkPrefix(r.Prefix); err != nil {
			return p.errf("redirect: %v", err)
		}
		if len(fields) == 4 {
			code, err := strconv.Atoi(fields[3])
			if err != nil || !validRedirectCode(code) {
				return p.errf("redirect: %q is not a redirect status (301, 302, 303, 307, or 308)", fields[3])
			}
			r.Code = code
		}
		p.site.Routes = append(p.site.Routes, r)
	case "proxy":
		if err := p.wants(fields, 2, "proxy /api/ localhost:9000"); err != nil {
			return err
		}
		if err := checkPrefix(fields[1]); err != nil {
			return p.errf("proxy: %v", err)
		}
		if _, err := upstreamURL(fields[2]); err != nil {
			return p.errf("proxy: %v", err)
		}
		p.site.Routes = append(p.site.Routes, Route{Prefix: fields[1], Kind: routeProxy, Target: fields[2]})
	default:
		return p.unknown(fields[0], []string{"root", "listing", "spa", "hsts", "header", "redirect", "proxy"})
	}
	return nil
}

func validRedirectCode(code int) bool {
	switch code {
	case 301, 302, 303, 307, 308:
		return true
	}
	return false
}

// checkHost rejects site names that cannot be a Host header value.
func checkHost(host string) error {
	bare := strings.TrimPrefix(host, "*.")
	switch {
	case host == "":
		return fmt.Errorf("empty host")
	case strings.Contains(bare, "*"):
		return fmt.Errorf("only a leading *.domain wildcard is supported")
	case strings.ContainsAny(host, "/: "):
		return fmt.Errorf("a site name is a bare hostname: no scheme, port, or path")
	}
	return nil
}

func checkPrefix(prefix string) error {
	if !strings.HasPrefix(prefix, "/") {
		return fmt.Errorf("prefix %q must start with /", prefix)
	}
	return nil
}

// upstreamURL turns a proxy target into a base URL. A bare host:port
// means plain HTTP; a full URL may name https.
func upstreamURL(target string) (*url.URL, error) {
	if !strings.Contains(target, "://") {
		target = "http://" + target
	}
	u, err := url.Parse(target)
	if err != nil {
		return nil, fmt.Errorf("upstream %q: %v", target, err)
	}
	if u.Scheme != "http" && u.Scheme != "https" {
		return nil, fmt.Errorf("upstream %q: scheme must be http or https", target)
	}
	if u.Host == "" {
		return nil, fmt.Errorf("upstream %q has no host", target)
	}
	if u.Path != "" && u.Path != "/" {
		return nil, fmt.Errorf("upstream %q: a path on the upstream is not supported; the request path is passed through unchanged", target)
	}
	u.Path = ""
	return u, nil
}

// unknown builds the error for a directive that does not exist, with a
// did-you-mean suggestion when one known directive is close.
func (p *parser) unknown(got string, known []string) error {
	for _, k := range known {
		if editDistance(got, k) <= 2 {
			return p.errf("unknown directive %q (did you mean %q?)", got, k)
		}
	}
	where := "here"
	switch p.block {
	case "site":
		where = "in a site block"
	case "tls":
		where = "in a tls block"
	}
	return p.errf("unknown directive %q %s (known: %s)", got, where, strings.Join(known, ", "))
}

// editDistance is the Levenshtein distance, only ever called on
// directive-length strings.
func editDistance(a, b string) int {
	prev := make([]int, len(b)+1)
	cur := make([]int, len(b)+1)
	for j := range len(b) + 1 {
		prev[j] = j
	}
	for i := 1; i <= len(a); i++ {
		cur[0] = i
		for j := 1; j <= len(b); j++ {
			cost := 1
			if a[i-1] == b[j-1] {
				cost = 0
			}
			cur[j] = min(prev[j]+1, cur[j-1]+1, prev[j-1]+cost)
		}
		prev, cur = cur, prev
	}
	return prev[len(b)]
}

// splitFields splits one config line into arguments: whitespace
// separates, double quotes group, an unquoted # starts a comment.
func splitFields(line string) ([]string, error) {
	var fields []string
	var cur strings.Builder
	inQuote, hasToken := false, false
	flush := func() {
		if hasToken {
			fields = append(fields, cur.String())
			cur.Reset()
			hasToken = false
		}
	}
	for _, r := range line {
		switch {
		case inQuote && r == '"':
			inQuote = false
		case inQuote:
			cur.WriteRune(r)
		case r == '"':
			inQuote = true
			hasToken = true // "" is a valid empty argument
		case r == '#':
			flush()
			return fields, nil
		case r == ' ' || r == '\t' || r == '\r':
			flush()
		default:
			cur.WriteRune(r)
			hasToken = true
		}
	}
	if inQuote {
		return nil, fmt.Errorf("unterminated quote")
	}
	flush()
	return fields, nil
}
