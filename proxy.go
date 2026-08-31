package main

import (
	"context"
	"errors"
	"log/slog"
	"net"
	"net/http"
	"net/http/httputil"
	"time"

	"github.com/hammondus/nitrokit"
)

// proxyTransport is shared by every proxy route: connection pooling
// works per upstream host inside one transport. A dead upstream is a
// fast 502 (dial timeout), and a connected-but-silent one is cut when
// it sends no response header inside the budget — generous because
// long-polling holds the request open on purpose. There is no overall
// response timeout: bounding body duration would cut streams, which is
// the same reason the server's WriteTimeout is 0.
var proxyTransport = &http.Transport{
	DialContext:           (&net.Dialer{Timeout: 5 * time.Second}).DialContext,
	ResponseHeaderTimeout: 60 * time.Second,
	ForceAttemptHTTP2:     true,
	MaxIdleConnsPerHost:   16,
	IdleConnTimeout:       90 * time.Second,
}

// newProxy builds the reverse proxy for one route. target is the value
// from the config file, already vetted by the parser.
func newProxy(target string, trust *nitrokit.ProxyTrust, logger *slog.Logger) (http.Handler, error) {
	upstream, err := upstreamURL(target)
	if err != nil {
		return nil, err
	}
	return &httputil.ReverseProxy{
		Rewrite: func(pr *httputil.ProxyRequest) {
			pr.SetURL(upstream)
			pr.SetXForwarded()
			// nitro is the trust boundary: it resolves the real client
			// itself (through trusted-proxies, when configured) and hands
			// the upstream exactly one X-Forwarded-For value, so a
			// backend needs no trust list of its own beyond nitro. The
			// inbound header is attacker-controlled and never forwarded.
			if ip := trust.ClientIP(pr.In); ip.IsValid() {
				pr.Out.Header.Set("X-Forwarded-For", ip.String())
			}
			// SetURL rewrites the Host header to the upstream's; the
			// original is what a name-routing backend needs.
			pr.Out.Host = pr.In.Host
		},
		Transport: proxyTransport,
		ErrorHandler: func(w http.ResponseWriter, r *http.Request, err error) {
			// A client that went away mid-proxy is not an upstream fault.
			if errors.Is(err, context.Canceled) {
				return
			}
			logger.Error("proxy", "upstream", upstream.Host, "path", r.URL.Path, "err", err)
			http.Error(w, "bad gateway", http.StatusBadGateway)
		},
	}, nil
}
