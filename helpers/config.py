"""Configuration data models and loader."""

from dataclasses import dataclass
from typing import List, Optional, Union, Literal
from dataclass_wizard import YAMLWizard  # type: ignore # pylint: disable=no-name-in-module


@dataclass(frozen=True)
class ClientConfig(YAMLWizard):
    """Configuration model for a torrent client."""
    type: Literal['qbittorrent', 'deluge', 'transmission']
    url: str
    username: str
    password: str
    https_verify: bool
    download_shares: int = 1
    upload_shares: int = 1


@dataclass(frozen=True)
class IgnoreStreamConfig(YAMLWizard):
    """Configuration model for ignored streams."""
    local: bool
    ip_networks: Optional[tuple[str, ...]]
    paused_after: int


@dataclass(frozen=True)
class StreamBasedSpeedsConfig(YAMLWizard):
    """Configuration model for stream based speeds."""
    enabled: bool
    speeds: dict[int, Union[int, float, str]]
    default: Optional[Union[int, float, str]] = None


@dataclass(frozen=True)
class MediaServerConfig(YAMLWizard):  # pylint: disable=too-many-instance-attributes
    """Configuration model for a media server."""
    type: Literal['plex', 'tautulli', 'jellyfin', 'emby']
    url: str
    https_verify: bool
    bandwidth_multiplier: float
    update_interval: int
    ignore_streams: IgnoreStreamConfig
    token: Optional[str] = None
    api_key: Optional[str] = None
    stream_based_speeds: Optional[StreamBasedSpeedsConfig] = None


@dataclass(frozen=True)
class ScheduleConfig(YAMLWizard):
    """Configuration model for speed schedules."""
    start: str
    end: str
    days: tuple[Literal['all', 'mon', 'tue', 'wed', 'thu', 'fri', 'sat', 'sun'], ...]
    upload: Union[int, str]
    download: Union[int, str]


@dataclass(frozen=True)
class ModulesConfig(YAMLWizard):
    """Configuration model for optional modules."""
    media_servers: Optional[List[MediaServerConfig]]
    schedule: Optional[List[ScheduleConfig]]


@dataclass(frozen=True)
class SpeedrrConfig(YAMLWizard):  # pylint: disable=too-many-instance-attributes
    """Configuration model for main Speedrr config."""
    logs_path: Optional[str]
    units: Literal[
        'bit',
        'B',
        'byte',
        'Kbit',
        'kilobit',
        'Kibit',
        'kibibit',
        'KB',
        'kilobyte',
        'KiB',
        'kibibyte',
        'Mbit',
        'megabit',
        'Mibit',
        'mebibit',
        'MB',
        'megabyte',
        'MiB',
        'mebibyte',
        'Gbit',
        'gigabit',
        'Gibit',
        'gibibit',
        'GB',
        'gigabyte',
        'GiB',
        'gibibyte',
    ]
    min_upload: int
    max_upload: int
    min_download: int
    max_download: int
    clients: List[ClientConfig]
    modules: ModulesConfig
    manual_speed_algorithm_share: Optional[bool] = False


def load_config(config_file: str) -> SpeedrrConfig:
    """Load Speedrr configuration from a YAML file."""
    config = SpeedrrConfig.from_yaml_file(config_file)
    if isinstance(config, list):
        raise ValueError("Config can't be a list")
    return config
