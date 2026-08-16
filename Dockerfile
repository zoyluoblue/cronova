# syntax=docker/dockerfile:1
# Multi-stage build: static Go binary -> distroless. See docs/DEPLOY.md "Docker".
#
#   docker build -t cronova .
#   docker build --build-arg VERSION="$(git describe --tags --always --dirty)" -t cronova .
#
# The binary embeds the web console (internal/web, embed.FS), so the final image
# is just the scheduler plus a static busybox shell. The shell is not optional:
# the executor launches EVERY task through `sh -c` (internal/executor/runner.go),
# including the pure-Go http/sql operators (`cronova run-op ...`). python/jar
# tasks need interpreters this image deliberately does not carry — pair the
# scheduler with a host-side cronova-executor for those (docs/DEPLOY.md).

ARG GO_VERSION=1.26.5

FROM golang:${GO_VERSION}-alpine AS build
WORKDIR /src
# Cache module downloads separately from source changes.
COPY go.mod go.sum ./
RUN go mod download
COPY . .
# VERSION is baked with the same ldflags contract as the Makefile / package.sh
# (-X main.version). TARGETOS/TARGETARCH make `docker buildx` cross-builds work.
ARG TARGETOS TARGETARCH
ARG VERSION=dev
RUN CGO_ENABLED=0 GOOS=${TARGETOS:-linux} GOARCH=${TARGETARCH} \
    go build -trimpath -ldflags "-s -w -X main.version=${VERSION}" \
    -o /out/cronova ./cmd/cronova

# Stage a rootfs snippet here because the distroless final stage has no shell to
# run mkdir/ln: the state-dir skeleton (chowned to nonroot on COPY, so the named
# volume's first-use copy-up hands the mount to uid 65532), the example DAGs
# (the same seeding deploy/install.sh performs), and symlinks for the busybox
# applets shell tasks may use.
RUN mkdir -p /rootfs/bin \
      /rootfs/var/lib/cronova/data \
      /rootfs/var/lib/cronova/dags \
      /rootfs/var/lib/cronova/logs \
      /rootfs/var/lib/cronova/projects \
      /rootfs/var/lib/cronova/workspaces \
      /rootfs/var/lib/cronova/backups && \
    cp dags/*.yaml /rootfs/var/lib/cronova/dags/ && \
    for a in sh ash env printf echo true false test [ cat cp mv rm mkdir rmdir \
             ls ln date sleep sed awk grep head tail tr cut sort uniq wc xargs \
             find tar gzip gunzip basename dirname wget od stat tee touch \
             readlink realpath seq expr sync mktemp; do \
      ln -s /bin/busybox "/rootfs/bin/$a"; \
    done

# busybox's musl variant is a single fully static binary — no libc to bring along.
FROM busybox:1.37.0-musl AS busybox

# distroless/static ships CA certificates (outbound webhooks/https), tzdata, and
# a nonroot user (uid 65532) — but no shell, package manager, or libc.
FROM gcr.io/distroless/static-debian12:nonroot
COPY --from=busybox /bin/busybox /bin/busybox
COPY --from=build /rootfs/bin/ /bin/
# 65532 = distroless "nonroot"; numeric so any builder resolves it.
COPY --from=build --chown=65532:65532 /rootfs/var/lib/cronova /var/lib/cronova
COPY --from=build /out/cronova /cronova

# All state lives under the single /var/lib/cronova volume. These envs also
# steer the `healthcheck`, `users`, and `backup` subcommands run via
# `docker exec` (each reads CRONOVA_* itself).
ENV CRONOVA_DB=/var/lib/cronova/data/cronova.db \
    CRONOVA_DAGS=/var/lib/cronova/dags \
    CRONOVA_LOGS=/var/lib/cronova/logs \
    CRONOVA_PROJECTS=/var/lib/cronova/projects \
    CRONOVA_WORKSPACES=/var/lib/cronova/workspaces \
    CRONOVA_KEY_FILE=/var/lib/cronova/cronova.key \
    PATH=/usr/local/bin:/usr/bin:/bin

WORKDIR /var/lib/cronova
VOLUME /var/lib/cronova
EXPOSE 8090
USER nonroot:nonroot

# `cronova healthcheck` probes /readyz itself — no curl/wget/shell required.
# It honors CRONOVA_HTTP (rewriting 0.0.0.0 to 127.0.0.1 for the probe).
HEALTHCHECK --interval=30s --timeout=5s --start-period=10s --retries=3 \
  CMD ["/cronova", "healthcheck"]

ENTRYPOINT ["/cronova"]
CMD ["serve"]
