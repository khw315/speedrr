FROM python:3.10-alpine

LABEL org.opencontainers.image.source = "https://github.com/khw315/speedrr"
LABEL org.opencontainers.image.licenses=GPL-3.0
LABEL org.opencontainers.image.description="Dynamically manage speeds on torrent clients, with Plex/Jellyfin/Emby intergration."

ADD . /home

WORKDIR /home

RUN pip install -r requirements.txt

CMD ["python", "./main.py"]
