package main

import (
	"slices"
	"strings"
	"testing"
)

func TestZeroConfig(t *testing.T) {
	cfg := zeroConfig("127.0.0.1:8080", t.TempDir(), false)
	if err := cfg.validate(); err != nil {
		t.Fatal(err)
	}
	if cfg.Listen != "127.0.0.1:8080" || cfg.Log != "stdout" || len(cfg.Sites) != 1 {
		t.Errorf("zero config: %+v", cfg)
	}
}

func TestValidateDefaults(t *testing.T) {
	dir := t.TempDir()
	cfg := &Config{Sites: []*Site{{Host: "a.com", Root: dir}}}
	if err := cfg.validate(); err != nil {
		t.Fatal(err)
	}
	if cfg.Listen != ":8080" {
		t.Errorf("default listen = %q", cfg.Listen)
	}

	cfg = &Config{TLS: &TLSConfig{CacheDir: dir}, Sites: []*Site{{Host: "a.com", Root: dir}}}
	if err := cfg.validate(); err != nil {
		t.Fatal(err)
	}
	if cfg.Listen != ":443" {
		t.Errorf("default TLS listen = %q", cfg.Listen)
	}
}

func TestValidateErrors(t *testing.T) {
	dir := t.TempDir()
	tests := []struct {
		name string
		cfg  *Config
		want string
	}{
		{"no sites", &Config{}, "no site blocks"},
		{"dup host", &Config{Sites: []*Site{{Host: "a.com", Root: dir}, {Host: "a.com", Root: dir}}}, "defined twice"},
		{"empty site", &Config{Sites: []*Site{{Host: "a.com"}}}, "would serve nothing"},
		{"listing without root", &Config{Sites: []*Site{{Host: "a.com", Listing: true, Routes: []Route{{Prefix: "/", Kind: routeProxy, Target: "x:1"}}}}}, "listing on but no root"},
		{"hsts without tls", &Config{Sites: []*Site{{Host: "a.com", Root: dir, HSTS: true}}}, "hsts"},
		{"tls without cache", &Config{TLS: &TLSConfig{}, Sites: []*Site{{Host: "a.com", Root: dir}}}, "cache directory"},
		{"wildcard without dns", &Config{TLS: &TLSConfig{CacheDir: dir}, Sites: []*Site{{Host: "*.a.com", Root: dir}}}, "dns solver"},
		{"missing root", &Config{Sites: []*Site{{Host: "a.com", Root: dir + "/nope"}}}, "nope"},
		{"dup prefix", &Config{Sites: []*Site{{Host: "a.com", Routes: []Route{
			{Prefix: "/x", Kind: routeRedirect, Target: "/a", Code: 308},
			{Prefix: "/x", Kind: routeProxy, Target: "up:1"},
		}}}}, "two routes for prefix /x"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			err := tt.cfg.validate()
			if err == nil {
				t.Fatalf("no error, want %q", tt.want)
			}
			if !strings.Contains(err.Error(), tt.want) {
				t.Errorf("error %q does not contain %q", err, tt.want)
			}
		})
	}
}

func TestTLSHosts(t *testing.T) {
	dir := t.TempDir()
	cfg := &Config{
		TLS: &TLSConfig{CacheDir: dir, Route53Zone: "Z1"},
		Sites: []*Site{
			{Host: "a.com", Root: dir},
			{Host: "www.a.com", Routes: []Route{{Prefix: "/", Kind: routeRedirect, Target: "https://a.com/", Code: 308}}},
			{Host: "*.a.com", Root: dir},
		},
	}
	if err := cfg.validate(); err != nil {
		t.Fatal(err)
	}
	want := []string{"a.com", "www.a.com", "*.a.com"}
	if got := cfg.tlsHosts(); !slices.Equal(got, want) {
		t.Errorf("tlsHosts = %v, want %v", got, want)
	}
}
