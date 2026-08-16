# Speedrr Sidecar: YouTube Network Traffic & Subnet Monitor

A lightweight, high-performance Go-based microservice and sidecar container designed to detect YouTube and GoogleVideo streaming traffic (`googlevideo.com`, `youtube.com`) across your local network (Home Lab) and subnets/VLANs, and automatically dispatch real-time webhook events to [Speedrr](https://github.com/khw315/speedrr).

---

## Key Features

- **Single Host & Subnet/CIDR Monitoring**: Supports tracking individual target IPs (e.g. `192.168.1.50`) or entire subnet/VLAN ranges (e.g. `192.168.1.0/24`, `10.0.10.0/24`).
- **Multi-Client Stream Tracking**: Concurrently tracks all active streaming devices in a subnet and provides the aggregated active stream count (`active_stream_count`) and client IP list (`active_clients`) to Speedrr.
- **Zero-Copy / Low-Allocation Packet Sniffing**: Powered by `gopacket/pcap` with in-kernel Linux Berkeley Packet Filters (BPF).
- **DNS & TLS Client Hello SNI Extraction**: Inspects DNS Queries (UDP Port 53) and extracts *Server Name Indication* (SNI) hostnames directly from TLS Client Hello handshakes (TCP Port 443) using zero-allocation byte-offset parsing.
- **Per-Client Thread-Safe Debounce Cooldown**: Maintains dedicated cooldown timers per client to prevent rate-limit flapping during chunked video buffering.
- **Webhook Dispatcher**: Sends non-blocking HTTP POST JSON payloads (`stream_started`, `stream_update`, `stream_stopped`) to Speedrr.
- **Minimal Container Footprint**: Multi-stage Alpine-based Docker image (~12 MB) with non-root security (`appuser` with Linux capabilities `CAP_NET_RAW` and `CAP_NET_ADMIN`).

---

## Project Structure

```
speedrr/
├── docker-compose.yml                      # Main compose file orchestrating Speedrr + Monitor Sidecar
└── sidecars/
    └── youtube-monitor/
        ├── Dockerfile                      # Multi-stage Dockerfile (Alpine + CGO + libpcap)
        ├── config.go                       # Configuration loader (YAML + Environment Variables)
        ├── config.example.yaml             # Example YAML configuration file
        ├── go.mod / go.sum                 # Go module descriptor & dependencies
        ├── main.go                         # Packet capture engine, SNI parser, state & webhook
        ├── main_test.go                    # Unit tests for domain matching, BPF filter, & SNI
        ├── docs/
        │   └── architecture.md             # Standalone architecture, data flow, and state diagram
        └── README.md
```

---

## Deployment & Usage

### 1. Using Docker Compose (Recommended)

Edit `docker-compose.yml` in the project root to configure your environment:
- `MONITOR_INTERFACE`: Physical host interface name (e.g. `eth0`, `br0`, `enp3s0`).
- `TARGET_IPS`: Comma-separated specific target IPs (optional).
- `TARGET_SUBNETS`: Comma-separated subnet/CIDR ranges to monitor (e.g. `192.168.1.0/24,10.0.10.0/24`).
- `WEBHOOK_URL`: Destination Speedrr webhook URL.

Start the containers:
```bash
docker compose up -d --build
```

### 2. Standalone Docker Container

Build the Docker image:
```bash
docker build -t speedrr-youtube-monitor:latest ./sidecars/youtube-monitor
```

Run the container in host network mode:
```bash
docker run -d \
  --name speedrr-youtube-monitor \
  --restart unless-stopped \
  --net=host \
  --cap-add=NET_ADMIN \
  --cap-add=NET_RAW \
  -e MONITOR_INTERFACE=eth0 \
  -e TARGET_SUBNETS=192.168.1.0/24 \
  -e WEBHOOK_URL=http://127.0.0.1:8080/api/v1/webhook/stream \
  -e COOLDOWN_SECONDS=30 \
  speedrr-youtube-monitor:latest
```

---

## Environment Variables Configuration

| Variable | Default | Description |
|---|---|---|
| `MONITOR_INTERFACE` | `eth0` | Host physical network interface to capture packets from |
| `TARGET_IPS` | `""` | Comma-separated target host IPs |
| `TARGET_SUBNETS` | `""` | Comma-separated subnets in CIDR notation (e.g. `192.168.1.0/24,10.0.10.0/24`) |
| `GATEWAY_IP` | `""` | Gateway / Router IP (optional) |
| `WEBHOOK_URL` | `http://speedrr:8080/api/v1/webhook/stream` | Destination Webhook endpoint URL |
| `COOLDOWN_SECONDS` | `30` | Idle timeout duration before a client stream is considered stopped |
| `PROMISCUOUS` | `true` | Enable promiscuous mode on network interface |
| `DEBUG` | `false` | Enable verbose debug logging for DNS queries and SNI matches |

---

## Webhook Payload Examples

**1. First client in subnet (`192.168.1.105`) starts streaming YouTube:**
```json
{
  "event": "stream_started",
  "state": "ACTIVE",
  "service": "youtube",
  "target_ip": "192.168.1.105",
  "active_clients": ["192.168.1.105"],
  "active_stream_count": 1,
  "matched": "rr1---sn-4g5ednld.googlevideo.com",
  "protocol": "TLS",
  "timestamp": "2026-08-17T01:05:00Z"
}
```

**2. Second client (`192.168.1.120`) in the same subnet starts streaming:**
```json
{
  "event": "stream_update",
  "state": "ACTIVE",
  "service": "youtube",
  "target_ip": "192.168.1.120",
  "active_clients": ["192.168.1.105", "192.168.1.120"],
  "active_stream_count": 2,
  "matched": "rr2---sn-4g5ednld.googlevideo.com",
  "protocol": "TLS",
  "timestamp": "2026-08-17T01:05:15Z"
}
```

**3. All clients in the subnet stop streaming and cooldown expires:**
```json
{
  "event": "stream_stopped",
  "state": "IDLE",
  "service": "youtube",
  "target_ip": "192.168.1.120",
  "active_clients": [],
  "active_stream_count": 0,
  "matched": "cooldown_timeout",
  "protocol": "SYSTEM",
  "timestamp": "2026-08-17T01:05:45Z"
}
```
