# Speedrr Sidecar: YouTube Network Traffic & Bandwidth Monitor

Microservice/sidecar container berbasis Go (Golang) berkinerja tinggi untuk mendeteksi lalu lintas streaming YouTube (`googlevideo.com`, `youtube.com`) pada jaringan lokal (Home Lab) dan mengirimkan event webhook ke [Speedrr](https://github.com/khw315/speedrr) secara otomatis.

---

## Fitur Utama

- **Zero-Copy / Low Allocation Packet Sniffing**: Menggunakan `gopacket/pcap` dengan BPF (*Berkeley Packet Filter*) terkompilasi langsung pada level kernel Linux.
- **DNS & TLS Client Hello SNI Extraction**: Mem-parsing DNS Query (Port 53 UDP) dan mengekstrak *Server Name Indication* (SNI) dari handshake TLS Client Hello (Port 443 TCP) secara *byte-offset* tanpa overhead alokasi memori berlebih.
- **Thread-safe State & Debounce Cooldown**: Menggunakan `sync.Mutex` dan `time.Timer` untuk mencegah flapping saat video buffering.
- **Webhook Dispatcher**: Mengirimkan HTTP POST JSON dengan payload status stream (`stream_started`, `stream_stopped`) ke Speedrr.
- **Clean Containerization**: Multi-stage Dockerfile berbasis Alpine Linux dengan ukuran image ultra-ringan (~12 MB).

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
        ├── go.mod                          # Go module descriptor
        ├── go.sum                          # Checksum dependencies
        ├── main.go                         # Engine Packet Capture, SNI Parser, & Webhook
        ├── main_test.go                    # Unit tests untuk domain matching & SNI parser
        └── README.md
```

---

## Cara Menjalankan

### 1. Menggunakan Docker Compose (Direkomendasikan)

Edit `docker-compose.yml` di root proyek untuk menyesuaikan:
- `MONITOR_INTERFACE`: Nama interface fisik host Anda (misal `eth0`, `br0`, `enp3s0`).
- `TARGET_IPS`: Daftar IP perangkat di LAN yang ingin dipantau (misal `192.168.1.50,192.168.1.51`).
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
  -e TARGET_IPS=192.168.1.50 \
  -e WEBHOOK_URL=http://127.0.0.1:8080/api/v1/webhook/stream \
  -e COOLDOWN_SECONDS=30 \
  speedrr-youtube-monitor:latest
```

---

## Konfigurasi Environment Variables

| Variable | Default | Keterangan |
|---|---|---|
| `MONITOR_INTERFACE` | `eth0` | Interface jaringan fisik host |
| `TARGET_IPS` | `192.168.1.50` | IP perangkat target (pisahkan koma untuk multiple IP) |
| `GATEWAY_IP` | `192.168.1.1` | IP Router / Gateway (opsional) |
| `WEBHOOK_URL` | `http://127.0.0.1:8080/api/v1/webhook/stream` | URL Webhook Speedrr |
| `COOLDOWN_SECONDS` | `30` | Waktu jeda sebelum status kembali ke IDLE |
| `PROMISCUOUS` | `true` | Aktifkan promiscuous mode pada adapter jaringan |
| `DEBUG` | `false` | Cetak log detail query DNS & TLS SNI |
