# Changelog

All notable changes to this project will be documented in this file.

The format is based on [Keep a Changelog](https://keepachangelog.com/en/1.1.0/),
and this project adheres to [Semantic Versioning](https://semver.org/spec/v2.0.0.html).

---

## [Unreleased]

## [dev2.1.0] - 2026-08-17

### 🚀 New Features

- **Subnet & CIDR Range Monitoring**:
  - Support for tracking entire subnets and VLANs (e.g. `192.168.1.0/24`, `10.0.10.0/24`) via `target_subnets` / `TARGET_SUBNETS`.
  - Kernel BPF filter generator automatically outputs `net <cidr>` for subnets and `host <ip>` for individual targets.
  - Added dynamic client source IP extraction (`extractSourceIP`) from IPv4/IPv6 packet headers.
  - Multi-client active stream state tracker with per-client cooldown timers.
  - Enhanced webhook payload containing `active_clients` list and `active_stream_count` for Speedrr's stream-based speed mapping.

- **Inbound Webhook Receiver Module (`modules.webhook`)**:
  - Embedded lightweight HTTP server (`net/http`) listening for external streaming events (`/api/v1/webhook/stream`, `/health`).
  - Supports token authentication (`Bearer` / `X-API-Key` / query parameter).
  - Integrates seamlessly with `stream_based_speeds` table to dynamically throttle torrent client upload speeds when external streams (e.g. YouTube, sidecars, Home Assistant) become active.
  - Aggregates external active streams with Plex, Jellyfin, and Emby media server counts.
  - Instantaneous event-driven recalculation without waiting for periodic polling intervals.

- **YouTube & GoogleVideo Network Streaming Monitor Sidecar (`speedrr-youtube-monitor`)**:
  - High-performance, low-memory Go sidecar for monitoring LAN streaming traffic in promiscuous mode via `gopacket/pcap`.
  - In-kernel BPF filtering for target client IPs (`host`) and subnets (`net <cidr>`) on DNS (Port 53) and TLS/QUIC (Port 443).
  - Fast, zero-allocation TLS Client Hello SNI parser extracting `googlevideo.com` and `youtube.com` hostnames directly from byte-offsets.
  - Early-detection DNS Query inspector matching streaming domains.
  - Thread-safe Debounce & Cooldown State Manager (`sync.Mutex`) preventing speed flapping during video chunk buffering.
  - Asynchronous JSON Webhook Dispatcher sending `stream_started`, `stream_update`, and `stream_stopped` events to Speedrr.

### 🐳 CI/CD & Containerization

- **Multi-Arch Docker Images & Multi-Stage Builds**:
  - Multi-stage minimal Alpine-based Dockerfile (~12 MB) matching Speedrr's base runtime (`alpine:3.21`, Go 1.24).
  - Non-root container security (`speedrr:speedrr` user with Linux capabilities `CAP_NET_RAW` and `CAP_NET_ADMIN`).
  - Added parallel GitHub Actions workflow job in `docker_push.yml` building and publishing multi-architecture (`linux/amd64`, `linux/arm64`) images to Docker Hub and GHCR on release tags (`v*`, `dev*`).
  - Out-of-the-box `docker-compose.yml` for unified deployment of Speedrr alongside the monitor sidecar.

### 🧪 Testing & Code Quality

- Added comprehensive unit test suites in `sidecars/youtube-monitor/config_test.go` and `sidecars/youtube-monitor/main_test.go` with 100% test pass rate.
- Configured multi-module code coverage in GitHub Actions CI and SonarCloud analysis (`sonar.go.coverage.reportPaths`).

### 🎨 Documentation & Design

- Created comprehensive Markdown architecture and data flow design specification in `sidecars/youtube-monitor/docs/architecture.md`.
- Added comprehensive English user guide and configuration reference in `sidecars/youtube-monitor/README.md`.
