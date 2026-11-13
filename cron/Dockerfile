FROM python:3.12-alpine
WORKDIR /app

COPY requirements.txt ./
RUN pip install --no-cache-dir -r requirements.txt

COPY check_status.py ./

CMD ["python", "/app/check_status.py"]