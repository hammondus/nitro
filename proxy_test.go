package main

import (
	"bufio"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"
	"time"

	"github.com/hammondus/nitrokit"
)

// proxyFor returns an httptest front-end proxying every request to
// backend, plus the headers the backend saw.
func proxyFor(t *testing.T, backend http.Handler, trust *nitrokit.ProxyTrust) (*httptest.Server, *http.Header) {
	t.Helper()
	var seen http.Header
	bs := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		seen = r.Header.Clone()
		seen.Set("Test-Backend-Host", r.Host)
		backend.ServeHTTP(w, r)
	}))
	t.Cleanup(bs.Close)
	if trust == nil {
		trust = &nitrokit.ProxyTrust{}
	}
	p, err := newProxy(strings.TrimPrefix(bs.URL, "http://"), trust, discardLogger())
	if err != nil {
		t.Fatal(err)
	}
	fs := httptest.NewServer(p)
	t.Cleanup(fs.Close)
	return fs, &seen
}

func TestProxyForwardedHeaders(t *testing.T) {
	front, seen := proxyFor(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {}), nil)

	req, _ := http.NewRequest(http.MethodGet, front.URL+"/x", nil)
	req.Host = "site.example"
	// Attacker-supplied chain: must not reach the backend.
	req.Header.Set("X-Forwarded-For", "6.6.6.6")
	resp, err := front.Client().Do(req)
	if err != nil {
		t.Fatal(err)
	}
	resp.Body.Close()

	if got := seen.Get("X-Forwarded-For"); got != "127.0.0.1" {
		t.Errorf("X-Forwarded-For = %q, want the peer only", got)
	}
	if got := seen.Get("X-Forwarded-Proto"); got != "http" {
		t.Errorf("X-Forwarded-Proto = %q", got)
	}
	if got := seen.Get("Test-Backend-Host"); got != "site.example" {
		t.Errorf("backend saw Host %q, want the original", got)
	}
}

func TestProxyTrustedProxies(t *testing.T) {
	// The test client connects from 127.0.0.1, which is in the trust
	// list, so the inbound X-Forwarded-For is believed and its rightmost
	// untrusted hop becomes the one forwarded value.
	trust, err := nitrokit.ParseTrustedProxies("127.0.0.1/32,::1/128")
	if err != nil {
		t.Fatal(err)
	}
	front, seen := proxyFor(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {}), trust)

	req, _ := http.NewRequest(http.MethodGet, front.URL, nil)
	req.Header.Set("X-Forwarded-For", "203.0.113.9, 127.0.0.1")
	resp, err := front.Client().Do(req)
	if err != nil {
		t.Fatal(err)
	}
	resp.Body.Close()
	if got := seen.Get("X-Forwarded-For"); got != "203.0.113.9" {
		t.Errorf("X-Forwarded-For = %q, want the resolved client", got)
	}
}

func TestProxyStreaming(t *testing.T) {
	release := make(chan struct{})
	backend := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/event-stream")
		fmt.Fprint(w, "data: first\n\n")
		http.NewResponseController(w).Flush()
		<-release
		fmt.Fprint(w, "data: second\n\n")
	})
	front, _ := proxyFor(t, backend, nil)

	resp, err := front.Client().Get(front.URL)
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	defer close(release)

	// The first event must arrive while the backend still holds the
	// response open — that is what streaming through the proxy means.
	got := make(chan string, 1)
	go func() {
		line, _ := bufio.NewReader(resp.Body).ReadString('\n')
		got <- line
	}()
	select {
	case line := <-got:
		if !strings.Contains(line, "first") {
			t.Errorf("first line = %q", line)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("first event never arrived: response is being buffered")
	}
}

func TestProxyUpgrade(t *testing.T) {
	// A minimal protocol upgrade: the backend hijacks, echoes one line.
	backend := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("Upgrade") != "echo" {
			http.Error(w, "no upgrade", http.StatusBadRequest)
			return
		}
		conn, rw, err := w.(http.Hijacker).Hijack()
		if err != nil {
			return
		}
		defer conn.Close()
		rw.WriteString("HTTP/1.1 101 Switching Protocols\r\nUpgrade: echo\r\nConnection: Upgrade\r\n\r\n")
		rw.Flush()
		line, _ := rw.ReadString('\n')
		rw.WriteString("echo: ")
		rw.WriteString(line)
		rw.Flush()
	})
	front, _ := proxyFor(t, backend, nil)

	u, _ := url.Parse(front.URL)
	conn, err := net.Dial("tcp", u.Host)
	if err != nil {
		t.Fatal(err)
	}
	defer conn.Close()
	fmt.Fprintf(conn, "GET / HTTP/1.1\r\nHost: %s\r\nUpgrade: echo\r\nConnection: Upgrade\r\n\r\n", u.Host)
	br := bufio.NewReader(conn)
	status, err := br.ReadString('\n')
	if err != nil || !strings.Contains(status, "101") {
		t.Fatalf("status = %q, err %v", status, err)
	}
	for { // skip response headers
		line, err := br.ReadString('\n')
		if err != nil {
			t.Fatal(err)
		}
		if line == "\r\n" {
			break
		}
	}
	fmt.Fprint(conn, "hello\n")
	reply, err := br.ReadString('\n')
	if err != nil || reply != "echo: hello\n" {
		t.Fatalf("reply = %q, err %v", reply, err)
	}
}

func TestProxyDeadUpstream(t *testing.T) {
	// A port that nothing listens on: refused connection, fast 502.
	l, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	target := l.Addr().String()
	l.Close()

	p, err := newProxy(target, &nitrokit.ProxyTrust{}, discardLogger())
	if err != nil {
		t.Fatal(err)
	}
	front := httptest.NewServer(p)
	defer front.Close()

	resp, err := front.Client().Get(front.URL)
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusBadGateway {
		t.Errorf("dead upstream = %d, want 502", resp.StatusCode)
	}
	body, _ := io.ReadAll(resp.Body)
	if !strings.Contains(string(body), "bad gateway") {
		t.Errorf("body = %q", body)
	}
}

func TestUpstreamURL(t *testing.T) {
	tests := []struct {
		in, want string
		wantErr  bool
	}{
		{in: "localhost:9000", want: "http://localhost:9000"},
		{in: "http://10.0.0.5:8000", want: "http://10.0.0.5:8000"},
		{in: "https://internal.example", want: "https://internal.example"},
		{in: "http://up:80/", want: "http://up:80"},
		{in: "ftp://up:21", wantErr: true},
		{in: "http://up/path", wantErr: true},
		{in: "://bad", wantErr: true},
	}
	for _, tt := range tests {
		u, err := upstreamURL(tt.in)
		if tt.wantErr {
			if err == nil {
				t.Errorf("upstreamURL(%q): no error", tt.in)
			}
			continue
		}
		if err != nil {
			t.Errorf("upstreamURL(%q): %v", tt.in, err)
			continue
		}
		if u.String() != tt.want {
			t.Errorf("upstreamURL(%q) = %q, want %q", tt.in, u, tt.want)
		}
	}
}
