# Build stage
FROM golang:1.26-alpine AS builder

ARG VERSION=dev
ARG COMMIT=none
ARG BUILD_DATE=unknown

# Install build dependencies
RUN apk add --no-cache git ca-certificates

WORKDIR /build

# Copy go mod files
COPY go.mod go.sum ./
RUN go mod download

# Copy source code
COPY . .

# Build the binary with version info
RUN CGO_ENABLED=0 GOOS=linux go build -a -installsuffix cgo \
    -ldflags "-s -w \
      -X github.com/okdaichi/qumo/internal/version.version=${VERSION} \
      -X github.com/okdaichi/qumo/internal/version.commit=${COMMIT} \
      -X github.com/okdaichi/qumo/internal/version.date=${BUILD_DATE}" \
    -o qumo .

# Runtime stage
FROM alpine:latest

ARG VERSION=dev
ARG COMMIT=none
ARG BUILD_DATE=unknown

# OCI image labels
LABEL org.opencontainers.image.title="qumo" \
      org.opencontainers.image.description="MoQT Relay & SDN Controller" \
      org.opencontainers.image.url="https://github.com/okdaichi/qumo" \
      org.opencontainers.image.source="https://github.com/okdaichi/qumo" \
      org.opencontainers.image.version="${VERSION}" \
      org.opencontainers.image.created="${BUILD_DATE}" \
      org.opencontainers.image.revision="${COMMIT}" \
      org.opencontainers.image.licenses="MIT"

# Install runtime dependencies
RUN apk --no-cache add ca-certificates tzdata wget openssl

WORKDIR /app

# Copy entrypoint script
COPY docker-entrypoint.sh /usr/local/bin/
RUN chmod +x /usr/local/bin/docker-entrypoint.sh

# Copy binary from builder
COPY --from=builder /build/qumo .

# Copy config files (optional - can be generated from env vars)
COPY config.relay.yaml config.sdn.yaml ./

# Create certs and data directories
RUN mkdir -p certs data

# Expose ports
# 4433: MoQT (QUIC) and HTTP health/metrics (TCP)
# 8090: SDN HTTP API
EXPOSE 4433/udp 4433 8090

# Health check
HEALTHCHECK --interval=30s --timeout=3s --start-period=5s --retries=3 \
  CMD wget --no-verbose --tries=1 --spider http://localhost:4433/health || exit 1

# Use entrypoint script for config generation
ENTRYPOINT ["docker-entrypoint.sh"]

# Default command (relay)
# Override with: docker run qumo sdn -config config.sdn.yaml
CMD ["relay", "-config", "config.relay.yaml"]