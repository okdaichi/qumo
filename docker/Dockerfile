# Stage 1: build the playground web UI.
# The Go binary embeds playground/dist via //go:embed all:playground/dist, so
# the real UI must exist before `go build`. The build needs Deno (jsr deps +
# `deno check`) and Node (Vite). Deno has no official multi-arch alpine image and
# deno.land's install.sh is unreliable, so download the pinned binary directly
# from GitHub releases, picking the right arch. DENO_VERSION must match
# .github/workflows/release.yml.
FROM node:22-alpine AS frontend
ARG DENO_VERSION=2.8.1
RUN apk add --no-cache curl unzip \
 && case "$(uname -m)" in \
      x86_64)  DARCH=x86_64-unknown-linux-gnu ;; \
      aarch64) DARCH=aarch64-unknown-linux-gnu ;; \
      *) echo "unsupported arch: $(uname -m)"; exit 1 ;; \
    esac \
 && curl -fsSL -o /tmp/deno.zip \
      "https://github.com/denoland/deno/releases/download/v${DENO_VERSION}/deno-${DARCH}.zip" \
 && unzip -o /tmp/deno.zip -d /usr/local/bin \
 && rm /tmp/deno.zip \
 && deno --version \
 && deno --version | grep -q "${DENO_VERSION}"
WORKDIR /ui
COPY playground/ ./
RUN deno install && deno task build

# Stage 2: build the Go binary.
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

# Overlay the real playground UI (built in the frontend stage) onto the
# placeholder that ships in the repo, so go:embed picks it up.
COPY --from=frontend /ui/dist ./playground/dist

# Build the binary with version info
RUN CGO_ENABLED=0 GOOS=linux go build -a -installsuffix cgo \
    -ldflags "-s -w \
      -X github.com/qumo-dev/qumo/internal/version.version=${VERSION} \
      -X github.com/qumo-dev/qumo/internal/version.commit=${COMMIT} \
      -X github.com/qumo-dev/qumo/internal/version.date=${BUILD_DATE}" \
    -o qumo .

# Runtime stage
FROM alpine:latest

ARG VERSION=dev
ARG COMMIT=none
ARG BUILD_DATE=unknown

# OCI image labels
LABEL org.opencontainers.image.title="qumo" \
      org.opencontainers.image.description="MoQT Relay" \
      org.opencontainers.image.url="https://github.com/qumo-dev/qumo" \
      org.opencontainers.image.source="https://github.com/qumo-dev/qumo" \
      org.opencontainers.image.version="${VERSION}" \
      org.opencontainers.image.created="${BUILD_DATE}" \
      org.opencontainers.image.revision="${COMMIT}" \
      org.opencontainers.image.licenses="MIT"

# Install runtime dependencies
RUN apk --no-cache add ca-certificates tzdata wget

WORKDIR /app

# Copy binary from builder
COPY --from=builder /build/qumo .

# Create certs directory
RUN mkdir -p certs

# Expose ports
# 4433: MoQT (QUIC) and HTTP health/metrics (TCP)
EXPOSE 4433/udp 4433

# Health check
HEALTHCHECK --interval=30s --timeout=3s --start-period=5s --retries=3 \
  CMD wget --no-verbose --tries=1 --spider http://localhost:4433/health || exit 1

ENTRYPOINT ["/app/qumo"]

# Default command
CMD ["relay"]