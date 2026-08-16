# Speedrr Sidecar: YouTube Network Traffic & Subnet Monitor

Microservice/sidecar container berbasis Go (Golang) berkinerja tinggi untuk mendeteksi lalu lintas streaming YouTube (`googlevideo.com`, `youtube.com`) pada jaringan lokal (Home Lab) dan subnet/VLAN, serta mengirimkan event webhook ke [Speedrr](https://github.com/khw315/speedrr) secara otomatis.

---

## Fitur Utama

- **Single Host & Subnet/CIDR Monitoring**: Mendukung pemantauan IP spesifik (`192.168.1.50`) maupun seluruh rentang subnet/VLAN (`192.168.1.0/24`, `10.0.10.0/24`).
- **Multi-Client Stream Tracking**: Melacak seluruh perangkat aktif di subnet secara simultan dan mengirimkan jumlah stream aktif (`active_stream_count`) dan daftar IP aktif (`active_clients`) ke Speedrr.
- **Zero-Copy / Low Allocation Packet Sniffing**: Menggunakan `gopacket/pcap` dengan BPF (*Berkeley Packet Filter*) terkompilasi langsung pada level kernel Linux.
- **DNS & TLS Client Hello SNI Extraction**: Mem-parsing DNS Query (Port 53 UDP) dan mengekstrak *Server Name Indication* (SNI) dari handshake TLS Client Hello (Port 443 TCP) secara *byte-offset* tanpa alokasi memori berlebih.
- **Per-Client Thread-safe Debounce Cooldown**: Menggunakan timer cooldown per-klien untuk mencegah flapping saat video buffering.
- **Webhook Dispatcher**: Mengirimkan HTTP POST JSON dengan payload status stream (`stream_started`, `stream_update`, `stream_stopped`) ke Speedrr.
- **Clean Containerization**: Multi-stage Dockerfile berbasis Alpine Linux dengan ukuran image ultra-ringan (~12 MB) dan non-root user capabilities (`CAP_NET_RAW`, `CAP_NET_ADMIN`).

---

## Struktur Proyek

```
speedrr/
├── docker-compose.yml                      # Compose file utama untuk Speedrr + Monitor Sidecar
└── sidecars/
    └── youtube-monitor/
        ├── Dockerfile                      # Multi-stage Dockerfile (Alpine + CGO + libpcap)
        ├── config.go                       # Loader konfigurasi (YAML + Environment Variables)
        ├── config.example.yaml             # Contoh file konfigurasi
        ├── go.mod / go.sum                 # Go module descriptor & dependencies
        ├── main.go                         # Engine Packet Capture, SNI Parser, & Webhook
        ├── main_test.go                    # Unit tests untuk domain matching, BPF filter, & SNI
        └── README.md
```

---

## Cara Menjalankan

### 1. Menggunakan Docker Compose (Direkomendasikan)

Edit `docker-compose.yml` di root proyek untuk menyesuaikan:
- `MONITOR_INTERFACE`: Nama interface fisik host Anda (misal `eth0`, `br0`, `enp3s0`).
- `TARGET_IPS`: Daftar IP perangkat spesifik (opsional).
- `TARGET_SUBNETS`: Daftar Subnet/CIDR yang ingin dipantau (misal `192.168.1.0/24,10.0.10.0/24`).
- `WEBHOOK_URL`: Endpoint Webhook Speedrr.

Jalankan container:
```bash
docker compose up -d --build
```

### 2. Build Docker Image Secara Terpisah

```bash
docker build -t speedrr-youtube-monitor:latest ./sidecars/youtube-monitor
```

Jalankan container:
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

## Konfigurasi Environment Variables

| Variable | Default | Keterangan |
|---|---|---|
| `MONITOR_INTERFACE` | `eth0` | Interface jaringan fisik host |
| `TARGET_IPS` | `""` | IP perangkat target (pisahkan koma) |
| `TARGET_SUBNETS` | `""` | Subnet CIDR (e.g. `192.168.1.0/24,10.0.10.0/24`) |
| `GATEWAY_IP` | `""` | IP Router / Gateway (opsional) |
| `WEBHOOK_URL` | `http://speedrr:8080/api/v1/webhook/stream` | URL Webhook Speedrr |
| `COOLDOWN_SECONDS` | `30` | Waktu jeda sebelum status kembali ke IDLE |
| `PROMISCUOUS` | `true` | Aktifkan promiscuous mode pada adapter jaringan |
| `DEBUG` | `false` | Cetak log detail query DNS & TLS SNI |

---

## Contoh Payload Webhook Multi-Client Subnet

Ketika klien pertama di subnet (`192.168.1.105`) mulai streaming:
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

Ketika klien kedua (`192.168.1.120`) juga mulai streaming di subnet yang sama:
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
