"""Prompt-injection classifier sidecar for KeiRouter.

Serves a Hugging Face sequence-classification model (Llama Prompt Guard 2 by
default) with the text-classification contract KeiRouter's injection
detector speaks:

    POST /predict  {"inputs": ["text", ...]}
    -> [[{"label": "INJECTION", "score": p}, {"label": "SAFE", "score": 1 - p}], ...]
"""

import os

import torch
from fastapi import FastAPI
from pydantic import BaseModel
from transformers import AutoModelForSequenceClassification, AutoTokenizer

MODEL_ID = os.getenv("MODEL_ID", "meta-llama/Llama-Prompt-Guard-2-86M")
# Raw labels that mean "injection" for the loaded model. Prompt Guard 2 uses
# LABEL_1 (malicious); ProtectAI's DeBERTa models use INJECTION.
INJECTION_LABELS = {
    label.strip().upper()
    for label in os.getenv("INJECTION_LABELS", "LABEL_1,INJECTION,JAILBREAK,MALICIOUS").split(",")
    if label.strip()
}
MAX_LENGTH = int(os.getenv("MAX_LENGTH", "512"))
BATCH_SIZE = int(os.getenv("BATCH_SIZE", "16"))

torch.set_num_threads(int(os.getenv("TORCH_THREADS", "2")))
tokenizer = AutoTokenizer.from_pretrained(MODEL_ID)
model = AutoModelForSequenceClassification.from_pretrained(MODEL_ID).eval()
injection_ids = [i for i, name in model.config.id2label.items() if str(name).upper() in INJECTION_LABELS]
if not injection_ids:
    raise SystemExit(f"none of {sorted(INJECTION_LABELS)} is a label of {MODEL_ID}: {model.config.id2label}")

app = FastAPI(title="keirouter-promptguard")


class PredictRequest(BaseModel):
    inputs: str | list[str]
    truncate: bool = True


def score(texts: list[str]) -> list[float]:
    out: list[float] = []
    for i in range(0, len(texts), BATCH_SIZE):
        batch = tokenizer(
            texts[i : i + BATCH_SIZE],
            padding=True,
            truncation=True,
            max_length=MAX_LENGTH,
            return_tensors="pt",
        )
        with torch.inference_mode():
            probs = torch.softmax(model(**batch).logits, dim=-1)
        out.extend(float(probs[row, injection_ids].sum()) for row in range(probs.shape[0]))
    return out


@app.post("/predict")
def predict(req: PredictRequest):
    single = isinstance(req.inputs, str)
    texts = [req.inputs] if single else req.inputs
    results = [
        [{"label": "INJECTION", "score": p}, {"label": "SAFE", "score": 1.0 - p}]
        for p in score(texts)
    ] if texts else []
    return results[0] if single else results


@app.get("/health")
def health():
    return {"status": "ok", "model": MODEL_ID}
