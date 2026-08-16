# ==============================================================================
# STAGE 1: Builder with CGO & Libpcap
# ==============================================================================
FROM golang:1.24-alpine AS builder

# Install compiler toolchain & libpcap headers sorted alphabetically
RUN apk add --no-cache \
    gcc \
    git \
    libpcap-dev \
    musl-dev

WORKDIR /app

# Cache dependency layer
COPY go.mod go.sum* ./
RUN go mod download

# Copy source code
COPY . .

# Compile Go binary with CGO enabled for libpcap
RUN CGO_ENABLED=1 go build -ldflags="-w -s" -o speedrr-ytmon .

# ==============================================================================
# STAGE 2: Minimal Runtime Image
# ==============================================================================
FROM alpine:3.21

LABEL org.opencontainers.image.source="https://github.com/khw315/speedrr"
LABEL org.opencontainers.image.licenses="GPL-3.0"
LABEL org.opencontainers.image.title="Speedrr YouTube Monitor Sidecar"
LABEL org.opencontainers.image.description="Promiscuous packet capture & TLS SNI parser to track YouTube streaming on Home Lab LAN and subnets."

# Install runtime packages sorted alphabetically
RUN apk add --no-cache \
    ca-certificates \
    libcap \
    libpcap \
    tzdata \
    && addgroup -S -g 1000 speedrr \
    && adduser -S -u 1000 -G speedrr -h /home/speedrr speedrr

WORKDIR /app

# Copy compiled binary from builder stage
COPY --from=builder /app/speedrr-ytmon /usr/local/bin/speedrr-ytmon

# Grant Linux capabilities cap_net_raw & cap_net_admin to allow non-root packet capture
RUN setcap 'cap_net_raw,cap_net_admin=+ep' /usr/local/bin/speedrr-ytmon \
    && chown -R speedrr:speedrr /app /home/speedrr

# Default environment variables
ENV MONITOR_INTERFACE=eth0 \
    TARGET_IPS="" \
    TARGET_SUBNETS="" \
    WEBHOOK_URL=http://127.0.0.1:8080/api/v1/webhook/stream \
    COOLDOWN_SECONDS=30 \
    PROMISCUOUS=true \
    DEBUG=false

USER speedrr

ENTRYPOINT ["speedrr-ytmon"]
