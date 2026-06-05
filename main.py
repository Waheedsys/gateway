import os
import json
import asyncio
import logging
from datetime import datetime, timezone
from contextlib import asynccontextmanager
from fastapi import FastAPI, HTTPException, Request, Response, status
from fastapi.responses import StreamingResponse
from pydantic import BaseModel, Field
from typing import Optional, Dict, Any, List

from db import (
    ensure_indexes, close_db,
    create_conversation, get_conversation, list_conversations, cancel_conversation,
    create_message, list_messages, get_last_n_messages, search_relevant_messages,
    create_inference_log, list_inference_logs, get_stats_24h
)
from cache import (
    close_redis, compute_cache_key, get_cached_response, set_cached_response
)
from events import bus, Event
from mcp_client import MCPClient
from agent import run_agent, run_agent_stream

logging.basicConfig(level=logging.INFO)
logger = logging.getLogger("gateway.main")

mcp_client = MCPClient()

@asynccontextmanager
async def lifespan(app: FastAPI):
    # Startup
    logger.info("Initializing gateway services...")
    await ensure_indexes()
    bus.start()
    yield
    # Shutdown
    logger.info("Stopping gateway services...")
    await bus.stop()
    await close_redis()
    await close_db()

app = FastAPI(title="AI Agent Gateway", lifespan=lifespan)

# --- Schemas ---

class CreateConversationRequest(BaseModel):
    title: Optional[str] = None

class CreateMessageRequest(BaseModel):
    role: str
    content: str

class InferenceRequest(BaseModel):
    content: str
    model: Optional[str] = None
    stream: Optional[bool] = False

class IngestLogRequest(BaseModel):
    provider: str
    model: str
    status: str
    latency_ms: int
    input_preview: str
    output_preview: Optional[str] = None
    error_message: Optional[str] = None
    raw_metadata: Optional[Dict[str, Any]] = None

class CallToolRequest(BaseModel):
    name: str
    arguments: Optional[Dict[str, Any]] = None

# --- Routes ---

@app.get("/health")
async def health():
    return Response(content="ok\n", media_type="text/plain")

@app.post("/conversations", status_code=status.HTTP_201_CREATED)
async def create_conv(req: CreateConversationRequest):
    title = req.title or "New conversation"
    try:
        conv = await create_conversation(title)
        return conv
    except Exception as e:
        logger.error(f"Failed to create conversation: {e}")
        raise HTTPException(status_code=500, detail="failed to create conversation")

@app.get("/conversations")
async def list_convs():
    try:
        return await list_conversations()
    except Exception as e:
        logger.error(f"Failed to list conversations: {e}")
        raise HTTPException(status_code=500, detail="failed to list conversations")

@app.get("/conversations/{conversation_id}")
async def get_conv(conversation_id: str):
    conv = await get_conversation(conversation_id)
    if not conv:
        raise HTTPException(status_code=404, detail="conversation not found")
    return conv

@app.post("/conversations/{conversation_id}/cancel", status_code=status.HTTP_204_NO_CONTENT)
async def cancel_conv(conversation_id: str):
    success = await cancel_conversation(conversation_id)
    if not success:
        raise HTTPException(status_code=404, detail="conversation not found")
    return Response(status_code=status.HTTP_204_NO_CONTENT)

@app.get("/conversations/{conversation_id}/messages")
async def list_conv_messages(conversation_id: str):
    try:
        return await list_messages(conversation_id)
    except Exception as e:
        logger.error(f"Failed to list messages: {e}")
        raise HTTPException(status_code=500, detail="failed to list messages")

@app.post("/conversations/{conversation_id}/messages", status_code=status.HTTP_201_CREATED)
async def create_conv_message(conversation_id: str, req: CreateMessageRequest):
    if not req.role or not req.content:
        raise HTTPException(status_code=400, detail="role and content are required")
    try:
        msg = await create_message(conversation_id, req.role, req.content)
        return msg
    except Exception as e:
        logger.error(f"Failed to create message: {e}")
        raise HTTPException(status_code=500, detail="failed to create message")

# --- Inference Agent Route ---

async def sse_cached_generator(conversation_id, user_message, model, cached_response, cache_key):
    yield f"data: {json.dumps({'event': 'start', 'user_message': user_message})}\n\n"
    
    requested_at = datetime.now(timezone.utc)
    words = cached_response.split(" ")
    for i, word in enumerate(words):
        chunk_text = word
        if i < len(words) - 1:
            chunk_text += " "
        yield f"data: {json.dumps({'event': 'text', 'text': chunk_text})}\n\n"
        await asyncio.sleep(0.015)
        
    responded_at = datetime.now(timezone.utc)
    latency_ms = int((responded_at - requested_at).total_seconds() * 1000)
    
    assistant_msg = await create_message(conversation_id, "assistant", cached_response)
    
    bus.publish(Event("inference.completed", {
        "conversation_id": conversation_id,
        "user_message_id": user_message["id"],
        "provider": os.getenv("LLM_PROVIDER", "openrouter"),
        "model": model,
        "latency_ms": latency_ms,
        "input_preview": user_message["content"][:200],
        "output_preview": cached_response[:200],
        "prompt_tokens": 0,
        "completion_tokens": 0,
        "total_tokens": 0,
        "raw_metadata": {"streaming": True, "cached": True},
        "requested_at": requested_at.isoformat(),
        "responded_at": responded_at.isoformat()
    }))
    
    yield f"data: {json.dumps({'event': 'done', 'assistant_message': assistant_msg})}\n\n"

async def sse_generator(conversation_id, user_message, model, prompt, history, cache_key):
    yield f"data: {json.dumps({'event': 'start', 'user_message': user_message})}\n\n"
    
    requested_at = datetime.now(timezone.utc)
    accumulated_text = []
    usage = {"prompt_tokens": 0, "completion_tokens": 0, "total_tokens": 0}
    raw_metadata = {}
    success = False
    error_msg = ""
    
    try:
        async for event in run_agent_stream(model, prompt, history, user_message["content"], mcp_client):
            ev_type = event["event"]
            if ev_type == "text":
                text = event["text"]
                accumulated_text.append(text)
                yield f"data: {json.dumps({'event': 'text', 'text': text})}\n\n"
            elif ev_type == "done":
                usage = event["usage"]
                raw_metadata = event["raw_metadata"]
                success = True
                
        if success:
            final_text = "".join(accumulated_text)
            responded_at = datetime.now(timezone.utc)
            latency_ms = int((responded_at - requested_at).total_seconds() * 1000)
            
            # Save assistant message in DB
            assistant_msg = await create_message(conversation_id, "assistant", final_text)
            
            # Cache in Redis
            await set_cached_response(cache_key, final_text)
            
            # Publish log event
            bus.publish(Event("inference.completed", {
                "conversation_id": conversation_id,
                "user_message_id": user_message["id"],
                "provider": os.getenv("LLM_PROVIDER", "openrouter"),
                "model": model,
                "latency_ms": latency_ms,
                "input_preview": user_message["content"][:200],
                "output_preview": final_text[:200],
                "prompt_tokens": usage.get("prompt_tokens", 0),
                "completion_tokens": usage.get("completion_tokens", 0),
                "total_tokens": usage.get("total_tokens", 0),
                "raw_metadata": raw_metadata,
                "requested_at": requested_at.isoformat(),
                "responded_at": responded_at.isoformat()
            }))
            
            # Yield done event
            yield f"data: {json.dumps({'event': 'done', 'assistant_message': assistant_msg})}\n\n"
            
    except Exception as e:
        error_msg = str(e)
        logger.error(f"Inference error: {e}", exc_info=True)
        yield f"data: {json.dumps({'event': 'error', 'error': error_msg})}\n\n"
        
        responded_at = datetime.now(timezone.utc)
        latency_ms = int((responded_at - requested_at).total_seconds() * 1000)
        bus.publish(Event("inference.failed", {
            "conversation_id": conversation_id,
            "user_message_id": user_message["id"],
            "provider": os.getenv("LLM_PROVIDER", "openrouter"),
            "model": model,
            "latency_ms": latency_ms,
            "input_preview": user_message["content"][:200],
            "error_message": error_msg,
            "raw_metadata": {"error": error_msg},
            "requested_at": requested_at.isoformat(),
            "responded_at": responded_at.isoformat()
        }))

@app.post("/conversations/{conversation_id}/infer")
async def run_inference(conversation_id: str, req: InferenceRequest):
    conv = await get_conversation(conversation_id)
    if not conv:
        raise HTTPException(status_code=404, detail="conversation not found")
    if conv.get("status") == "cancelled":
        raise HTTPException(status_code=409, detail="conversation is cancelled")
        
    content = req.content.strip()
    if not content:
        raise HTTPException(status_code=400, detail="content is required")
        
    model = req.model
    if not model:
        # Resolve default model based on provider
        provider = os.getenv("LLM_PROVIDER", "openrouter").lower()
        model = "claude-3-5-sonnet-20241022" if provider == "anthropic" else "openrouter/free"
        
    # Store user message
    user_message = await create_message(conversation_id, "user", content)
    
    # Retrieve context
    history = await get_last_n_messages(conversation_id, 8)
    # Exclude current message from history to prevent duplication
    history = [m for m in history if m["id"] != user_message["id"]]
    
    relevant = await search_relevant_messages(conversation_id, content, 5)
    
    # Deduplicate relevant context
    seen_ids = {m["id"] for m in history}
    relevant = [m for m in relevant if m["id"] not in seen_ids]
    
    # Build Elasticsearch retrieved context prompt
    system_prompt = "You are a helpful assistant. Continue this conversation using the recent context.\n\n"
    if relevant:
        system_prompt += "Relevant retrieved context from Elasticsearch:\n"
        for msg in relevant:
            role = msg["role"]
            text = msg["content"][:1500]
            system_prompt += f"- {role}: {text}\n"
        system_prompt += "\n"
        
    # Compute Redis cache key
    # Simple hash of prompt structure to matches Go logic
    prompt_struct_str = f"System: {system_prompt} User: {content}"
    cache_key = compute_cache_key(model, conversation_id, prompt_struct_str)
    
    cached_val = await get_cached_response(cache_key)
    
    if req.stream:
        if cached_val:
            return StreamingResponse(
                sse_cached_generator(conversation_id, user_message, model, cached_val, cache_key),
                media_type="text/event-stream"
            )
        else:
            return StreamingResponse(
                sse_generator(conversation_id, user_message, model, system_prompt, history, cache_key),
                media_type="text/event-stream"
            )
            
    # Non-streaming implementation
    if cached_val:
        requested_at = datetime.now(timezone.utc)
        responded_at = datetime.now(timezone.utc)
        latency_ms = int((responded_at - requested_at).total_seconds() * 1000)
        
        assistant_msg = await create_message(conversation_id, "assistant", cached_val)
        
        bus.publish(Event("inference.completed", {
            "conversation_id": conversation_id,
            "user_message_id": user_message["id"],
            "provider": os.getenv("LLM_PROVIDER", "openrouter"),
            "model": model,
            "latency_ms": latency_ms,
            "input_preview": content[:200],
            "output_preview": cached_val[:200],
            "prompt_tokens": 0,
            "completion_tokens": 0,
            "total_tokens": 0,
            "raw_metadata": {"streaming": False, "cached": True},
            "requested_at": requested_at.isoformat(),
            "responded_at": responded_at.isoformat()
        }))
        return {
            "user_message": user_message,
            "assistant_message": assistant_msg
        }
        
    requested_at = datetime.now(timezone.utc)
    try:
        assistant_text, usage, raw_metadata = await run_agent(
            model, system_prompt, history, content, mcp_client
        )
        responded_at = datetime.now(timezone.utc)
        latency_ms = int((responded_at - requested_at).total_seconds() * 1000)
        
        # Save assistant message
        assistant_msg = await create_message(conversation_id, "assistant", assistant_text)
        
        # Cache response
        await set_cached_response(cache_key, assistant_text)
        
        # Log event
        bus.publish(Event("inference.completed", {
            "conversation_id": conversation_id,
            "user_message_id": user_message["id"],
            "provider": os.getenv("LLM_PROVIDER", "openrouter"),
            "model": model,
            "latency_ms": latency_ms,
            "input_preview": content[:200],
            "output_preview": assistant_text[:200],
            "prompt_tokens": usage.get("prompt_tokens", 0),
            "completion_tokens": usage.get("completion_tokens", 0),
            "total_tokens": usage.get("total_tokens", 0),
            "raw_metadata": raw_metadata,
            "requested_at": requested_at.isoformat(),
            "responded_at": responded_at.isoformat()
        }))
        
        return {
            "user_message": user_message,
            "assistant_message": assistant_msg
        }
    except Exception as e:
        logger.error(f"Non-streaming Agent error: {e}", exc_info=True)
        responded_at = datetime.now(timezone.utc)
        latency_ms = int((responded_at - requested_at).total_seconds() * 1000)
        
        bus.publish(Event("inference.failed", {
            "conversation_id": conversation_id,
            "user_message_id": user_message["id"],
            "provider": os.getenv("LLM_PROVIDER", "openrouter"),
            "model": model,
            "latency_ms": latency_ms,
            "input_preview": content[:200],
            "error_message": str(e),
            "raw_metadata": {"error": str(e)},
            "requested_at": requested_at.isoformat(),
            "responded_at": responded_at.isoformat()
        }))
        raise HTTPException(status_code=502, detail=str(e))

# --- Other endpoints ---

@app.get("/conversations/{conversation_id}/inference-logs")
async def get_conv_inference_logs(conversation_id: str):
    try:
        return await list_inference_logs(conversation_id)
    except Exception as e:
        logger.error(f"Failed to list logs: {e}")
        raise HTTPException(status_code=500, detail="failed to list logs")

@app.post("/ingest/inference-logs", status_code=status.HTTP_201_CREATED)
async def ingest_log(req: IngestLogRequest):
    try:
        now = datetime.now(timezone.utc).isoformat()
        log_doc = {
            "provider": req.provider,
            "model": req.model,
            "status": req.status,
            "latency_ms": req.latency_ms,
            "input_preview": req.input_preview,
            "output_preview": req.output_preview or "",
            "error_message": req.error_message or "",
            "raw_metadata": req.raw_metadata or {},
            "requested_at": now,
            "responded_at": now
        }
        saved = await create_inference_log(log_doc)
        return saved
    except Exception as e:
        logger.error(f"Failed to ingest log: {e}")
        raise HTTPException(status_code=500, detail="failed to ingest log")

@app.get("/inference/stats")
async def get_stats():
    try:
        stats = await get_stats_24h()
        return stats
    except Exception as e:
        logger.error(f"Failed to get stats: {e}")
        raise HTTPException(status_code=500, detail="failed to get stats")

@app.get("/mcp/tools")
async def list_mcp_tools():
    if not mcp_client.is_enabled():
        raise HTTPException(status_code=503, detail="MCP_SERVER_COMMAND is not configured")
    try:
        tools = await mcp_client.list_tools()
        return {"tools": tools}
    except Exception as e:
        raise HTTPException(status_code=502, detail=str(e))

@app.post("/mcp/tools/call")
async def call_mcp_tool(req: CallToolRequest):
    if not mcp_client.is_enabled():
        raise HTTPException(status_code=503, detail="MCP_SERVER_COMMAND is not configured")
    try:
        result = await mcp_client.call_tool(req.name, req.arguments or {})
        return {"name": req.name, "result": result}
    except Exception as e:
        raise HTTPException(status_code=502, detail=str(e))
