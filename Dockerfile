# ─── Stage 1: Build ───────────────────────────────────────────────────────────
FROM golang:1.24-alpine AS builder

# Install git (needed to fetch Go modules via HTTPS) and CA certs
RUN apk add --no-cache git ca-certificates

WORKDIR /src

# Cache module downloads separately from source changes
COPY go.mod go.sum ./
RUN go mod download

# Copy source and build a fully-static binary
COPY . .
RUN CGO_ENABLED=0 GOOS=linux GOARCH=amd64 \
    go build -trimpath -ldflags="-s -w" -o /gosearch ./cmd/gosearch

# ─── Stage 2: Run ─────────────────────────────────────────────────────────────
FROM alpine:3.20

# Trust system CA certs (needed for the crawler to reach HTTPS sites)
RUN apk add --no-cache ca-certificates

# Non-root user with a fixed UID/GID (10001), so the Azure file share can be
# mounted owned by this user (APP_UID in deploy/azure.env must match).
RUN addgroup -S -g 10001 appgroup && adduser -S -u 10001 -G appgroup appuser

WORKDIR /app

COPY --from=builder /gosearch ./gosearch

# DATA_DIR is the root for persistent data.
# On Azure Container Apps it is overridden to the file share mount path (/mnt/data).
# The default (/app/data) keeps local-dev behaviour without a volume.
ENV DATA_DIR=/app/data \
    PORT=8080

# Pre-create the default data dir so local runs work without a volume mounted
RUN mkdir -p /app/data && chown -R appuser:appgroup /app

USER appuser

EXPOSE 8080

CMD ["./gosearch"]
