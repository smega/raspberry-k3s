# Python 3.12: rpi-lgpio needs lgpio, which has no 3.13 wheel and does
# not build from source here (CI, 2026-11-15). Trixie rather than
# bookworm: bookworm's unfixed CVEs (zlib, util-linux) were all of this
# image's Trivy findings.
FROM python:3.12-slim-trixie

WORKDIR /app
COPY app/requirements.txt ./
RUN apt-get update \
    && apt-get install -y --no-install-recommends gcc python3-dev \
    && pip install --no-cache-dir -r requirements.txt \
    && apt-get purge -y gcc python3-dev \
    && apt-get autoremove -y \
    && rm -rf /var/lib/apt/lists/*

COPY app/led_temp.py ./

CMD ["python3", "/app/led_temp.py"]
