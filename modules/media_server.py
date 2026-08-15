"""Media server module (Plex, Tautulli, Jellyfin, Emby)."""

import ipaddress
import sys
import threading
import time
import traceback
from typing import Union, List

import httpx

from helpers.config import SpeedrrConfig, MediaServerConfig
from helpers.log_loader import logger
from helpers.bit_convert import bit_conv

LOG_GETTING_BANDWIDTH = "%s Getting bandwidth"


class MediaServerModule:
    """Module managing media server stream and bandwidth monitoring."""

    def __init__(
        self,
        config: SpeedrrConfig,
        module_config: List[MediaServerConfig],
        update_event: threading.Event
    ) -> None:
        self.reduction_value_dict: dict[MediaServerConfig, float] = {}
        self.stream_count_dict: dict[MediaServerConfig, int] = {}

        self._config = config
        self._module_config = module_config
        self._update_event = update_event

        self.servers: list[Union[PlexServer, TautulliServer, JellyfinServer, EmbyServer]] = []

        for server in self._module_config:
            if server.type == "plex":
                self.servers.append(PlexServer(config, server, self))
            elif server.type == "tautulli":
                self.servers.append(TautulliServer(config, server, self))
            elif server.type == "jellyfin":
                self.servers.append(JellyfinServer(config, server, self))
            elif server.type == "emby":
                self.servers.append(EmbyServer(config, server, self))
            else:
                logger.critical(
                    "<media_servers> Unknown media server type in config: %s", server.type
                )
                sys.exit(1)

            self._initial_bandwidth_check(self.servers[-1])

    def notify_update(self) -> None:
        """Trigger update event."""
        self._update_event.set()

    def _initial_bandwidth_check(
        self, server: 'BaseServer', max_retries: int = 5, base_delay: float = 2.0
    ) -> None:
        """Attempt an initial bandwidth check with retry + exponential backoff."""
        for attempt in range(1, max_retries + 1):
            try:
                server.get_bandwidth()
                return
            except Exception:  # pylint: disable=broad-exception-caught
                if attempt < max_retries:
                    delay = base_delay * (2 ** (attempt - 1))
                    logger.warning(
                        "<media_servers> Initial bandwidth check for %s failed "
                        "(attempt %s/%s), retrying in %ss...\n%s",
                        server.server_config.url, attempt, max_retries, delay,
                        traceback.format_exc()
                    )
                    time.sleep(delay)
                else:
                    logger.error(
                        "<media_servers> Initial bandwidth check for %s failed "
                        "after %s attempts. Starting with 0 bandwidth; "
                        "will recover on next polling cycle.\n%s",
                        server.server_config.url, max_retries,
                        traceback.format_exc()
                    )
                    server.set_reduction(0)
                    server.set_stream_count(0)

    def get_reduction_value(self) -> tuple[float, float]:
        """How much to reduce speed by. Returns tuple (upload, download)."""
        stream_based_servers = [
            server for server in self._module_config
            if server.stream_based_speeds and server.stream_based_speeds.enabled
        ]

        if stream_based_servers:
            total_streams = sum(self.stream_count_dict.values())
            logger.info("<media_servers> Total active streams: %s", total_streams)
            counts_str = '; '.join(
                f'{server.url}: {count}' for server, count in self.stream_count_dict.items()
            )
            logger.info("<media_servers> Stream counts per server = %s", counts_str)
            return float('-inf'), 0

        reductions_str = '; '.join(
            f'{server.url}: {reduction}'
            for server, reduction in self.reduction_value_dict.items()
        )
        logger.info("<media_servers> Upload reduction values = %s", reductions_str)
        return sum(self.reduction_value_dict.values()), 0

    def get_stream_count(self) -> int:
        """Get total number of active streams across all servers."""
        return sum(self.stream_count_dict.values())

    def get_target_upload_speed(self) -> Union[int, float, str]:
        """Get target upload speed based on stream count for stream-based mode."""
        total_streams = self.get_stream_count()

        for server_config in self._module_config:
            if server_config.stream_based_speeds and server_config.stream_based_speeds.enabled:
                speeds_config = server_config.stream_based_speeds

                if total_streams in speeds_config.speeds:
                    return speeds_config.speeds[total_streams]

                applicable = [
                    c for c in speeds_config.speeds.keys() if c <= total_streams
                ]
                if applicable:
                    return speeds_config.speeds[max(applicable)]

                if speeds_config.default is not None:
                    return speeds_config.default

                return self._config.max_upload

        return self._config.max_upload

    def run(self) -> None:
        """Start media server threads."""
        for server in self.servers:
            server.daemon = True
            server.start()


class BaseServer(threading.Thread):
    """Base class for media server connections."""

    def __init__(
        self,
        config: SpeedrrConfig,
        server_config: MediaServerConfig,
        module: MediaServerModule
    ) -> None:
        threading.Thread.__init__(self)

        self._config = config
        self._server_config = server_config
        self._module = module

        self._client = httpx.Client(
            base_url=self._server_config.url,
            verify=self._server_config.https_verify
        )

        self._paused_since: dict[str, int] = {}
        self._logger_prefix = f"<{self._server_config.type}|{self._server_config.url}>"
        self._module.reduction_value_dict[self._server_config] = 0

    @property
    def server_config(self) -> MediaServerConfig:
        """Return server configuration."""
        return self._server_config

    def get_bandwidth(self) -> int:
        """Get current bandwidth usage from server, in Kbit/s."""
        raise NotImplementedError("get_bandwidth must be implemented in a subclass")

    def set_reduction(self, reduction) -> None:
        """Set upload speed reduction for server, in config units."""
        reduction = bit_conv(reduction, "Kbit", self._config.units)
        old_reduction = self._module.reduction_value_dict.get(self._server_config)

        if old_reduction == reduction:
            return

        self._module.reduction_value_dict[self._server_config] = reduction
        self._module.notify_update()

    def set_stream_count(self, count: int) -> None:
        """Set stream count for server and dispatch an update event."""
        old_count = self._module.stream_count_dict.get(self._server_config)
        if old_count == count:
            return

        self._module.stream_count_dict[self._server_config] = count
        self._module.notify_update()

    def _is_local_stream(self, ip_address: str) -> bool:
        """Check if an IP address belongs to a local stream."""
        if self._server_config.ignore_streams.local:
            if ip_address == "lan" or ipaddress.ip_address(ip_address).is_private:
                return True
        if self._server_config.ignore_streams.ip_networks:
            ip = ipaddress.ip_address(ip_address)
            networks = (
                ipaddress.ip_network(network)
                for network in self._server_config.ignore_streams.ip_networks
            )
            if any(ip in network for network in networks):
                return True
        return False

    # pylint: disable=too-many-arguments,too-many-positional-arguments
    def process_session(
        self, bandwidth: int, paused: bool, ip_address: str, session_id: str, title: str
    ) -> int:
        """Process a session and return bandwidth usage."""
        if paused and self._server_config.ignore_streams.paused_after != -1:
            if session_id not in self._paused_since:
                self._paused_since[session_id] = int(time.time())
                logger.debug(
                    "%s %s:%s is paused, noted time", self._logger_prefix, title, session_id
                )
            elif (
                int(time.time()) - self._paused_since[session_id]
                > self._server_config.ignore_streams.paused_after
            ):
                logger.debug(
                    "%s Removing %s:%s from count, paused for too long",
                    self._logger_prefix, title, session_id
                )
                return 0
        elif self._server_config.ignore_streams.paused_after != -1:
            if session_id in self._paused_since:
                logger.debug(
                    "%s %s:%s is no longer paused, removing from paused dict",
                    self._logger_prefix, title, session_id
                )
                del self._paused_since[session_id]

        if self._is_local_stream(ip_address):
            logger.debug(
                "%s Ignoring local stream %s:%s (%s)",
                self._logger_prefix, title, session_id, ip_address
            )
            return 0

        logger.debug(
            "%s Adding %s to count for %s:%s",
            self._logger_prefix, bandwidth, title, session_id
        )
        return bandwidth

    def remove_old_paused(self, active_session_ids: list[str]) -> None:
        """Remove sessions from paused dict if no longer active."""
        expired = [sid for sid in self._paused_since if sid not in active_session_ids]
        for session_id in expired:
            logger.debug(
                "%s Removing %s from paused_since, no longer in session list",
                self._logger_prefix, session_id
            )
            del self._paused_since[session_id]

    def run(self) -> None:
        while True:
            try:
                bandwidth = int(
                    self.get_bandwidth() * self._server_config.bandwidth_multiplier
                )
            except Exception:  # pylint: disable=broad-exception-caught
                logger.error(
                    "%s Error getting bandwidth:\n%s",
                    self._logger_prefix, traceback.format_exc()
                )
            else:
                self.set_reduction(bandwidth)

            time.sleep(self._server_config.update_interval)


class PlexServer(BaseServer):
    """Plex server integration."""

    def get_bandwidth(self) -> int:
        logger.debug(LOG_GETTING_BANDWIDTH, self._logger_prefix)
        res = self._client.get(
            "/status/sessions",
            params={"X-Plex-Token": self._server_config.token, "X-Plex-Language": "en"},
            headers={"Accept": "application/json"}
        )
        logger.debug("%s Got %s response from Plex", self._logger_prefix, res.status_code)
        res.raise_for_status()

        res_json: dict = res.json()
        if "MediaContainer" not in res_json:
            raise RuntimeError(f"Error from Plex: {res_json}")

        if res_json["MediaContainer"]["size"] == 0:
            logger.debug("%s No sessions found", self._logger_prefix)
            self.set_stream_count(0)
            return 0

        count = 0
        stream_count = 0
        session_ids: list[str] = []

        for session in res_json["MediaContainer"]["Metadata"]:
            session_ids.append(session["Session"]["id"])
            bandwidth = self.process_session(
                bandwidth=int(session["Session"]["bandwidth"]),
                paused=session["Player"]["state"] == "paused",
                ip_address=session["Player"]["address"],
                session_id=session["Session"]["id"],
                title=session["title"]
            )
            count += bandwidth
            if bandwidth > 0:
                stream_count += 1

        self.remove_old_paused(session_ids)
        self.set_stream_count(stream_count)
        return count


class TautulliServer(BaseServer):
    """Tautulli server integration."""

    def get_bandwidth(self) -> int:
        logger.debug(LOG_GETTING_BANDWIDTH, self._logger_prefix)
        res = self._client.get(
            "/api/v2",
            params={"apikey": self._server_config.api_key, "cmd": "get_activity"}
        )
        logger.debug("%s Got %s response from Tautulli", self._logger_prefix, res.status_code)
        res.raise_for_status()

        res_json: dict = res.json()
        if res_json["response"]["result"] != "success":
            raise RuntimeError(f"Error from Tautulli: {res_json['response']['message']}")

        count = 0
        stream_count = 0
        session_ids: list[str] = []

        for session in res_json["response"]["data"]["sessions"]:
            session_ids.append(session["session_id"])
            bandwidth = self.process_session(
                bandwidth=int(session["bandwidth"]),
                paused=session["state"] == "paused",
                ip_address=session["ip_address"],
                session_id=session["session_id"],
                title=session["full_title"]
            )
            count += bandwidth
            if bandwidth > 0:
                stream_count += 1

        self.remove_old_paused(session_ids)
        self.set_stream_count(stream_count)
        return count


class JellyfinServer(BaseServer):
    """Jellyfin server integration."""

    def get_bandwidth(self) -> int:
        logger.debug(LOG_GETTING_BANDWIDTH, self._logger_prefix)
        res = self._client.get(
            "/Sessions",
            headers={"Authorization": f'MediaBrowser Token="{self._server_config.api_key}"'}
        )
        logger.debug("%s Got %s response from Jellyfin", self._logger_prefix, res.status_code)
        res.raise_for_status()

        res_json: list[dict] = res.json()
        count = 0
        stream_count = 0
        session_ids: list[str] = []

        for session in res_json:
            if session.get("NowPlayingItem"):
                session_ids.append(session["Id"])
                if session["PlayState"]["PlayMethod"] in ["DirectPlay", "DirectStream"]:
                    logger.debug(
                        "%s %s is direct play, calculating estimated bandwidth from MediaStreams",
                        self._logger_prefix, session['Id']
                    )
                    bandwidth = sum(
                        int(stream.get("BitRate", 0))
                        for stream in session["NowPlayingItem"]["MediaStreams"]
                    )
                else:
                    bandwidth = int(session["TranscodingInfo"]["Bitrate"])

                processed = self.process_session(
                    bandwidth=bandwidth,
                    paused=session["PlayState"]["IsPaused"],
                    ip_address=session["RemoteEndPoint"],
                    session_id=session["Id"],
                    title=session["NowPlayingItem"]["Name"]
                )
                count += processed
                if processed > 0:
                    stream_count += 1

        self.remove_old_paused(session_ids)
        self.set_stream_count(stream_count)
        return int(round(bit_conv(count, 'bit', 'Kbit'), 0))


class EmbyServer(BaseServer):
    """Emby server integration."""

    def get_bandwidth(self) -> int:
        logger.debug(LOG_GETTING_BANDWIDTH, self._logger_prefix)
        res = self._client.get(f"/Sessions?api_key={self._server_config.api_key}")
        logger.debug("%s Got %s response from Emby", self._logger_prefix, res.status_code)
        res.raise_for_status()

        res_json: list[dict] = res.json()
        count = 0
        stream_count = 0
        session_ids: list[str] = []

        for session in res_json:
            if session.get("NowPlayingItem"):
                session_ids.append(session["Id"])
                if session["PlayState"]["PlayMethod"] == "Transcode":
                    bandwidth = int(session["TranscodingInfo"]["Bitrate"])
                else:
                    logger.debug(
                        "%s %s is direct play or direct stream, "
                        "calculating estimated bandwidth from MediaStreams",
                        self._logger_prefix, session['Id']
                    )
                    bandwidth = sum(
                        int(stream.get("BitRate", 0))
                        for stream in session["NowPlayingItem"]["MediaStreams"]
                    )

                processed = self.process_session(
                    bandwidth=bandwidth,
                    paused=session["PlayState"]["IsPaused"],
                    ip_address=session["RemoteEndPoint"],
                    session_id=session["Id"],
                    title=session["NowPlayingItem"]["Name"]
                )
                count += processed
                if processed > 0:
                    stream_count += 1

        self.remove_old_paused(session_ids)
        self.set_stream_count(stream_count)
        return int(round(bit_conv(count, 'bit', 'Kbit'), 0))
