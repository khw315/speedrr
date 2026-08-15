"""Transmission client integration."""

from typing import Union
import urllib.parse
import transmission_rpc
from transmission_rpc.error import (
    TransmissionAuthError,
    TransmissionConnectError,
    TransmissionTimeoutError,
)

from helpers.config import SpeedrrConfig, ClientConfig
from helpers.log_loader import logger
from helpers.bit_convert import bit_conv


class TransmissionClient:
    """Transmission client wrapper."""

    def __init__(self, config: SpeedrrConfig, config_client: ClientConfig) -> None:
        self._client_config = config_client
        self._config = config

        # Gets hostname, port, and path from url and checks if values are sensible
        u = urllib.parse.urlparse(config_client.url)

        protocol = u.scheme
        if protocol == "http":
            default_port = 80
        elif protocol == "https":
            default_port = 443
        else:
            raise ValueError(f"<trans|{self._client_config.url}> Unknown url scheme {u.scheme}")

        if u.hostname is None:
            raise ValueError(f"<trans|{self._client_config.url}> Missing hostname")

        logger.debug(
            "<trans|%s> Connecting to Transmission at %s",
            self._client_config.url,
            config_client.url
        )

        try:
            self._client = transmission_rpc.Client(
                protocol=protocol,
                username=config_client.username,
                password=config_client.password,
                host=u.hostname,
                port=u.port or default_port,
                path=u.path or "/transmission/rpc",
            )
        except TransmissionTimeoutError as err:
            raise RuntimeError(
                f"<trans|{self._client_config.url}> Connection to Transmission timed out"
            ) from err
        except TransmissionAuthError as err:
            raise RuntimeError(
                f"<trans|{self._client_config.url}> Failed to login to Transmission, "
                "check your credentials"
            ) from err
        except TransmissionConnectError as err:
            raise RuntimeError(
                f"<trans|{self._client_config.url}> Failed to connect to Transmission, "
                "check your url"
            ) from err

        logger.debug("<trans|%s> Connected to Transmission", self._client_config.url)

    @property
    def client_config(self) -> ClientConfig:
        """Return client configuration."""
        return self._client_config

    def get_active_torrent_count(self) -> int:
        """Get the number of torrents that are currently downloading or uploading."""
        logger.debug("<trans|%s> Getting active torrent count", self._client_config.url)

        session_stats = self._client.session_stats()
        return session_stats.active_torrent_count

    def set_upload_speed(self, speed: Union[int, float]) -> None:
        """Set the upload speed limit for the client, in config units."""
        if speed == float('inf'):
            logger.debug("<trans|%s> Setting upload speed to unlimited", self._client_config.url)
            self._client.set_session(speed_limit_up_enabled=False)
        else:
            logger.debug(
                "<trans|%s> Setting upload speed to %s%s",
                self._client_config.url, speed, self._config.units
            )
            speed_limit_up = max(1, int(bit_conv(speed, self._config.units, 'KB')))
            self._client.set_session(
                speed_limit_up_enabled=True, speed_limit_up=speed_limit_up
            )

    def set_download_speed(self, speed: Union[int, float]) -> None:
        """Set the download speed limit for the client, in config units."""
        if speed == float('inf'):
            logger.debug(
                "<trans|%s> Setting download speed to unlimited", self._client_config.url
            )
            self._client.set_session(speed_limit_down_enabled=False)
        else:
            logger.debug(
                "<trans|%s> Setting dowload speed to %s%s",
                self._client_config.url, speed, self._config.units
            )
            speed_limit_down = max(1, int(bit_conv(speed, self._config.units, "KB")))
            self._client.set_session(
                speed_limit_down_enabled=True, speed_limit_down=speed_limit_down
            )
