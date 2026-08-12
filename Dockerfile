# ── frontend build ──────────────────────────────────────────────────────────
# Builds the React SPA (web/frontend) into web/dist, which the Go binary then
# embeds. Isolated first stage so the Go image needs no Node toolchain.
FROM node:22-alpine AS web
WORKDIR /src/web/frontend
COPY web/frontend/package.json web/frontend/package-lock.json ./
RUN npm ci
COPY web/frontend/ ./
RUN npm run build

# ── go build ──────────────────────────────────────────────────────────────────
FROM golang:1.25-alpine AS builder

ARG VERSION=1.0.0

WORKDIR /build

COPY go.mod go.sum ./
RUN go mod download

COPY . .
# Overwrite the committed web/dist/.gitkeep placeholder with the real UI built
# above, then compile — //go:embed all:dist picks up the freshly built assets.
COPY --from=web /src/web/dist ./web/dist
RUN CGO_ENABLED=0 GOOS=linux go build \
    -ldflags="-s -w -X main.Version=${VERSION}" \
    -o /gamarr ./cmd/gamarr/

# ── rom-converto fetch ────────────────────────────────────────────────────────
# The ROM/disc conversion engine (internal/converto shells out to it). A single
# static-musl binary, sha256-pinned to a release asset so the image is
# reproducible. Static build → runs directly on the alpine runtime, no deps.
FROM alpine:3.21 AS converto
ARG ROM_CONVERTO_VERSION=v0.17.0
ARG ROM_CONVERTO_SHA256=24fc93e8403121aa9f39483e22ffe9f330f6a8a65db865b464ab1770600aac54
RUN apk add --no-cache curl \
 && curl -fSL -o /rom-converto \
      "https://github.com/DevYukine/rom-converto/releases/download/${ROM_CONVERTO_VERSION}/rom-converto-cli-linux-x64-musl" \
 && echo "${ROM_CONVERTO_SHA256}  /rom-converto" | sha256sum -c - \
 && chmod +x /rom-converto

FROM alpine:3.21

RUN apk add --no-cache ca-certificates tzdata p7zip && \
    adduser -D -u 1000 gamarr

COPY --from=builder /gamarr /usr/local/bin/gamarr
COPY --from=converto /rom-converto /usr/local/bin/rom-converto
COPY clamd.conf /app/clamd.conf

WORKDIR /app
EXPOSE 5001

# Runs as root by design. Gamarr writes its SQLite DB to DATA_DIR, imports into
# library volumes that are commonly root-owned, and talks to the Docker socket
# (root:docker) to start and stop the on-demand ClamAV container. Dropping to
# the unprivileged `gamarr` user breaks all three on existing deployments, so
# that has to land as a documented migration rather than a silent default.

ENTRYPOINT ["/usr/local/bin/gamarr"]
