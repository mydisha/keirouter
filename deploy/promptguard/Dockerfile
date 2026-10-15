# Prompt-injection classifier sidecar (Llama Prompt Guard 2 by default).
# CPU-only PyTorch keeps the image small; an 86M model scores a 512-token
# chunk in tens of milliseconds on a few cores.
FROM python:3.12-slim

ENV PIP_NO_CACHE_DIR=1 \
    PYTHONUNBUFFERED=1 \
    HF_HOME=/models

WORKDIR /app
COPY requirements.txt .
RUN pip install --index-url https://download.pytorch.org/whl/cpu torch==2.5.1 \
 && pip install -r requirements.txt

COPY app.py .
EXPOSE 8000
CMD ["uvicorn", "app:app", "--host", "0.0.0.0", "--port", "8000", "--workers", "1"]
