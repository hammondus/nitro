# Design decisions

nitro is the generic web server that nitrokit was extracted to enable: a
cut-down nginx that serves static files, virtual hosts, a reverse proxy,
redirects, and its own TLS, from one small config file. The founding
decisions were made in the 2026-08-31 planning session; entries below
record them and everything decided since.

## Scope of v1 (2026-08-31)

In: static files, opt-in directory listing, virtual hosts, full reverse
proxy (WebSockets, SSE, X-Forwarded-*), redirects, SPA fallback, TLS
termination via nitrokit ACME. nitro is the eventual replacement for nginx
proxy manager, so the proxy path is not a toy.

Out of v1, deliberately: per-site rate limiting (nitrokit.Limiter is ready
when a deployment needs it), regex rewrites, basic auth, mixed HTTP/HTTPS
vhosts, SIGHUP reload (restart only), response compression, per-site body
caps.

## nitro composes nitrokit unchanged

nitro uses `NewServer`, `Run`, `RunTLS`, `ACME`, `NewRoute53`, `AccessLog`,
`WriteBudget`, `ParseTrustedProxies`, `Healthz`,
`NoCache`, `HSTS`, and `HealthProbe` as shipped. That zero-change fit is
the result the extraction aimed for, and worth recording.

Deliberately not used:

- `SecureHeaders` — a generic server cannot impose one CSP on arbitrary
  sites it merely serves or proxies. Per-site `header` directives are the
  tool instead.
- `Assets` / `ParseTemplates` — embedded-asset fingerprinting has no
  meaning for serving arbitrary directories; nitro's one template (the
  directory listing) is embedded directly.

One future nitrokit change flagged, not made: `RunTLS` hardcodes port 80
to ACME challenges plus the HTTPS redirect. Mixed HTTP-only and HTTPS
vhosts in one nitro would need an `ACME.HTTPFallback http.Handler`
(mirroring `autocert.Manager.HTTPHandler(fallback)`) — additive, and not
worth adding until a real mixed-mode deployment exists.

## Custom config format, hand-written parser

Decided 2026-08-31 over TOML (first real third-party dependency in the
stack) and JSON (no comments, hostile to hand-editing). Caddyfile-style
blocks read like a server config, and the parser is a few hundred
auditable lines that nitro owns. Unknown directives and malformed values
are startup errors, never warnings — nitrokit's env philosophy. Config
lookup is one rule: `./nitro.conf` if present, `-c` overrides, neither
means zero-config mode.

Flags and a config file are mutually exclusive. `-addr`, `-root`, and
`-listing` exist so quick dev use never needs a file; when a file is in
use they are rejected rather than merged, so there is exactly one source
of truth and no precedence table to document.

## Zero-config mode binds loopback

`nitro` with no config serves the current directory on `127.0.0.1:8080`.
Loopback, not all interfaces: zero-config mode is a dev convenience, and
binding the LAN by default would expose whatever directory the command
happens to run in. `-addr` widens it deliberately.

## No directory listing by default

Go's `http.FileServer` renders a listing whenever `index.html` is absent,
which is why nitro does not use it. nitro's static handler 404s instead;
`listing on` (or `-listing`) opts a site in. The listing page is one
embedded server-rendered template: breadcrumbs, size and ISO date
columns, sorting via query-parameter links, no JavaScript, no external
assets.

## /dir/index.html redirects to /dir/

The static handler already held that every file has one URL: `/a//b`,
`/a/./b` and `/a/../b` redirect to the clean path, a directory redirects
to its trailing-slash form, and a file redirects away from one.
`index.html` was the gap — the same page answered at `/` and at
`/index.html`, which is the duplicate-URL problem the rest of the
canonicalisation exists to prevent.

nginx and Apache both leave the two URLs alone; Go's `http.FileServer`
redirects. nitro follows Go here, against its own "cut-down nginx"
framing, because the alternative is a rule nitro states and then breaks
in the most common case on any site.

Two details the code comments also carry. The suffix cut is
`/index.html`, not `index.html`, so a real file named `myindex.html`
survives. And the check sits after the dotfile 404, so
`/.git/index.html` stays an ordinary 404 rather than becoming a redirect
that confirms the directory is there.

The redirect happens before the filesystem is touched, matching the
other canonicalisation rules, so `/dir/index.html` redirects even when
that file is absent and lands on whatever `/dir/` itself answers.

## Dotfiles are invisible

Any path with a dot-prefixed segment is refused with 404 — the same 404
an absent file gets, so probing for `.git`, `.env`, or `.DS_Store`
reveals nothing. Listings omit them. The survey of real-world scanner
traffic behind every public server makes this the only safe default; a
future directive can open it up if a real need appears.

## Every static response revalidates; a site may override (2026-10-18)

Every file and listing is `Cache-Control: no-cache`. This replaces the
original split, which gave HTML `no-cache` and everything else
`public, max-age=3600`. The files nitro serves are unhashed, so a URL's
content changes under it on deploy. With a lifetime on assets, a fresh
page could run for up to an hour against the CSS, JavaScript, or service
worker it replaced. That is the failure the house caching rule exists to
prevent. `http.ServeContent` sends `Last-Modified`, so each revalidation
is a 304 and costs one round trip, not a transfer.

A site's `header Cache-Control` now wins over the default. Site headers
are set before the static handler runs, and the handler used to
overwrite them, so a site could not opt out. The default applies only
when nothing is set, so a stronger policy stays visible in the config
that asked for it.

## Missing MIME types are registered at startup (2026-10-18)

Go's built-in table has no entry for `.webmanifest` or the web font
types, and the distroless image ships no `/etc/mime.types`. Without
registration, `ServeContent` sniffs a manifest as `text/plain` and a
font as `application/octet-stream`. The list stays to types a static
site actually serves; anything rarer can wait for a real need.

## The proxy hands upstream one X-Forwarded-For value

nitro is the trust boundary. It resolves the real client itself —
through `trusted-proxies` when a CDN sits in front, otherwise the TCP
peer — and sends the upstream exactly that one address. The inbound
header, which is attacker-controlled, is never forwarded.

Rejected: the conventional append-to-the-chain behaviour. It would make
every backend repeat nitro's trust configuration (nitro's CIDRs plus the
CDN's) to read the chain safely, and two of the seventeen surveyed
servers got exactly that walk wrong. A backend behind nitro trusts nitro
and reads the single value with `nitrokit.ClientIP` or by hand — either
is now correct.

The upstream also sees the original `Host` (`pr.Out.Host = pr.In.Host`,
undoing `SetURL`'s rewrite) and `X-Forwarded-Proto` from
`SetXForwarded`, so name-routing backends work unchanged.

## Proxy paths pass through unchanged

`proxy /api/ localhost:9000` sends `/api/x` to the upstream as `/api/x`,
like nginx `proxy_pass` with no URI part, and an upstream URL carrying a
path is a config error rather than silently ignored. Prefix stripping is
a rewrite, and rewrites are out of v1; if a real deployment needs it, it
gets an explicit form (not a trailing-slash pun on the upstream URL,
which is nginx's least-guessable behaviour).

## Timeouts: rolling write deadline, no global WriteTimeout

`nitrokit.NewServer`'s 30 s `WriteTimeout` would cut every proxied SSE
stream and any download slower than that, so nitro takes the documented
escape hatch (the reason `NewServer` returns the plain struct) and sets
it to 0. In its place:

- Static responses run under `nitrokit.WriteBudget` (30 s budget,
  renewed as writes make progress) — the nginx `send_timeout` model: a
  slow download lives, a stalled client is cut. nitro carried its own
  copy (`deadlineWriter`) until nitrokit shipped the wrapper; retired
  2026-09-10.
- Proxied requests get no client-side write deadline — a generic proxy
  cannot bound response duration — and upstream stalls are bounded
  instead by the shared transport: 5 s dial, 60 s response-header
  timeout. 60 s rather than the planned 30 s because long-polling holds
  the request open on purpose and 60 s is nginx's own default read
  timeout.
- Streaming works without a `FlushInterval` setting:
  `httputil.ReverseProxy` flushes immediately for responses with unknown
  length, which covers SSE and chunked streams.

## Odds and ends worth a line

- **Single-site configs answer every host.** With one site there is
  nothing to disambiguate, and it keeps zero-config mode and the
  simplest file identical in behaviour. Multi-site requests matching no
  site get 421, the status defined for exactly this.
- **`/healthz` answers before routing, loopback peers only.** Docker's
  self-probe needs it on every config; remote clients asking for
  /healthz fall through to the sites, so it shadows nothing publicly.
- **Redirect and proxy prefixes match on segment boundaries** — `/old`
  matches `/old/x`, never `/older` — and duplicate prefixes in one site
  are a startup error, because the longest-prefix sort would otherwise
  pick one silently.
- **Canonical URLs by redirect.** `//a`, `/a/./b`, `/a/../b`, a file
  with a trailing slash, and a directory without one all 301 to the one
  clean form before the filesystem is touched.

## Depending on nitrokit

nitrokit has been published at `github.com/hammondus/nitrokit` since
2026-09-01, so `go.mod` pins it (v0.3.0, pinned 2026-09-10) and the Docker
build resolves it from the module proxy like any dependency. The pin
brought three indirect `golang.org/x/` lines: nitrokit's ACME support
needs `x/crypto`. nitro itself imports nothing outside the standard
library and nitrokit.

The git-ignored `go.work` (this directory plus `../nitrokit`) stays as the
development path for working against an unreleased nitrokit change. With
the file present, `go` builds against `../nitrokit`; without it
(`GOWORK=off`, or on the deploy host, where the file does not exist) it
builds against the pin. Before bumping the pin, run `make test` both ways.

Until 2026-09-10 the workspace was the only resolution path, which is why
the Dockerfile did not exist before then.

## Docker: build on the host, distroless, nonroot

- **Two-stage build on the deploy host.** `make deploy` is `git pull`
  then `docker compose up -d --build`; nothing has to be copied over.
  The cost is that the image build needs network access to the module
  proxy for nitrokit.
- **`distroless/static`, nonroot.** A static binary in an image with no
  shell and no libc, running as uid 65532. Ports below 1024 are not a
  problem: Docker sets `ip_unprivileged_port_start=0` inside containers,
  so a TLS deployment binds :80 and :443 without a capability. A host
  that overrides that sysctl cannot run the TLS mode, because `RunTLS`
  binds :80 for ACME challenges and there is no way to remap it.
- **Config and site roots are read-only bind mounts**
  (`/etc/nitro/nitro.conf`, `/srv`); the certificate cache is a named
  volume at `/var/lib/nitro`, created with the nonroot owner in a prep
  stage. A named volume mounted over an empty root-owned directory is
  unwritable by an unprivileged process, and the failure surfaces only at
  the first certificate request.
- **The `HEALTHCHECK` fails under a `tls` block.** `nitrokit.HealthProbe`
  dials the listen port over plain HTTP, and with TLS that port speaks
  TLS. The probe is correct for the plain-HTTP deployment compose defaults
  to. Fixing it means teaching `HealthProbe` about TLS, or probing :80 —
  a nitrokit change, flagged and not made.
