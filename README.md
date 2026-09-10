# nitro

nitro is a general-purpose web server: static files, virtual hosts, a
reverse proxy, redirects, and its own TLS termination, configured by one
small file. Think of a cut-down nginx that is a single static binary.
It is built on [nitrokit](https://github.com/hammondus/nitrokit) and has
no other dependencies.

## Quick start

With no config file, nitro serves the current directory on
`127.0.0.1:8080`:

```
nitro
```

Zero-config flags:

- `-addr :8080` — listen address. The default binds loopback only;
  widening it to the network is a deliberate act.
- `-root /some/dir` — directory to serve.
- `-listing` — render directory listings. Without it, a directory with
  no `index.html` is a 404, never a file list.

These flags apply only to zero-config mode. When a config file is in
use they are rejected: the file is the single source of truth.

## Config file

nitro reads `./nitro.conf` when it exists; `-c path` names another
file. The format is one directive per line, `#` comments, double quotes
around a value containing spaces, and `{}` blocks opened on the same
line. Unknown directives and malformed values stop startup with the
file and line in the error.

```
listen :443
log stdout
tls {
    cache /data/certs
    email you@example.com
    dns route53 Z0123456789ABC
}

site example.com {
    root /srv/example
    listing on
    hsts
    header X-Frame-Options DENY
    redirect /old /new 301
    proxy /api/ localhost:9000
}

site www.example.com {
    redirect / https://example.com/ 308
}

site *.example.com {
    proxy / localhost:9100
}
```

### Global directives

- `listen ADDR` — bind address. Default `:8080`, or `:443` when a
  `tls` block exists.
- `log stdout | off | PATH` — the access log. Lifecycle and error
  messages always go to standard error, so `off` silences requests,
  not failures.
- `trusted-proxies CIDR...` — the proxies in front of nitro (a CDN,
  another load balancer). With this set, nitro resolves the real
  client address from `X-Forwarded-For` by walking from the right and
  stopping at the first untrusted hop. Unset, the TCP peer is the
  client and the header is ignored.
- `tls { ... }` — terminate TLS with automatic certificates. `cache`
  (required) is the directory certificates persist in — in Docker it
  must be a volume. `email` receives expiry notices from the CA.
  `dns route53 ZONEID` enables wildcard certificates through DNS-01;
  the solver reads the standard `AWS_*` environment variables. Running
  with a `tls` block accepts Let's Encrypt's terms of service; port 80
  is also bound, for challenges and the HTTPS redirect.

### Site directives

A `site HOST { ... }` block is one virtual host, matched by the Host
header. `*.example.com` matches one label (`a.example.com`, not
`a.b.example.com`) and does not cover its apex — list `example.com`
separately. A request matching no site gets a 421. When the config has
exactly one site, that site answers every host.

- `root DIR` — serve static files from DIR. Omit it for a proxy- or
  redirect-only site.
- `listing on | off` — render a listing for directories with no
  `index.html`: breadcrumbs, sizes, dates, sortable columns, no
  JavaScript. Default off.
- `spa` — serve `index.html` for extensionless paths that match no
  file, for client-side routing. Paths with an extension still 404.
- `redirect PREFIX TARGET [CODE]` — redirect requests under PREFIX.
  The unmatched remainder of the path and the query string carry over:
  `redirect /old /new` sends `/old/x?q=1` to `/new/x?q=1`. CODE
  defaults to 308.
- `proxy PREFIX UPSTREAM` — reverse-proxy requests under PREFIX to
  UPSTREAM (`host:port`, or an `http://` / `https://` URL with no
  path). The request path is passed through unchanged. WebSocket
  upgrades and streaming responses (server-sent events) pass through.
  The upstream sees the original `Host`, `X-Forwarded-Proto`, and a
  single-value `X-Forwarded-For` holding the client address nitro
  itself resolved — a backend needs no trust list beyond nitro.
- `header NAME VALUE` — set an extra response header on everything the
  site serves. Repeatable.
- `hsts` — send `Strict-Transport-Security` (one year). Only valid
  with a `tls` block: the TLS terminator owns that policy.

Route prefixes match on segment boundaries — `/old` matches `/old` and
`/old/x`, never `/older` — and the longest matching prefix wins.
Requests matching no route fall through to `root`.

### What nitro serves and refuses

- Every file has one URL. `/a//b`, `/a/./b` and `/a/../b` redirect to
  the clean path; a directory redirects to its trailing-slash form and a
  file redirects away from one; and `/dir/index.html` redirects to
  `/dir/`, because the index is the directory's content rather than a
  page beside it. All are 301.
- Any path containing a dot-prefixed segment (`.git`, `.env`,
  `.DS_Store`) is a 404, identical to an absent file, and listings
  omit dotfiles.
- HTML responses are `Cache-Control: no-cache` (stored, revalidated
  every time); other files get `public, max-age=3600`. Files on disk
  are unhashed, so nothing is ever marked immutable.
- `GET /healthz` from a loopback peer answers the built-in liveness
  probe; `nitro -healthcheck` backs a Docker `HEALTHCHECK` with it.
  From anywhere else, `/healthz` is an ordinary path.

## Operations

Restart to apply config changes; there is no reload signal. Shutdown
on SIGINT or SIGTERM drains in-flight requests for up to 10 seconds
(nitrokit's lifecycle).

`make build` builds for the dev machine, `make test` runs vet and the
tests, `make release` cross-compiles stripped static binaries for
linux/arm64, linux/amd64, macOS, and Windows into `dist/`.

## Docker

The Dockerfile builds nitro from source into a distroless image, and
`compose.yaml` runs it. Inside the container nitro reads
`/etc/nitro/nitro.conf`, serves site roots from `/srv`, and keeps
certificates in a volume at `/var/lib/nitro`.

To deploy, put `nitro.conf` and a `sites/` directory next to
`compose.yaml` on the host, then run `make deploy`. The config must bind
a wildcard address (`listen :8080`, not `127.0.0.1:8080`), because a
loopback bind is unreachable through the port mapping. For a TLS
deployment, publish ports 80 and 443 instead of 8080 and point `tls {
cache }` at `/var/lib/nitro`; `compose.yaml` shows both.

The image's health check dials the listen port over plain HTTP, so it
reports unhealthy under a `tls` block. See DESIGN-DECISIONS.md.

## Developing against nitrokit

`go.mod` pins a released nitrokit. To work against an unreleased change
in `../nitrokit`, add a git-ignored `go.work` listing `.` and
`../nitrokit`; the Docker build ignores it and uses the pin.
