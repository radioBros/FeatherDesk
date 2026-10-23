# syntax=docker/dockerfile:1.7
# SPDX-License-Identifier: AGPL-3.0-only
#
# FeatherDesk image (DEP-001): one static binary with the SPA embedded, on
# distroless/static nonroot. Build: `make image` or
#   docker buildx build --platform linux/amd64,linux/arm64 \
#     --build-arg VERSION=$(git describe --tags --always) --build-arg COMMIT=$(git rev-parse --short HEAD) .
#
# The node and Go stages run on the BUILD platform and cross-compile
# (CGO_ENABLED=0), so an arm64 image never runs pnpm or go under QEMU.
#
# Base digests resolved from the registries on 2026-10-21; node (24.22.1) and distroless
# (ca-certificates 20250419+deb13u1, eMudhra roots removed) bumped 2026-10-23 (bump in a reviewed PR):
#   node:24-alpine                          sha256:06d7adf3e0ef61a13f1d28682726366f97a15aa1888c934d3ecec86c33918b53
#   golang:1.27-alpine                      sha256:f2c872bcee949b7be9c490efd02b48a8a2fbf947cdf533f1c58525ec9d8170fd
#   gcr.io/distroless/static-debian13:nonroot sha256:d62067879de890a1def9da0e7766b7d20810a5e5555cef8506b752437d7a3e88

ARG PNPM_VERSION=11.0.8

# ---------------------------------------------------------------- 1. web ----
FROM --platform=$BUILDPLATFORM node:24-alpine@sha256:06d7adf3e0ef61a13f1d28682726366f97a15aa1888c934d3ecec86c33918b53 AS web
ARG PNPM_VERSION
ENV COREPACK_ENABLE_DOWNLOAD_PROMPT=0 \
    PNPM_HOME=/pnpm \
    CI=true
RUN corepack enable && corepack prepare pnpm@${PNPM_VERSION} --activate
WORKDIR /src/web
COPY web/package.json web/pnpm-lock.yaml web/pnpm-workspace.yaml ./
RUN --mount=type=cache,id=pnpm-store,target=/pnpm/store \
    pnpm config set store-dir /pnpm/store && \
    pnpm install --frozen-lockfile
COPY web/ ./
RUN pnpm build

# ---------------------------------------------------------------- 2. go -----
FROM --platform=$BUILDPLATFORM golang:1.27-alpine@sha256:f2c872bcee949b7be9c490efd02b48a8a2fbf947cdf533f1c58525ec9d8170fd AS build
ARG TARGETOS
ARG TARGETARCH
ARG VERSION=dev
ARG COMMIT=unknown
WORKDIR /src
COPY go.mod go.sum ./
RUN --mount=type=cache,id=gomod,target=/go/pkg/mod go mod download
COPY . .
# The SPA from stage 1 replaces whatever web/dist the context carried (none:
# .dockerignore excludes it) before go:embed picks it up.
RUN rm -rf web/dist
COPY --from=web /src/web/dist ./web/dist
RUN --mount=type=cache,id=gomod,target=/go/pkg/mod \
    --mount=type=cache,id=gobuild,target=/root/.cache/go-build \
    CGO_ENABLED=0 GOOS=$TARGETOS GOARCH=$TARGETARCH \
    go build -trimpath -buildvcs=false \
      -ldflags "-s -w -buildid= -X main.version=${VERSION} -X main.commit=${COMMIT}" \
      -o /out/featherdesk ./cmd/featherdesk
# /data must exist in the image, owned by nonroot, so a fresh named volume
# inherits a writable directory (distroless has no shell to mkdir at runtime).
RUN mkdir -p /out/data && chmod 0750 /out/data

# ---------------------------------------------------------------- 3. run ----
FROM gcr.io/distroless/static-debian13:nonroot@sha256:d62067879de890a1def9da0e7766b7d20810a5e5555cef8506b752437d7a3e88
ARG VERSION=dev
ARG COMMIT=unknown
LABEL org.opencontainers.image.title="FeatherDesk" \
      org.opencontainers.image.description="Lightweight help desk: one container, SQLite or Postgres, embedded SPA" \
      org.opencontainers.image.source="https://github.com/radioBros/FeatherDesk" \
      org.opencontainers.image.vendor="RadioBros" \
      org.opencontainers.image.authors="RadioBros (https://radiobros.com/)" \
      org.opencontainers.image.url="https://radiobros.com/" \
      org.opencontainers.image.licenses="AGPL-3.0-only" \
      org.opencontainers.image.version="${VERSION}" \
      org.opencontainers.image.revision="${COMMIT}"
COPY --from=build /out/featherdesk /featherdesk
COPY --from=build --chown=65532:65532 /out/data /data
# No GOMEMLIMIT/GOGC here: serve derives GOMEMLIMIT from the cgroup limit (75%).
ENV FD_DATA_DIR=/data \
    FD_LISTEN_ADDR=:8080
USER nonroot:nonroot
VOLUME ["/data"]
EXPOSE 8080
HEALTHCHECK --interval=30s --timeout=5s --start-period=10s --retries=3 \
  CMD ["/featherdesk", "healthcheck"]
ENTRYPOINT ["/featherdesk"]
CMD ["serve"]
