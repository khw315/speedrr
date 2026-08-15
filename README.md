<p align="center">
    <img src="https://raw.githubusercontent.com/itschasa/speedrr/master/images/speedrr_text.png" alt="speedrr" width="336" height="84">
    <br/>
    <h1>speedrr - Dynamic Upload and Download Speed Manager for Torrenting (Golang Edition)</h1>
</p>

> [!NOTE]
> This repository is a fork of [itschasa/speedrr](https://github.com/itschasa/speedrr), rewritten in **Golang** for ultra-low memory footprint (~5–15 MB RAM), lightweight Docker image (<15 MB), single binary deployment, and native concurrency.

Change your torrent client's upload speed dynamically on events such as:
- When a Plex/Jellyfin/Emby/Tautulli stream starts
- Time of day and day of the week
- Number of active media streams (stream-based predictive control)

Change your torrent client's download speed dynamically on events such as:
- Time of day and day of the week

This tool is ideal for users with limited upload speed; however, anyone can use it to maximize seeding rates while keeping Plex/Jellyfin/Emby streams buffer-free! Great for Home Labs, NAS (Unraid, Synology, TrueNAS), and Raspberry Pi / SBC devices.



## Features
- **Ultra Lightweight**: Built with Golang. Consumes only **~5–15 MB RAM** and runs from a **<15 MB Docker container**.
- **Multi-Server Support**: Plex, Jellyfin, Emby, and Tautulli.
- **Multi-Client Support**: qBittorrent and Transmission.
    - Bandwidth is split between clients by number of downloading/uploading torrents or manual share ratios.
- **Stream-Based Speed Control**: Set specific upload speeds based on active stream count instead of bandwidth estimates.
- **Time/Day Schedules**: Flexible time-of-day and day-of-week speed limits with support for percentages (`"50%"`), fixed values, and `unlimited`.
- **Resilient Startup**: Exponential backoff retries if media servers or torrent clients are temporarily offline.
- **Single Static Binary**: No Python interpreter or external runtime dependencies required.



## Setup

### Docker (GHCR)

Pull the Docker image:
```bash
docker pull ghcr.io/khw315/speedrr:latest
```

Example `docker run` command:
```bash
docker run -d \
    -e SPEEDRR_CONFIG=/data/config.yaml \
    -v /path/to/config_folder/:/data/ \
    --name speedrr \
    --network host \
    ghcr.io/khw315/speedrr:latest
```

Example `docker-compose.yml`:
```yaml
services:
  speedrr:
    image: ghcr.io/khw315/speedrr:latest
    container_name: speedrr
    restart: unless-stopped
    network_mode: host
    environment:
      - SPEEDRR_CONFIG=/data/config.yaml
    volumes:
      - ./config:/data
```

### Unraid

1. Open your Unraid console and create a template:
```bash
cd /boot/config/plugins/dockerMan/templates-user && touch my-speedrr.xml && nano my-speedrr.xml
```
2. Copy and paste the template from [`speedrr-unraid.xml`](speedrr-unraid.xml).
3. Open WebUI > `Docker` > `Add Container` > Select `speedrr`.
4. Place your `config.yaml` in `/appdata/speedrr/` and start the container.

### Building from Source (Go)

1. Install **Go 1.22+**.
2. Clone this repository:
```bash
git clone https://github.com/khw315/speedrr.git
cd speedrr
```
3. Build the binary:
```bash
go build -o speedrr .
```
4. Run Speedrr:
```bash
./speedrr --config_path config.yaml
```



## Stream-Based Speed Control

Instead of dynamically reducing upload speed based on bandwidth usage, you can configure Speedrr to set specific upload speeds based on the number of active streams.

### Why Use Stream-Based Speeds?

Traditional bandwidth-based control is reactive—it reduces your upload speed based on how much bandwidth streams are using. Stream-based control is predictive—you define exactly what upload speed you want for different numbers of streams.

Benefits:
- **More Predictable**: You control exactly what happens with 1, 2, 3+ streams.
- **Max Seeding When Idle**: Set unlimited upload when no streams are active.
- **Better Balance**: Fine-tune the trade-off between streaming quality and torrent upload.
- **Easier Configuration**: Count streams instead of estimating bandwidth needs.

### Quick Start

Add `stream_based_speeds` to your media server configuration:

```yaml
modules:
  media_servers:
    - type: jellyfin
      url: http://your-jellyfin-server:8096
      api_key: your_api_key
      https_verify: false

      update_interval: 5
      ignore_streams:
        local: true
        ip_networks: [192.168.0.0/24, 127.0.0.1]
        paused_after: 300

      stream_based_speeds:
        enabled: true
        speeds:
          0: unlimited    # No streams = unlimited upload
          1: 10           # 1 stream = 10 Mbit/s upload
          2: 8            # 2 streams = 8 Mbit/s upload
          3: 6            # 3 streams = 6 Mbit/s upload
          4: 5            # 4+ streams = 5 Mbit/s upload
        default: 5
```

See [`config.stream_based.example.yaml`](config.stream_based.example.yaml) for a fully documented configuration example.



## Contributing

Contributions are welcome! Feel free to open issues or submit pull requests.

## License

Distributed under the [GPL-3.0 License](LICENSE).
