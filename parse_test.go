package main

import (
	"strings"
	"testing"
)

func TestParseFullConfig(t *testing.T) {
	conf := `
# global
listen :443
log /var/log/nitro.log
trusted-proxies 10.0.0.0/8 ::1/128
tls {
	cache /data/certs
	email you@example.com
	dns route53 Z0123456789ABC
}

site example.com {
	root /srv/example
	listing on
	spa
	hsts
	header X-Frame-Options DENY
	header X-Robots-Tag "noindex, nofollow"
	redirect /old /new 301
	proxy /api/ localhost:9000
}

site www.example.com {
	redirect / https://example.com/ 308
}

site *.example.com {
	proxy / localhost:9100
}
`
	cfg, err := parseConfig("nitro.conf", []byte(conf))
	if err != nil {
		t.Fatal(err)
	}
	if cfg.Listen != ":443" || cfg.Log != "/var/log/nitro.log" {
		t.Errorf("globals: listen=%q log=%q", cfg.Listen, cfg.Log)
	}
	if cfg.Trusted == nil {
		t.Error("trusted-proxies not parsed")
	}
	if cfg.TLS == nil || cfg.TLS.CacheDir != "/data/certs" || cfg.TLS.Email != "you@example.com" || cfg.TLS.Route53Zone != "Z0123456789ABC" {
		t.Errorf("tls block: %+v", cfg.TLS)
	}
	if len(cfg.Sites) != 3 {
		t.Fatalf("got %d sites, want 3", len(cfg.Sites))
	}

	s := cfg.Sites[0]
	if s.Host != "example.com" || s.Root != "/srv/example" || !s.Listing || !s.SPA || !s.HSTS {
		t.Errorf("site 0: %+v", s)
	}
	wantHeaders := []Header{{"X-Frame-Options", "DENY"}, {"X-Robots-Tag", "noindex, nofollow"}}
	if len(s.Headers) != 2 || s.Headers[0] != wantHeaders[0] || s.Headers[1] != wantHeaders[1] {
		t.Errorf("headers: %+v", s.Headers)
	}
	if len(s.Routes) != 2 {
		t.Fatalf("site 0 routes: %+v", s.Routes)
	}
	if r := s.Routes[0]; r.Kind != routeRedirect || r.Prefix != "/old" || r.Target != "/new" || r.Code != 301 {
		t.Errorf("redirect route: %+v", r)
	}
	if r := s.Routes[1]; r.Kind != routeProxy || r.Prefix != "/api/" || r.Target != "localhost:9000" {
		t.Errorf("proxy route: %+v", r)
	}

	if cfg.Sites[1].Routes[0].Code != 308 {
		t.Errorf("www redirect code: %d", cfg.Sites[1].Routes[0].Code)
	}
	if cfg.Sites[2].Host != "*.example.com" {
		t.Errorf("wildcard host: %q", cfg.Sites[2].Host)
	}
}

func TestParseDefaultRedirectCode(t *testing.T) {
	cfg, err := parseConfig("t", []byte("site a.com {\nredirect /x /y\n}"))
	if err != nil {
		t.Fatal(err)
	}
	if got := cfg.Sites[0].Routes[0].Code; got != 308 {
		t.Errorf("default redirect code = %d, want 308", got)
	}
}

func TestParseHostLowercased(t *testing.T) {
	cfg, err := parseConfig("t", []byte("site EXAMPLE.com {\nroot .\n}"))
	if err != nil {
		t.Fatal(err)
	}
	if cfg.Sites[0].Host != "example.com" {
		t.Errorf("host = %q", cfg.Sites[0].Host)
	}
}

func TestParseErrors(t *testing.T) {
	tests := []struct {
		name string
		conf string
		want string // substring of the error
	}{
		{"unknown global", "serve :8080", `unknown directive "serve"`},
		{"suggestion", "listn :8080", `did you mean "listen"`},
		{"site suggestion", "site a.com {\nlsting on\n}", `did you mean "listing"`},
		{"listen args", "listen :80 :443", "listen takes 1"},
		{"bad trusted", "trusted-proxies 10.0.0.1", "trusted-proxies"},
		{"unclosed site", "site a.com {\nroot .", "never closed"},
		{"stray brace", "}", "} without an open block"},
		{"site needs brace", "site a.com", "opens a block"},
		{"bad host port", "site a.com:80 {\n}", "no scheme, port, or path"},
		{"bad wildcard", "site a.*.com {\n}", "leading *.domain"},
		{"empty listing", "site a.com {\nlisting maybe\n}", "not on or off"},
		{"spa args", "site a.com {\nspa on\n}", "spa takes no arguments"},
		{"redirect code", "site a.com {\nredirect /a /b 200\n}", "not a redirect status"},
		{"redirect prefix", "site a.com {\nredirect old /new\n}", "must start with /"},
		{"proxy scheme", "site a.com {\nproxy / ftp://up:21\n}", "scheme must be http or https"},
		{"proxy path", "site a.com {\nproxy / localhost:9000/app\n}", "not supported"},
		{"dup tls", "tls {\ncache /a\n}\ntls {\ncache /b\n}", "defined twice"},
		{"tls provider", "tls {\ndns cloudflare Z1\n}", "unknown provider"},
		{"unterminated quote", `site a.com {` + "\n" + `header X "broken` + "\n}", "unterminated quote"},
		{"header args", "site a.com {\nheader X-One\n}", "header takes 2"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			_, err := parseConfig("t", []byte(tt.conf))
			if err == nil {
				t.Fatalf("no error, want %q", tt.want)
			}
			if !strings.Contains(err.Error(), tt.want) {
				t.Errorf("error %q does not contain %q", err, tt.want)
			}
		})
	}
}

func TestParseErrorLineNumbers(t *testing.T) {
	_, err := parseConfig("nitro.conf", []byte("listen :80\n\nbogus x\n"))
	if err == nil || !strings.HasPrefix(err.Error(), "nitro.conf:3:") {
		t.Errorf("error = %v, want nitro.conf:3: prefix", err)
	}
}

func TestSplitFields(t *testing.T) {
	tests := []struct {
		in   string
		want []string
	}{
		{"a b c", []string{"a", "b", "c"}},
		{"  a\tb  ", []string{"a", "b"}},
		{`a "b c" d`, []string{"a", "b c", "d"}},
		{"a # comment", []string{"a"}},
		{"# whole line", nil},
		{"", nil},
		{`a "#not comment"`, []string{"a", "#not comment"}},
		{`a ""`, []string{"a", ""}},
	}
	for _, tt := range tests {
		got, err := splitFields(tt.in)
		if err != nil {
			t.Errorf("splitFields(%q): %v", tt.in, err)
			continue
		}
		if len(got) != len(tt.want) {
			t.Errorf("splitFields(%q) = %q, want %q", tt.in, got, tt.want)
			continue
		}
		for i := range got {
			if got[i] != tt.want[i] {
				t.Errorf("splitFields(%q) = %q, want %q", tt.in, got, tt.want)
				break
			}
		}
	}
}
