# ==============================================================================
# STAGE 1: Builder dengan CGO & Libpcap
# ==============================================================================
FROM golang:1.23-alpine AS builder

# Pasang compiler toolchain & header libpcap secara terurut alfabetis
RUN apk add --no-cache \
    gcc \
    git \
    libpcap-dev \
    musl-dev

WORKDIR /app

# Cache layer dependensi
COPY go.mod go.sum* ./
RUN go mod download

# Salin source code
COPY . .

# Kompilasi Go binary dengan CGO aktif untuk libpcap
RUN CGO_ENABLED=1 GOOS=linux go build -ldflags="-w -s" -o speedrr-ytmon .

# ==============================================================================
# STAGE 2: Minimal Runtime Image
# ==============================================================================
FROM alpine:3.20

LABEL org.opencontainers.image.title="Speedrr YouTube Monitor Sidecar"
LABEL org.opencontainers.image.description="Promiscuous packet capture & TLS SNI parser to track YouTube streaming on Home Lab LAN"

# Pasang runtime packages terurut alfabetis
RUN apk add --no-cache \
    ca-certificates \
    libcap \
    libpcap \
    tzdata \
    && addgroup -S -g 1000 appgroup \
    && adduser -S -u 1000 -G appgroup appuser

WORKDIR /app

# Salin hasil kompilasi binary dari stage builder
COPY --from=builder /app/speedrr-ytmon /usr/local/bin/speedrr-ytmon

# Berikan Linux capability cap_net_raw & cap_net_admin agar binary bisa dijalankan oleh non-root user
RUN setcap 'cap_net_raw,cap_net_admin=+ep' /usr/local/bin/speedrr-ytmon

# Environment default
ENV MONITOR_INTERFACE=eth0 \
    TARGET_IPS=192.168.1.50 \
    WEBHOOK_URL=http://127.0.0.1:8080/api/v1/webhook/stream \
    COOLDOWN_SECONDS=30 \
    PROMISCUOUS=true \
    DEBUG=false

USER appuser

ENTRYPOINT ["/usr/local/bin/speedrr-ytmon"]
