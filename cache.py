import os
import hashlib
from typing import Optional
from redis.asyncio import Redis

REDIS_ADDR = os.getenv("REDIS_ADDR", "localhost:6379")
CACHE_TTL = os.getenv("CACHE_TTL", "5m")

redis_client: Optional[Redis] = None

def get_redis() -> Optional[Redis]:
    global redis_client
    if redis_client is None and REDIS_ADDR:
        # Parse Host and Port
        host = REDIS_ADDR
        port = 6379
        if ":" in REDIS_ADDR:
            parts = REDIS_ADDR.split(":")
            host = parts[0]
            try:
                port = int(parts[1])
            except ValueError:
                pass
        redis_client = Redis(host=host, port=port, decode_responses=True)
    return redis_client

async def close_redis():
    global redis_client
    if redis_client is not None:
        await redis_client.close()
        redis_client = None

def get_cache_ttl_seconds() -> int:
    # Parse m, s, h durations. e.g. "5m", "10s", "1h"
    val = CACHE_TTL.strip()
    if val.endswith("s"):
        return int(val[:-1])
    if val.endswith("m"):
        return int(val[:-1]) * 60
    if val.endswith("h"):
        return int(val[:-1]) * 3600
    try:
        return int(val)
    except ValueError:
        return 300  # default 5 minutes

def compute_cache_key(model: str, conversation_id: str, prompt: str) -> str:
    hasher = hashlib.sha256()
    hasher.update(model.encode("utf-8"))
    hasher.update(b":")
    hasher.update(conversation_id.encode("utf-8"))
    hasher.update(b":")
    hasher.update(prompt.encode("utf-8"))
    return f"cache:inference:{hasher.hexdigest()}"

async def get_cached_response(key: str) -> Optional[str]:
    client = get_redis()
    if client is None:
        return None
    try:
        return await client.get(key)
    except Exception:
        return None

async def set_cached_response(key: str, value: str):
    client = get_redis()
    if client is None:
        return
    try:
        ttl = get_cache_ttl_seconds()
        await client.set(key, value, ex=ttl)
    except Exception:
        pass
