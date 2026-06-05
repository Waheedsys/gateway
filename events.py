import asyncio
import logging
from datetime import datetime, timezone
from typing import Dict, Any, Callable
from db import create_inference_log

logger = logging.getLogger("gateway.events")

class Event:
    def __init__(self, event_type: str, payload: Dict[str, Any]):
        self.type = event_type
        self.occurred_at = datetime.now(timezone.utc).isoformat()
        self.payload = payload

class EventBus:
    def __init__(self, buffer_size: int = 100):
        self._queue = asyncio.Queue(maxsize=buffer_size)
        self._worker_task = None

    def start(self):
        self._worker_task = asyncio.create_task(self._worker())

    async def stop(self):
        if self._worker_task:
            self._worker_task.cancel()
            try:
                await self._worker_task
            except asyncio.CancelledError:
                pass
            self._worker_task = None

    def publish(self, event: Event):
        try:
            self._queue.put_nowait(event)
        except asyncio.QueueFull:
            logger.warning(f"Event bus queue full. Dropping event: {event.type}")

    async def _worker(self):
        while True:
            try:
                event = await self._queue.get()
                await self._handle_event(event)
                self._queue.task_done()
            except asyncio.CancelledError:
                break
            except Exception as e:
                logger.error(f"Error in event worker loop: {e}", exc_info=True)

    async def _handle_event(self, event: Event):
        try:
            payload = event.payload
            
            if event.type == "inference.completed":
                log_doc = {
                    "conversation_id": payload.get("conversation_id"),
                    "message_id": payload.get("user_message_id"),
                    "provider": payload.get("provider"),
                    "model": payload.get("model"),
                    "status": "success",
                    "latency_ms": payload.get("latency_ms"),
                    "input_preview": payload.get("input_preview"),
                    "output_preview": payload.get("output_preview"),
                    "prompt_tokens": payload.get("prompt_tokens", 0),
                    "completion_tokens": payload.get("completion_tokens", 0),
                    "total_tokens": payload.get("total_tokens", 0),
                    "raw_metadata": payload.get("raw_metadata", {}),
                    "requested_at": payload.get("requested_at"),
                    "responded_at": payload.get("responded_at")
                }
                await create_inference_log(log_doc)
                
            elif event.type == "inference.failed":
                log_doc = {
                    "conversation_id": payload.get("conversation_id"),
                    "message_id": payload.get("user_message_id"),
                    "provider": payload.get("provider"),
                    "model": payload.get("model"),
                    "status": "error",
                    "latency_ms": payload.get("latency_ms"),
                    "input_preview": payload.get("input_preview"),
                    "error_message": payload.get("error_message"),
                    "raw_metadata": payload.get("raw_metadata", {}),
                    "requested_at": payload.get("requested_at"),
                    "responded_at": payload.get("responded_at")
                }
                await create_inference_log(log_doc)
                
        except Exception as e:
            logger.error(f"Failed to handle event {event.type}: {e}", exc_info=True)

# Global event bus
bus = EventBus(100)
