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

- Static responses arm a rolling per-write deadline
  (`deadlineWriter`, 30 s budget, renewed as writes make progress) — the
  nginx `send_timeout` model: a slow download lives, a stalled client is
  cut.
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

## Deploying before nitrokit resolves

nitrokit has no remote and no tags yet, so `go.mod` cannot require it;
the git-ignored `go.work` (this directory plus `../nitrokit`) is the only
resolution path, per the house module rules. Consequence: a Docker build,
which excludes `go.work`, cannot build nitro until nitrokit is pushed and
tagged — at which point the require gets pinned and the workspace becomes
optional. The Dockerfile lands then, not before.
