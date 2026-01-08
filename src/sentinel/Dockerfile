# Build stage
FROM golang:1.22-alpine AS builder

WORKDIR /app

# Install build dependencies
RUN apk add --no-cache git

# Copy go mod files
COPY go.mod go.sum* ./

# Download dependencies (if go.sum exists)
RUN go mod download 2>/dev/null || true

# Copy source code
COPY . .

# Build the sentinel binary
RUN CGO_ENABLED=0 GOOS=linux go build -ldflags="-w -s" -o /sentinel ./cmd/sentinel

# Runtime stage
FROM alpine:3.19

WORKDIR /app

# Install runtime dependencies
RUN apk add --no-cache ca-certificates curl

# Create non-root user
RUN adduser -D -u 1000 sentinel

# Copy binary from builder
COPY --from=builder /sentinel /app/sentinel

# Create required directories
RUN mkdir -p /mnt/resource/trustbridge /dev/shm/weights && \
    chown -R sentinel:sentinel /mnt/resource/trustbridge /dev/shm/weights

# Environment defaults
ENV TB_TARGET_DIR=/mnt/resource/trustbridge
ENV TB_PIPE_PATH=/dev/shm/model-pipe
ENV TB_READY_SIGNAL=/dev/shm/weights/ready.signal
ENV TB_RUNTIME_URL=http://127.0.0.1:8081
ENV TB_PUBLIC_ADDR=0.0.0.0:8000
ENV TB_LOG_LEVEL=info

# Expose ports
EXPOSE 8000 8001

# Health check
HEALTHCHECK --interval=10s --timeout=5s --start-period=30s --retries=3 \
    CMD curl -f http://localhost:8001/health || exit 1

# Run as non-root user
USER sentinel

# Run sentinel
ENTRYPOINT ["/app/sentinel"]
