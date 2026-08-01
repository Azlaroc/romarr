FROM golang:1.25-alpine AS builder

ARG VERSION=dev

WORKDIR /build

COPY go.mod go.sum ./
RUN go mod download

COPY . .
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

USER 1000:1000

ENTRYPOINT ["/usr/local/bin/gamarr"]
