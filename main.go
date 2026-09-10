// nitro is a general-purpose web server: static files, virtual hosts, a
// reverse proxy, redirects, and its own TLS termination, configured by
// one nitro.conf. With no config file it serves the current directory on
// 127.0.0.1:8080. See README.md for the config reference.
package main

import (
	"context"
	"flag"
	"fmt"
	"log/slog"
	"net/http"
	"os"

	"github.com/hammondus/nitrokit"
)

func main() {
	var (
		confPath    = flag.String("c", "", "config file (default ./nitro.conf when it exists)")
		addr        = flag.String("addr", "127.0.0.1:8080", "listen address (zero-config mode only)")
		root        = flag.String("root", ".", "directory to serve (zero-config mode only)")
		listing     = flag.Bool("listing", false, "render directory listings (zero-config mode only)")
		healthcheck = flag.Bool("healthcheck", false, "probe the running server and exit")
	)
	flag.Parse()

	cfg, err := loadConfig(*confPath, *addr, *root, *listing)
	if err != nil {
		fatal(err)
	}
	if err := cfg.validate(); err != nil {
		fatal(err)
	}

	// Backs a Docker HEALTHCHECK: the container runs this same binary
	// with -healthcheck and exits by the probe's error.
	if *healthcheck {
		if err := nitrokit.HealthProbe(cfg.Listen); err != nil {
			fatal(err)
		}
		return
	}

	logger, err := accessLogger(cfg.Log)
	if err != nil {
		fatal(err)
	}
	srvHandler, err := buildServer(cfg, slog.Default())
	if err != nil {
		fatal(err)
	}
	handler := http.Handler(srvHandler)
	if logger != nil {
		handler = nitrokit.AccessLog(logger, handler)
	}

	srv := nitrokit.NewServer(cfg.Listen, handler)
	// A proxied stream (SSE, a long download) must be able to outlive
	// any constant, so the global write timeout goes: static responses
	// run under nitrokit.WriteBudget instead (see writeBudget), and
	// upstream stalls are bounded by proxyTransport.
	srv.WriteTimeout = 0

	slog.Info("listening", "addr", cfg.Listen, "tls", cfg.TLS != nil)
	if cfg.TLS != nil {
		err = runTLS(cfg, srv)
	} else {
		err = nitrokit.Run(context.Background(), srv)
	}
	if err != nil {
		fatal(err)
	}
	slog.Info("shutdown complete")
}

// loadConfig resolves the one config source: an explicit -c path, else
// ./nitro.conf when it exists, else zero-config mode built from the dev
// flags. A config file and the dev flags never merge — with a file in
// use the flags are rejected, so there is exactly one source of truth.
func loadConfig(confPath, addr, root string, listing bool) (*Config, error) {
	path := confPath
	if path == "" {
		if _, err := os.Stat("nitro.conf"); err == nil {
			path = "nitro.conf"
		}
	}
	if path == "" {
		return zeroConfig(addr, root, listing), nil
	}

	var conflict []string
	flag.Visit(func(f *flag.Flag) {
		switch f.Name {
		case "addr", "root", "listing":
			conflict = append(conflict, "-"+f.Name)
		}
	})
	if len(conflict) > 0 {
		return nil, fmt.Errorf("%v applies only to zero-config mode; with %s in use, set it there instead", conflict, path)
	}

	data, err := os.ReadFile(path)
	if err != nil {
		return nil, err
	}
	return parseConfig(path, data)
}

// accessLogger builds the logger AccessLog writes through, or nil when
// access logging is off. Lifecycle and error messages use slog.Default
// regardless: switching the access log off must not silence failures.
func accessLogger(target string) (*slog.Logger, error) {
	switch target {
	case "off":
		return nil, nil
	case "stdout":
		return slog.New(slog.NewTextHandler(os.Stdout, nil)), nil
	}
	f, err := os.OpenFile(target, os.O_APPEND|os.O_CREATE|os.O_WRONLY, 0o644)
	if err != nil {
		return nil, fmt.Errorf("access log: %w", err)
	}
	return slog.New(slog.NewTextHandler(f, nil)), nil
}

// runTLS maps the tls{} block onto nitrokit.RunTLS: certificates for
// every site's host, with wildcards issued through the Route 53 solver.
func runTLS(cfg *Config, srv *http.Server) error {
	a := nitrokit.ACME{
		Hosts:    cfg.tlsHosts(),
		CacheDir: cfg.TLS.CacheDir,
		Email:    cfg.TLS.Email,
	}
	if cfg.TLS.Route53Zone != "" {
		solver, err := nitrokit.NewRoute53(cfg.TLS.Route53Zone)
		if err != nil {
			return err
		}
		a.DNS = solver
	}
	return nitrokit.RunTLS(context.Background(), srv, a)
}

func fatal(err error) {
	fmt.Fprintln(os.Stderr, "nitro:", err)
	os.Exit(1)
}
