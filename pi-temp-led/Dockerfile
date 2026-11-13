FROM python:3.12-slim-bookworm

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
