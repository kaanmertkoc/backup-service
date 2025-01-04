FROM golang:1.21-alpine AS builder

WORKDIR /app

# Install build dependencies
RUN apk add --no-cache gcc musl-dev sqlite-dev

# Copy go mod files
COPY go.mod go.sum ./

# Download dependencies
RUN go mod download

# Copy source code
COPY . .

# Build the application
RUN CGO_ENABLED=1 GOOS=linux go build -o backup-app

# Final stage
FROM alpine:3.19

# Install runtime dependencies
RUN apk add --no-cache \
    sqlite-libs \
    tzdata \
    ca-certificates

WORKDIR /app
COPY --from=builder /app/backup-app .

# Create backup directory
RUN mkdir /backups

# Add healthcheck
HEALTHCHECK --interval=30s --timeout=10s --start-period=5s --retries=3 \
    CMD ps aux | grep backup-app | grep -v grep || exit 1

ENTRYPOINT ["/app/backup-app"]