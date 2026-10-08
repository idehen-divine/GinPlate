# Multi-stage production build: compiles statically, ships a minimal
# non-root runtime. Never COPY .env into the image; pass configuration via
# environment variables or mounted secrets at runtime.
FROM golang:1.26-alpine AS build
WORKDIR /app
COPY go.mod go.sum ./
RUN go mod download
COPY . .
RUN CGO_ENABLED=0 go build -ldflags="-s -w" -o /bin/ginplate ./cmd/ginplate

FROM alpine:3.21
RUN adduser -D -H -u 10001 appuser && \
  mkdir -p /app/storage && chown appuser /app/storage && \
  apk add --no-cache wget
WORKDIR /app
COPY --from=build /bin/ginplate /app/ginplate
# Persistent data (uploads, file sessions, down marker) mounts here.
VOLUME ["/app/storage"]
EXPOSE 8080
USER appuser
HEALTHCHECK --interval=30s --timeout=3s --start-period=10s --retries=3 \
  CMD wget -qO- http://127.0.0.1:8080/livez | grep -q ok
ENTRYPOINT ["/app/ginplate"]
CMD ["serve"]
