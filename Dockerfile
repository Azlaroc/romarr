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

FROM alpine:3.21

RUN apk add --no-cache ca-certificates tzdata p7zip && \
    adduser -D -u 1000 gamarr

COPY --from=builder /gamarr /usr/local/bin/gamarr
COPY clamd.conf /app/clamd.conf

WORKDIR /app
EXPOSE 5001

# Runs as root by design. Gamarr writes its SQLite DB to DATA_DIR, imports into
# library volumes that are commonly root-owned, and talks to the Docker socket
# (root:docker) to start and stop the on-demand ClamAV container. Dropping to
# the unprivileged `gamarr` user breaks all three on existing deployments, so
# that has to land as a documented migration rather than a silent default.

ENTRYPOINT ["/usr/local/bin/gamarr"]
