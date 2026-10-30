import sys

from fastapi import FastAPI

app = FastAPI()


@app.get("/")
def root():
    return f"hello from python {sys.version_info.major}.{sys.version_info.minor}"
