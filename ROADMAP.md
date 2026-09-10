# Roadmap

Server-shaped features live here, not in nitrokit (see nitrokit's
DESIGN-DECISIONS.md for the boundary). In likely order:

## Config reload

Apply a changed config without dropping connections. Today the answer
is restart: nitrokit's `Run` drains for up to 10 s, which is fine for a
single-tenant server but still a visible gap next to `nginx -s reload`.

Shape to explore when work starts:

- Trigger: SIGHUP is the convention operators expect. No file watching
  — an explicit signal is auditable, a watcher fires on half-written
  files.
- Semantics: parse the new file fully before touching anything; a
  malformed config logs an error and keeps the old one running (the
  nginx behaviour operators rely on). Startup keeps failing fast on a
  bad config — only reload is forgiving.
- Swappable state is the site table, routes, headers, and log target —
  an atomic pointer swap. The listeners and the TLS manager are not
  swappable without a restart; changing `listen` or the `tls` block
  reports "restart required" rather than half-applying.

## Load balancing

`proxy` takes one upstream per prefix today. Multiple upstreams with
health-aware selection is the next real gap next to nginx, and it is
config-driven, so it lands here. Needs a decision on how little to
build: round-robin with a dead-upstream backoff covers the house use
cases; hashing and weights probably never earn their config surface.

## Inherited from nitrokit

Compression and (if ever) HTTP/3 arrive by upgrading nitrokit — see
its ROADMAP.md.

## Health check under TLS

`nitrokit.HealthProbe` dials plain HTTP, so the Docker `HEALTHCHECK`
fails when a `tls` block is configured. Needs a nitrokit change: probe
:80 (which `RunTLS` always binds) or speak TLS to the listen port.
