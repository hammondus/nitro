# Two-stage build for nitro. The build stage runs on the deploy host under
# `docker compose up --build`, so `go build` produces the host's arch with
# no cross-compile here; `make release` covers the cross-compiled binaries.
# nitrokit resolves from the module proxy, so the build needs network.

FROM golang:1.27 AS build
WORKDIR /src

# Dependencies first, so this layer caches across ordinary source edits.
COPY go.mod go.sum ./
RUN go mod download

COPY . .
# Static and stripped: no libc on the target, and debug info stays on the
# dev machine (same flags as the Makefile's release target).
RUN CGO_ENABLED=0 go build -trimpath -ldflags='-s -w' -o /out/nitro .

# distroless has no shell, so the certificate cache is created with the
# right owner here. A named volume mounted over an empty root-owned
# directory is not writable by the unprivileged process. 65532 is
# distroless's nonroot user.
FROM alpine:3 AS prep
RUN mkdir -p /var/lib/nitro && chown 65532:65532 /var/lib/nitro

# The Debian release is in the tag deliberately: a floating
# `static:nonroot` would follow Google onto the next Debian major without
# this file changing. A tag rather than a digest, so a rebuild still picks
# up CA and tzdata updates within Debian 13.
FROM gcr.io/distroless/static-debian13:nonroot
COPY --from=build /out/nitro /nitro
COPY --from=prep --chown=65532:65532 /var/lib/nitro /var/lib/nitro

# Layout the compose file mounts into:
#   /etc/nitro/nitro.conf  config (read-only bind mount)
#   /srv                   site roots (read-only bind mount)
#   /var/lib/nitro         tls { cache } (named volume)
#
# Ports below 1024: Docker sets ip_unprivileged_port_start=0 inside
# containers, so the nonroot user can bind :80 and :443 for a tls
# deployment without any capability.
EXPOSE 8080 80 443

# The probe dials the config's listen port over plain HTTP. Under a tls
# block that port speaks TLS, and the probe fails; see DESIGN-DECISIONS.md.
HEALTHCHECK --interval=30s --timeout=5s --start-period=5s \
  CMD ["/nitro", "-c", "/etc/nitro/nitro.conf", "-healthcheck"]

# Flags go in CMD, not ENTRYPOINT, so compose can replace them with
# `command:` without repeating the binary.
ENTRYPOINT ["/nitro"]
CMD ["-c", "/etc/nitro/nitro.conf"]
