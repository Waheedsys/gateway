import asyncio
from sentence_transformers import SentenceTransformer

_model = SentenceTransformer("all-MiniLM-L6-v2")

async def get_embedding(text: str) -> list[float]:
    loop = asyncio.get_running_loop()          # ← correct for Python 3.10+
    vec = await loop.run_in_executor(None, _model.encode, text[:8000])
    return vec.tolist()