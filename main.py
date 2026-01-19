import threading
from typing import Union, List
import traceback

from helpers.log_loader import logger
from helpers import arguments, config, log_loader
from clients import qbittorrent, transmission
from modules import media_server, schedule



if __name__ == '__main__':
    args = arguments.load_args()

    logger.debug("Loading config")

    if not args.config:
        logger.critical("No config file specified, use --config_path arg or SPEEDRR_CONFIG env var to specify a config file.")
        exit()

    cfg = config.load_config(args.config)

    if cfg.logs_path:
        log_loader.set_file_handler(cfg.logs_path, args.log_file_level)
    
    log_loader.stdout_handler.setLevel(args.log_level)
    
    logger.info("Starting Speedrr")

    
    update_event = threading.Event()
    

    clients: List[Union[qbittorrent.qBittorrentClient, transmission.TransmissionClient]] = []
    for client in cfg.clients:
        if client.type == "qbittorrent":
            torrent_client = qbittorrent.qBittorrentClient(cfg, client)

        elif client.type == "transmission":
            torrent_client = transmission.TransmissionClient(cfg, client)

        else:
            logger.critical(f"Unknown client type in config: {client.type}")
            exit()

        clients.append(torrent_client)
        
    sum_client_upload_shares = sum(client.upload_shares for client in cfg.clients)
    sum_client_download_shares = sum(client.download_shares for client in cfg.clients)

    modules: List[Union[media_server.MediaServerModule, schedule.ScheduleModule]] = []
    if cfg.modules.media_servers:
        plex_module = media_server.MediaServerModule(cfg, cfg.modules.media_servers, update_event)
        modules.append(plex_module)

    if cfg.modules.schedule:
        schedule_module = schedule.ScheduleModule(cfg, cfg.modules.schedule, update_event)
        modules.append(schedule_module)
    

    if not modules:
        logger.critical("No modules enabled in config, exiting")
        exit()
    

    for module in modules:
        module.run()
        logger.info(f"Started module: {module.__class__.__name__}")


    # Force an initial update
    update_event.set()

    while True:
        # Without a timeout, Ctrl+C won't work.
        # Polling isn't great, but it will work.
        event_triggered = update_event.wait(timeout=0.2)
        if not event_triggered:
            continue
        
        # Clear immediately, so that the next event can be set.
        update_event.clear()

        logger.info("Update event triggered")

        try:
            module_reduction_values = [
                module.get_reduction_value()
                for module in modules
            ]

            # These are in the config's units
            # If any module wants unlimited, set to inf for that direction
            upload_reductions = [module[0] for module in module_reduction_values]
            download_reductions = [module[1] for module in module_reduction_values]
            
            # Check if using stream-based speed mode (indicated by -inf)
            using_stream_based_speeds = any(r == float('-inf') for r in upload_reductions)
            
            if using_stream_based_speeds:
                # Stream-based mode: get BASE speed from media server module
                for module in modules:
                    if isinstance(module, media_server.MediaServerModule):
                        target_speed = module.get_target_upload_speed()
                        
                        # Handle different speed value types to get base speed
                        if isinstance(target_speed, str):
                            if target_speed.lower() == "unlimited":
                                base_upload_speed = float('inf')
                            elif target_speed.endswith('%'):
                                percentage = int(target_speed[:-1]) / 100
                                base_upload_speed = cfg.max_upload * percentage
                            else:
                                base_upload_speed = float(target_speed)
                        else:
                            base_upload_speed = target_speed
                        
                        break
                else:
                    # Fallback if no media server module found
                    base_upload_speed = cfg.max_upload
                
                # Now apply schedule reductions to the base speed
                # Filter out the stream-based indicator (-inf) from schedule reductions
                schedule_reductions = [r for r in upload_reductions if r != float('-inf')]
                
                if base_upload_speed == float('inf'):
                    # Base is unlimited
                    if any(r == float('inf') for r in schedule_reductions):
                        # Schedule also wants unlimited
                        new_upload_speed = float('inf')
                    elif schedule_reductions:
                        # Apply reduction to max_upload (since base is unlimited)
                        new_upload_speed = max(
                            cfg.min_upload,
                            cfg.max_upload - sum(schedule_reductions)
                        )
                    else:
                        # No schedule reductions, use unlimited
                        new_upload_speed = float('inf')
                else:
                    # Base is a specific value
                    if any(r == float('inf') for r in schedule_reductions):
                        # Schedule overrides to unlimited
                        new_upload_speed = float('inf')
                    elif schedule_reductions:
                        # Apply reduction to the base speed
                        new_upload_speed = max(
                            cfg.min_upload,
                            base_upload_speed - sum(schedule_reductions)
                        )
                    else:
                        # No schedule reductions, use base speed
                        new_upload_speed = base_upload_speed
                
                # Download speed calculation remains reduction-based
                if any(r == float('inf') for r in download_reductions):
                    new_download_speed = float('inf')
                else:
                    new_download_speed = max(
                        cfg.min_download,
                        (cfg.max_download - sum(download_reductions))
                    )
            else:
                # Bandwidth-based mode (original behavior)
                if any(r == float('inf') for r in upload_reductions):
                    new_upload_speed = float('inf')
                else:
                    new_upload_speed = max(
                        cfg.min_upload,
                        (cfg.max_upload - sum(upload_reductions))
                    )

                if any(r == float('inf') for r in download_reductions):
                    new_download_speed = float('inf')
                else:
                    new_download_speed = max(
                        cfg.min_download,
                        (cfg.max_download - sum(download_reductions))
                    )

            if new_upload_speed == float('inf'):
                logger.info(f"New calculated upload speed: unlimited")
            else:
                logger.info(f"New calculated upload speed: {new_upload_speed}{cfg.units}")
            
            if new_download_speed == float('inf'):
                logger.info(f"New calculated download speed: unlimited")
            else:
                logger.info(f"New calculated download speed: {new_download_speed}{cfg.units}")

            logger.info("Getting active torrent counts")

            client_active_torrent_dict = {
                client: client.get_active_torrent_count()
                for client in clients
            }

            sum_active_torrents = sum(client_active_torrent_dict.values())
            
            for torrent_client, active_torrent_count in client_active_torrent_dict.items():
                # If speed is unlimited, set it directly without splitting
                if new_upload_speed == float('inf'):
                    effective_upload_speed = float('inf')
                elif cfg.manual_speed_algorithm_share:
                    effective_upload_speed = (torrent_client._client_config.download_shares / sum_client_upload_shares * new_upload_speed)
                else: 
                    effective_upload_speed = (active_torrent_count / sum_active_torrents * new_upload_speed) if active_torrent_count > 0 else new_upload_speed
                
                if new_download_speed == float('inf'):
                    effective_download_speed = float('inf')
                elif cfg.manual_speed_algorithm_share:
                    effective_download_speed = (torrent_client._client_config.upload_shares / sum_client_download_shares * new_download_speed)
                else:
                    effective_download_speed = (active_torrent_count / sum_active_torrents * new_download_speed) if active_torrent_count > 0 else new_download_speed
                
                try:
                    torrent_client.set_upload_speed(effective_upload_speed)
                    torrent_client.set_download_speed(effective_download_speed)
                
                except Exception:
                    logger.warning(f"An error occurred while updating {torrent_client._client_config.url}, skipping:\n" + traceback.format_exc())
                
                else:
                    if effective_upload_speed == float('inf'):
                        logger.info(f"Set upload speed for {torrent_client._client_config.url} to unlimited")
                    else:
                        logger.info(f"Set upload speed for {torrent_client._client_config.url} to {effective_upload_speed}{cfg.units}")
                    
                    if effective_download_speed == float('inf'):
                        logger.info(f"Set download speed for {torrent_client._client_config.url} to unlimited")
                    else:
                        logger.info(f"Set download speed for {torrent_client._client_config.url} to {effective_download_speed}{cfg.units}")
            

            logger.info("Speeds updated")


        except Exception:
            logger.error("An error occurred while updating clients:\n" + traceback.format_exc())
        

        logger.info("Waiting for next update event")
