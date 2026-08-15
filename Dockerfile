# Build stage
FROM golang:1.24-alpine AS builder

WORKDIR /app

COPY go.mod go.sum ./
RUN go mod download

COPY . .

ARG TARGETOS
ARG TARGETARCH
RUN CGO_ENABLED=0 GOOS="${TARGETOS:-linux}" GOARCH="${TARGETARCH:-amd64}" go build -ldflags="-w -s" -o speedrr .

# Final stage
FROM alpine:3.21

LABEL org.opencontainers.image.source="https://github.com/khw315/speedrr"
LABEL org.opencontainers.image.licenses=GPL-3.0
LABEL org.opencontainers.image.description="Dynamically manage speeds on torrent clients, with Plex/Jellyfin/Emby integration."

RUN apk add --no-cache ca-certificates tzdata \
    && addgroup -S speedrr \
    && adduser -S speedrr -G speedrr -h /home/speedrr

WORKDIR /app

COPY --from=builder /app/speedrr /usr/local/bin/speedrr

RUN chown -R speedrr:speedrr /app /home/speedrr

USER speedrr

CMD ["speedrr"]
