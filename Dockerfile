# Multi-stage production build: compiles statically, ships a minimal
# non-root runtime. Never COPY .env into the image; pass configuration via
# environment variables or mounted secrets at runtime.
# Base images are digest-pinned for reproducible builds; update the tag and
# digest together through the controlled dependency process.
FROM golang:1.26.9-alpine@sha256:397ecc648150dd585f1734dfcd8d1bb4746664e54464191d77ad0ecffaf38bdb AS build
WORKDIR /app
COPY go.mod go.sum ./
RUN go mod download
COPY . .
RUN CGO_ENABLED=0 go build -ldflags="-s -w" -o /bin/ginplate ./cmd/ginplate

FROM alpine:3.21.8@sha256:3c81aa9a3d770b316568f4499e30461a5cd3fbd7180bd89e28e34894c7845832
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
