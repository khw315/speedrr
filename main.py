"""Speedrr main application entrypoint."""

import sys
import threading
import traceback
from typing import Union, List, Any

from helpers.log_loader import logger
from helpers import arguments, config, log_loader
from clients import qbittorrent, transmission
from modules import media_server, schedule


def _calculate_base_upload_speed(modules: list, cfg: config.SpeedrrConfig) -> Union[int, float]:
    """Calculate base upload speed for stream-based mode."""
    for module in modules:
        if isinstance(module, media_server.MediaServerModule):
            target_speed = module.get_target_upload_speed()
            if isinstance(target_speed, str):
                if target_speed.lower() == "unlimited":
                    return float('inf')
                if target_speed.endswith('%'):
                    percentage = int(target_speed[:-1]) / 100
                    return cfg.max_upload * percentage
                return float(target_speed)
            return target_speed
    return cfg.max_upload


def _calculate_stream_mode_upload(
    base_upload_speed: Union[int, float],
    schedule_reductions: list,
    min_upload: Union[int, float],
    max_upload: Union[int, float]
) -> Union[int, float]:
    """Calculate upload speed when stream-based speeds are active."""
    if any(r == float('inf') for r in schedule_reductions):
        return float('inf')

    if not schedule_reductions:
        return base_upload_speed

    reference_upload = max_upload if base_upload_speed == float('inf') else base_upload_speed
    return max(min_upload, reference_upload - sum(schedule_reductions))


def _calculate_target_speeds(
    modules: list, cfg: config.SpeedrrConfig
) -> tuple[Union[int, float], Union[int, float]]:
    """Calculate target upload and download speeds from module reduction values."""
    module_reductions = [m.get_reduction_value() for m in modules]
    upload_reductions = [m[0] for m in module_reductions]
    download_reductions = [m[1] for m in module_reductions]

    if any(r == float('-inf') for r in upload_reductions):
        base_upload = _calculate_base_upload_speed(modules, cfg)
        schedule_reductions = [r for r in upload_reductions if r != float('-inf')]
        new_upload = _calculate_stream_mode_upload(
            base_upload, schedule_reductions, cfg.min_upload, cfg.max_upload
        )
    else:
        new_upload = (
            float('inf') if any(r == float('inf') for r in upload_reductions)
            else max(cfg.min_upload, cfg.max_upload - sum(upload_reductions))
        )

    new_download = (
        float('inf') if any(r == float('inf') for r in download_reductions)
        else max(cfg.min_download, cfg.max_download - sum(download_reductions))
    )

    return new_upload, new_download


def _log_calculated_speeds(
    new_upload_speed: Union[int, float],
    new_download_speed: Union[int, float],
    units: str
) -> None:
    """Log calculated speeds."""
    if new_upload_speed == float('inf'):
        logger.info("New calculated upload speed: unlimited")
    else:
        logger.info("New calculated upload speed: %s%s", new_upload_speed, units)

    if new_download_speed == float('inf'):
        logger.info("New calculated download speed: unlimited")
    else:
        logger.info("New calculated download speed: %s%s", new_download_speed, units)


def _update_client_speed(
    torrent_client: Any,
    effective_upload_speed: Union[int, float],
    effective_download_speed: Union[int, float],
    units: str
) -> None:
    """Apply speed limits to a single torrent client."""
    c_config = torrent_client.client_config
    try:
        torrent_client.set_upload_speed(effective_upload_speed)
        torrent_client.set_download_speed(effective_download_speed)
    except Exception:  # pylint: disable=broad-exception-caught
        logger.warning(
            "An error occurred while updating %s, skipping:\n%s",
            c_config.url, traceback.format_exc()
        )
    else:
        if effective_upload_speed == float('inf'):
            logger.info("Set upload speed for %s to unlimited", c_config.url)
        else:
            logger.info(
                "Set upload speed for %s to %s%s", c_config.url, effective_upload_speed, units
            )

        if effective_download_speed == float('inf'):
            logger.info("Set download speed for %s to unlimited", c_config.url)
        else:
            logger.info(
                "Set download speed for %s to %s%s", c_config.url, effective_download_speed, units
            )


def _apply_speeds_to_clients(  # pylint: disable=too-many-arguments,too-many-positional-arguments
    clients: list,
    cfg: config.SpeedrrConfig,
    new_upload_speed: Union[int, float],
    new_download_speed: Union[int, float],
    sum_client_upload_shares: int,
    sum_client_download_shares: int
) -> None:
    """Calculate and set effective upload/download speeds for all clients."""
    logger.info("Getting active torrent counts")
    client_active_torrent_dict = {c: c.get_active_torrent_count() for c in clients}
    sum_active_torrents = sum(client_active_torrent_dict.values())

    for torrent_client, active_torrent_count in client_active_torrent_dict.items():
        c_config = torrent_client.client_config

        if new_upload_speed == float('inf'):
            eff_upload = float('inf')
        elif cfg.manual_speed_algorithm_share:
            eff_upload = c_config.download_shares / sum_client_upload_shares * new_upload_speed
        else:
            eff_upload = (
                (active_torrent_count / sum_active_torrents * new_upload_speed)
                if active_torrent_count > 0 else new_upload_speed
            )

        if new_download_speed == float('inf'):
            eff_download = float('inf')
        elif cfg.manual_speed_algorithm_share:
            eff_download = c_config.upload_shares / sum_client_download_shares * new_download_speed
        else:
            eff_download = (
                (active_torrent_count / sum_active_torrents * new_download_speed)
                if active_torrent_count > 0 else new_download_speed
            )

        _update_client_speed(torrent_client, eff_upload, eff_download, cfg.units)


def _init_clients(cfg: config.SpeedrrConfig) -> list:
    """Initialize torrent clients from configuration."""
    clients: List[Union[qbittorrent.QBittorrentClient, transmission.TransmissionClient]] = []
    for client in cfg.clients:
        if client.type == "qbittorrent":
            clients.append(qbittorrent.QBittorrentClient(cfg, client))
        elif client.type == "transmission":
            clients.append(transmission.TransmissionClient(cfg, client))
        else:
            logger.critical("Unknown client type in config: %s", client.type)
            sys.exit(1)
    return clients


def _init_modules(cfg: config.SpeedrrConfig, update_event: threading.Event) -> list:
    """Initialize active modules from configuration."""
    modules: List[Union[media_server.MediaServerModule, schedule.ScheduleModule]] = []
    if cfg.modules.media_servers:
        modules.append(
            media_server.MediaServerModule(cfg, cfg.modules.media_servers, update_event)
        )
    if cfg.modules.schedule:
        modules.append(
            schedule.ScheduleModule(cfg, cfg.modules.schedule, update_event)
        )

    if not modules:
        logger.critical("No modules enabled in config, exiting")
        sys.exit(1)

    for module in modules:
        module.run()
        logger.info("Started module: %s", module.__class__.__name__)

    return modules


def main() -> None:
    """Run Speedrr application main loop."""
    args = arguments.load_args()
    logger.debug("Loading config")

    if not args.config:
        logger.critical("No config file specified, use --config_path arg or SPEEDRR_CONFIG env var")
        sys.exit(1)

    cfg = config.load_config(args.config)

    if cfg.logs_path:
        log_loader.set_file_handler(cfg.logs_path, args.log_file_level)

    log_loader.stdout_handler.setLevel(args.log_level)
    logger.info("Starting Speedrr")

    update_event = threading.Event()

    clients = _init_clients(cfg)
    sum_client_upload_shares = sum(c.upload_shares for c in cfg.clients)
    sum_client_download_shares = sum(c.download_shares for c in cfg.clients)

    modules = _init_modules(cfg, update_event)
    update_event.set()

    while True:
        if not update_event.wait(timeout=0.2):
            continue

        update_event.clear()
        logger.info("Update event triggered")

        try:
            new_upload, new_download = _calculate_target_speeds(modules, cfg)
            _log_calculated_speeds(new_upload, new_download, cfg.units)
            _apply_speeds_to_clients(
                clients, cfg, new_upload, new_download,
                sum_client_upload_shares, sum_client_download_shares
            )
            logger.info("Speeds updated")
        except Exception:  # pylint: disable=broad-exception-caught
            logger.error("An error occurred while updating clients:\n%s", traceback.format_exc())

        logger.info("Waiting for next update event")


if __name__ == '__main__':
    main()
