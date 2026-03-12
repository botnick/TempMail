# syntax=docker/dockerfile:1

# ============================================================================
# TempMail Platform — Unified Multi-Stage Dockerfile
# Optimized for fast, concurrent builds using BuildKit cache sharing
# ============================================================================

# --- Stage 1: Base Builder (Shared dependencies) ---
FROM golang:1.23-alpine AS base-builder

# Install required system packages
RUN apk add --no-cache git ca-certificates tzdata

WORKDIR /build
ENV GOWORK=off

# Copy go.mod and go.sum files first to leverage layer caching
COPY shared/go.mod shared/go.sum ./shared/
COPY api/go.mod api/go.sum ./api/
COPY mail-edge/go.mod mail-edge/go.sum ./mail-edge/
COPY worker/go.mod worker/go.sum ./worker/

# Download all dependencies concurrently using BuildKit cache
RUN --mount=type=cache,target=/go/pkg/mod \
    cd api && go mod download && \
    cd ../mail-edge && go mod download && \
    cd ../worker && go mod download

# Copy shared code (changes here invalidate downstream builds, which is correct)
COPY shared/ ./shared/


# --- Stage 2a: API Builder ---
FROM base-builder AS api-builder
COPY api/ ./api/
# Build API binary
RUN --mount=type=cache,target=/go/pkg/mod \
    --mount=type=cache,target=/root/.cache/go-build \
    CGO_ENABLED=0 GOOS=linux go build -ldflags="-s -w" -o /app/api ./api


# --- Stage 2b: Mail-Edge Builder ---
FROM base-builder AS mail-edge-builder
COPY mail-edge/ ./mail-edge/
# Build Mail-Edge binary
RUN --mount=type=cache,target=/go/pkg/mod \
    --mount=type=cache,target=/root/.cache/go-build \
    CGO_ENABLED=0 GOOS=linux go build -ldflags="-s -w" -o /app/mail-edge ./mail-edge


# --- Stage 2c: Worker Builder ---
FROM base-builder AS worker-builder
COPY worker/ ./worker/
# Build Worker binary
RUN --mount=type=cache,target=/go/pkg/mod \
    --mount=type=cache,target=/root/.cache/go-build \
    CGO_ENABLED=0 GOOS=linux go build -ldflags="-s -w" -o /app/worker ./worker


# ============================================================================
# PRODUCTION IMAGES (Distroless)
# ============================================================================

# --- API Production Image ---
FROM gcr.io/distroless/static:nonroot AS api

COPY --from=base-builder /usr/share/zoneinfo /usr/share/zoneinfo
COPY --from=base-builder /etc/ssl/certs/ca-certificates.crt /etc/ssl/certs/
COPY --from=api-builder /app/api /app/api
COPY api/admin-ui/ /app/admin-ui/

WORKDIR /app
USER nonroot:nonroot
EXPOSE 4000

ENTRYPOINT ["/app/api"]


# --- Mail-Edge Production Image ---
FROM gcr.io/distroless/static:nonroot AS mail-edge

COPY --from=base-builder /usr/share/zoneinfo /usr/share/zoneinfo
COPY --from=base-builder /etc/ssl/certs/ca-certificates.crt /etc/ssl/certs/
COPY --from=mail-edge-builder /app/mail-edge /app/mail-edge

WORKDIR /app
USER nonroot:nonroot
EXPOSE 2525

ENTRYPOINT ["/app/mail-edge"]


# --- Worker Production Image ---
FROM gcr.io/distroless/static:nonroot AS worker

COPY --from=base-builder /usr/share/zoneinfo /usr/share/zoneinfo
COPY --from=base-builder /etc/ssl/certs/ca-certificates.crt /etc/ssl/certs/
COPY --from=worker-builder /app/worker /app/worker

WORKDIR /app
USER nonroot:nonroot

ENTRYPOINT ["/app/worker"]
