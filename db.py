import os
import uuid
from datetime import datetime, timezone, timedelta
from typing import List, Dict, Any, Optional
from elasticsearch import AsyncElasticsearch, NotFoundError

ELASTICSEARCH_URL = os.getenv("ELASTICSEARCH_URL", "http://localhost:9200").rstrip("/")

es_client: Optional[AsyncElasticsearch] = None

def get_db() -> AsyncElasticsearch:
    global es_client
    if es_client is None:
        es_client = AsyncElasticsearch(ELASTICSEARCH_URL)
    return es_client

async def close_db():
    global es_client
    if es_client is not None:
        await es_client.close()
        es_client = None

def new_id() -> str:
    return str(uuid.uuid4())

async def ensure_indexes():
    client = get_db()
    indexes = {
        "conversations": {
            "mappings": {
                "properties": {
                    "id": {"type": "keyword"},
                    "title": {
                        "type": "text",
                        "fields": {"keyword": {"type": "keyword"}}
                    },
                    "status": {"type": "keyword"},
                    "created_at": {"type": "date"},
                    "updated_at": {"type": "date"}
                }
            }
        },
        "messages": {
            "mappings": {
                "properties": {
                    "id": {"type": "keyword"},
                    "conversation_id": {"type": "keyword"},
                    "role": {"type": "keyword"},
                    "content": {"type": "text"},
                    "created_at": {"type": "date"}
                }
            }
        },
        "inference_logs": {
            "mappings": {
                "properties": {
                    "id": {"type": "keyword"},
                    "conversation_id": {"type": "keyword"},
                    "message_id": {"type": "keyword"},
                    "provider": {"type": "keyword"},
                    "model": {"type": "keyword"},
                    "status": {"type": "keyword"},
                    "latency_ms": {"type": "integer"},
                    "prompt_tokens": {"type": "integer"},
                    "completion_tokens": {"type": "integer"},
                    "total_tokens": {"type": "integer"},
                    "input_preview": {"type": "text"},
                    "output_preview": {"type": "text"},
                    "error_message": {"type": "text"},
                    "raw_metadata": {"type": "object", "enabled": False},
                    "requested_at": {"type": "date"},
                    "responded_at": {"type": "date"}
                }
            }
        }
    }

    for index, body in indexes.items():
        exists = await client.indices.exists(index=index)
        if not exists:
            await client.indices.create(index=index, body=body)

# --- Conversation Repository ---

async def create_conversation(title: str) -> Dict[str, Any]:
    client = get_db()
    now = datetime.now(timezone.utc).isoformat()
    doc_id = new_id()
    doc = {
        "id": doc_id,
        "title": title,
        "status": "active",
        "created_at": now,
        "updated_at": now
    }
    await client.index(index="conversations", id=doc_id, document=doc, refresh="true")
    return doc

async def get_conversation(doc_id: str) -> Optional[Dict[str, Any]]:
    client = get_db()
    try:
        res = await client.get(index="conversations", id=doc_id)
        return res["_source"]
    except NotFoundError:
        return None

async def list_conversations() -> List[Dict[str, Any]]:
    client = get_db()
    query = {
        "size": 100,
        "sort": [{"updated_at": {"order": "desc"}}],
        "query": {"match_all": {}}
    }
    res = await client.search(index="conversations", body=query)
    return [hit["_source"] for hit in res["hits"]["hits"]]

async def cancel_conversation(doc_id: str) -> bool:
    client = get_db()
    now = datetime.now(timezone.utc).isoformat()
    try:
        await client.update(
            index="conversations",
            id=doc_id,
            body={"doc": {"status": "cancelled", "updated_at": now}},
            refresh="true"
        )
        return True
    except NotFoundError:
        return False

# --- Message Repository ---

async def create_message(conversation_id: str, role: str, content: str) -> Dict[str, Any]:
    client = get_db()
    doc_id = new_id()
    now = datetime.now(timezone.utc).isoformat()
    doc = {
        "id": doc_id,
        "conversation_id": conversation_id,
        "role": role,
        "content": content,
        "created_at": now
    }
    await client.index(index="messages", id=doc_id, document=doc, refresh="true")
    return doc

async def list_messages(conversation_id: str) -> List[Dict[str, Any]]:
    client = get_db()
    query = {
        "size": 500,
        "sort": [{"created_at": {"order": "asc"}}],
        "query": {
            "term": {"conversation_id": conversation_id}
        }
    }
    res = await client.search(index="messages", body=query)
    return [hit["_source"] for hit in res["hits"]["hits"]]

async def get_last_n_messages(conversation_id: str, n: int) -> List[Dict[str, Any]]:
    client = get_db()
    query = {
        "size": n,
        "sort": [{"created_at": {"order": "desc"}}],
        "query": {
            "term": {"conversation_id": conversation_id}
        }
    }
    res = await client.search(index="messages", body=query)
    msgs = [hit["_source"] for hit in res["hits"]["hits"]]
    msgs.reverse()  # Chronological order
    return msgs

async def search_relevant_messages(conversation_id: str, query_str: str, n: int) -> List[Dict[str, Any]]:
    client = get_db()
    query = {
        "size": n,
        "query": {
            "bool": {
                "filter": [
                    {"term": {"conversation_id": conversation_id}}
                ],
                "must": [
                    {"match": {"content": query_str}}
                ]
            }
        }
    }
    res = await client.search(index="messages", body=query)
    return [hit["_source"] for hit in res["hits"]["hits"]]

# --- Inference Log Repository ---

async def create_inference_log(log: Dict[str, Any]) -> Dict[str, Any]:
    client = get_db()
    doc = log.copy()
    if not doc.get("id"):
        doc["id"] = new_id()
    if not doc.get("requested_at"):
        doc["requested_at"] = datetime.now(timezone.utc).isoformat()
    if doc.get("raw_metadata") is None:
        doc["raw_metadata"] = {}
    await client.index(index="inference_logs", id=doc["id"], document=doc, refresh="true")
    return doc

async def list_inference_logs(conversation_id: str) -> List[Dict[str, Any]]:
    client = get_db()
    query = {
        "size": 500,
        "sort": [{"requested_at": {"order": "desc"}}],
        "query": {
            "term": {"conversation_id": conversation_id}
        }
    }
    res = await client.search(index="inference_logs", body=query)
    return [hit["_source"] for hit in res["hits"]["hits"]]

async def get_stats_24h() -> Dict[str, Any]:
    client = get_db()
    one_day_ago = datetime.now(timezone.utc) - timedelta(days=1)
    query = {
        "size": 10000,
        "query": {
            "range": {
                "requested_at": {
                    "gte": one_day_ago.isoformat()
                }
            }
        }
    }

    res = await client.search(index="inference_logs", body=query)
    hits = res["hits"]["hits"]
    
    errors = 0
    total_latency = 0
    total_tokens = 0
    
    for hit in hits:
        doc = hit["_source"]
        if doc.get("status") == "error":
            errors += 1
        total_latency += doc.get("latency_ms", 0)
        total_tokens += doc.get("total_tokens", 0)
        
    total = len(hits)
    avg_latency = 0.0
    avg_tokens = 0.0
    if total > 0:
        avg_latency = float(total_latency) / total
        avg_tokens = float(total_tokens) / total
        
    return {
        "total_requests": total,
        "error_count": errors,
        "avg_latency_ms": avg_latency,
        "total_tokens": total_tokens,
        "avg_tokens": avg_tokens
    }
