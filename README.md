<p align="center">
    <img src="https://raw.githubusercontent.com/itschasa/speedrr/master/images/speedrr_text.png" alt="speedrr" width="336" height="84">
    <br/>
    <h1>speedrr - Dynamic Upload and Download Speed Manager for Torrenting</h1>
</p>

Change your torrent client's upload speed dynamically, on certain events such as:
- When a Plex/Jellyfin/Emby stream starts
- Time of day and day of the week
- <i>More coming soon!</i>


Change your torrent client's download speed dynamically, on certain events such as:
- Time of day and day of the week
- <i>More coming soon!</i>


This script is ideal for users with limited upload speed, however anyone can use it to maximise their upload speed, whilst keeping their Plex/Jellyfin/Emby streams buffer-free! Also great to adjust the download rate during the day, in case the bandwidth is needed for something else!



## Features
- Multi-server support for Plex, Jellyfin, Emby, and Tautulli.
- Supports qBittorrent and Transmission.
- Multi-torrent-client support.
    - Bandwidth is split between them, by number of downloading/uploading torrents.
- Schedule a time/day when upload speed should be lowered.
- Support for unlimited speeds in schedules (equivalent to turning off speed limits).
- **Stream-based speed control**: Set different upload speeds based on the number of active streams instead of bandwidth usage.



## Setup

### Docker
Pull the image with:
```cmd
docker pull itschasa/speedrr
```

Your config file should be stored outside of the container, for easy editing.

You can then add a volume to the container (like /data/), which points to a folder where your config is stored.

Example `docker run` command:
```
docker run -d
    -e SPEEDRR_CONFIG=/data/config.yaml
    -v /folder_with_config/:/data/
    --name speedrr
    --network host
    itschasa/speedrr
```

### Unraid
1. Open your console and run the following command:
```
cd /boot/config/plugins/dockerMan/templates-user && touch my-speedrr.xml && nano my-speedrr.xml
```
2. Go to <a href="https://raw.githubusercontent.com/itschasa/speedrr/main/speedrr-unraid.xml">speedrr-unraid.xml</a>, and copy and paste it into your console.
3. Press Ctrl+O, then Enter, then Ctrl+X (to save the file and exit).
4. Open your WebUI > `Docker` > `Add Container`.
5. Click `Select a template`, and select `speedrr`.
6. The options should be fine as they are defaulted. Apply changes.
7. Using the <a href="https://github.com/itschasa/speedrr/blob/main/config.yaml">template</a>, create config.yaml in your /appdata/speedrr/ folder, and fill out the config.
8. Start/Restart the container in the WebUI.
9. Check everything is working in the logs (Docker Logs).

### Source
1. Download the source code.
2. Install Python 3.10 (other versions should work).
3. Install the required modules with `python -m pip install -r requirements.txt`.
4. Edit the config to your liking.
5. Run `python main.py --config_path config.yaml` to start.


## Stream-Based Speed Control

**New in this version!** Instead of dynamically reducing upload speed based on bandwidth usage, you can configure speedrr to set specific upload speeds based on the **number of active streams**.

### Why Use Stream-Based Speeds?

Traditional bandwidth-based control is reactive—it reduces your upload speed based on how much bandwidth streams are using. Stream-based control is **predictive**—you define exactly what upload speed you want for different numbers of streams.

**Benefits:**
- 🎯 **More predictable** - You control exactly what happens with 1, 2, 3+ streams
- 🚀 **Max out when idle** - Set unlimited upload when no streams are active
- ⚖️ **Better balance** - Fine-tune the trade-off between streaming quality and torrent upload
- 📊 **Easier to configure** - Just count streams instead of estimating bandwidth needs

### Quick Start

Add `stream_based_speeds` to your media server configuration:

```yaml
modules:
  media_servers:
    - type: plex
      url: http://your-plex-server:32400
      token: your_token
      # ... other settings ...
      
      stream_based_speeds:
        enabled: true
        speeds:
          0: unlimited    # No streams = unlimited upload
          1: 10           # 1 stream = 10 Mbit/s upload
          2: 8            # 2 streams = 8 Mbit/s upload
          3: 6            # 3+ streams = 6 Mbit/s upload
        default: 5        # Optional: fallback speed
```

### Configuration Options

**`speeds` mapping** - Define upload speeds for different stream counts:
- Numbers: `10`, `15`, `20` (in your configured units)
- Percentages: `"50%"`, `"80%"` (of max_upload)  
- Unlimited: `unlimited` (removes speed limit)

**`default`** (optional) - Fallback speed for undefined stream counts. If omitted, uses the highest defined count's speed.

### How It Works

1. **Stream Counting**: Speedrr monitors your media server and counts active streams
2. **Filtering**: Local streams, paused streams, and ignored IPs are excluded from the count
3. **Speed Selection**: Upload speed is set based on your configured mapping
4. **Dynamic Updates**: Speed adjusts automatically as streams start/stop

### Use Cases

**Scenario 1: Maximize seeding when idle**
```yaml
speeds:
  0: unlimited   # Full upload when not streaming
  1: 8           # Conservative when streaming
```

**Scenario 2: Granular control for multiple users**
```yaml
speeds:
  0: unlimited
  1: 12          # One user streaming
  2: 10          # Two users
  3: 8           # Three users
  4: 6           # Four or more users
```

**Scenario 3: Using percentages**
```yaml
speeds:
  0: "100%"      # Full max_upload
  1: "70%"       # 70% of max_upload
  2: "50%"       # 50% of max_upload
```

### Complete Example

See [`config.stream_based.example.yaml`](config.stream_based.example.yaml) for a fully documented configuration with detailed comments and multiple examples.

### Backward Compatibility

This feature is completely optional. Existing configurations without `stream_based_speeds` will continue to work with the traditional bandwidth-based speed control.



## Contributing
Anyone is welcome to contribute! Feel free to open pull requests.

## Issues and Bugs
Please report any bugs in the <a href="https://github.com/itschasa/speedrr/issues">Issues</a> section.

## Feature Suggestions
Got an idea for the project? Suggest it <a href="https://github.com/itschasa/speedrr/issues">here</a>!
