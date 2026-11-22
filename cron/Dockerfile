FROM python:3.13-alpine
WORKDIR /app

COPY requirements.txt ./
# The checker runs, it doesn't install: pip leaves the image once the
# requirements are in. pip carries its own copies of msgpack and setuptools
# ( pip/_vendor ), with fixed HIGH vulnerabilities in them ( Trivy
# Operator, 2026-11-22 ).
RUN pip install --no-cache-dir --upgrade "setuptools>=78.1.1" \
 && pip install --no-cache-dir -r requirements.txt \
 && pip uninstall -y pip

COPY check_status.py ./

CMD ["python", "/app/check_status.py"]