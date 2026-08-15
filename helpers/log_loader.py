"""Logger loader and formatter module."""

import datetime
import logging
import pathlib
import sys
import traceback
from colorama import Fore

LOGGER_NAME = "speedrr"
DEFAULT_STDOUT_LOG_LEVEL = logging.INFO
FILE_LOG_NAME = f"{datetime.datetime.now():%Y-%m-%d %H.%M.%S}.log"
LOG_FORMAT = '[%(asctime)s] [%(levelname)s] %(message)s (%(filename)s:%(lineno)d)'


class ColourFormatter(logging.Formatter):
    """Logging formatter with ANSI colors."""

    FORMATS = {
        logging.DEBUG:      Fore.LIGHTBLACK_EX + LOG_FORMAT + Fore.RESET,
        logging.INFO:       LOG_FORMAT + Fore.RESET,
        logging.WARNING:    Fore.YELLOW + LOG_FORMAT + Fore.RESET,
        logging.ERROR:      Fore.LIGHTRED_EX + LOG_FORMAT + Fore.RESET,
        logging.CRITICAL:   Fore.RED + LOG_FORMAT + Fore.RESET
    }

    def format(self, record):
        log_fmt = self.FORMATS.get(record.levelno)
        formatter = logging.Formatter(log_fmt)
        return formatter.format(record)


logger = logging.getLogger(LOGGER_NAME)  # pylint: disable=invalid-name
logger.setLevel(logging.DEBUG)

stdout_handler = logging.StreamHandler()  # pylint: disable=invalid-name
stdout_handler.setLevel(DEFAULT_STDOUT_LOG_LEVEL)
stdout_handler.setFormatter(ColourFormatter())
logger.addHandler(stdout_handler)


def set_file_handler(folder: str, level: int) -> None:
    """Set up log file handler."""
    path = pathlib.Path(folder)
    path.mkdir(parents=True, exist_ok=True)

    file_handler = logging.FileHandler(
        str(pathlib.Path(folder, FILE_LOG_NAME)), encoding="utf-8"
    )
    file_handler.setLevel(level)
    file_handler.setFormatter(logging.Formatter(LOG_FORMAT))
    logger.addHandler(file_handler)


def handle_exception(exc_type, exc_value, exc_traceback):
    """Global unhandled exception handler."""
    if issubclass(exc_type, KeyboardInterrupt):
        sys.__excepthook__(exc_type, exc_value, exc_traceback)
        return

    tb_str = ' '.join(traceback.format_exception(exc_type, exc_value, exc_traceback))
    logger.error("Uncaught exception: %s", tb_str)


sys.excepthook = handle_exception
