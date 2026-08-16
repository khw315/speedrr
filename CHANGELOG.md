# Changelog

All notable changes to this project will be documented in this file.

The format is based on [Keep a Changelog](https://keepachangelog.com/en/1.1.0/),
and this project adheres to [Semantic Versioning](https://semver.org/spec/v2.0.0.html).

---

## [Unreleased] - 2026-08-17

### 🚀 New Features

- **YouTube Network Traffic Monitor Sidecar (`speedrr-youtube-monitor`)**:
  - Passive promiscuous packet sniffer built with `gopacket/pcap` to monitor streaming traffic on LAN.
  - In-kernel BPF filtering for target client IPs on DNS (Port 53) and TLS/QUIC (Port 443).
  - High-performance, zero-allocation TLS Client Hello SNI parser to extract `googlevideo.com` and `youtube.com` hostnames.
  - DNS Question query inspector for matching streaming domains.
  - Thread-safe Debounce & Cooldown State Manager (`sync.Mutex` with configurable timeout) to eliminate rate-limiting flapping during chunked video buffering.
  - Asynchronous JSON Webhook Dispatcher sending real-time `stream_started` and `stream_stopped` events to Speedrr.
  - Multi-stage minimal Docker container (~12 MB) based on Alpine Linux with non-root security (`appuser` with `CAP_NET_RAW` & `CAP_NET_ADMIN`).
  - Out-of-the-box `docker-compose.yml` configuration for seamless multi-container deployment.

### 🎨 Documentation & Design

- Added standalone editorial SVG architecture and data-flow diagram in `sidecars/youtube-monitor/docs/architecture.html`.
- Added comprehensive user and configuration guides in `sidecars/youtube-monitor/README.md`.
- Added unit test suite in `sidecars/youtube-monitor/main_test.go` with 100% test pass rate.
