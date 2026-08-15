FROM python:3.10-alpine

LABEL org.opencontainers.image.source="https://github.com/khw315/speedrr"
LABEL org.opencontainers.image.licenses=GPL-3.0
LABEL org.opencontainers.image.description="Dynamically manage speeds on torrent clients, with Plex/Jellyfin/Emby intergration."

WORKDIR /home

COPY requirements.txt main.py /home/
COPY clients /home/clients
COPY helpers /home/helpers
COPY modules /home/modules

RUN pip install --no-cache-dir --require-hashes --only-binary :all: -r requirements.txt \
    && addgroup -S speedrr \
    && adduser -S speedrr -G speedrr \
    && chown -R speedrr:speedrr /home

USER speedrr

CMD ["python", "./main.py"]
