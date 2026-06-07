import os
import logging
from typing import List, Dict, Any, AsyncGenerator, Tuple
from langchain_core.messages import SystemMessage, HumanMessage, AIMessage, BaseMessage
from langchain_core.tools import StructuredTool
from langchain_anthropic import ChatAnthropic
from langchain_openai import ChatOpenAI
from langgraph.prebuilt import create_react_agent

from mcp_client import MCPClient

logger = logging.getLogger("gateway.agent")

def get_llm(model_name: str):
    provider = os.getenv("LLM_PROVIDER", "openrouter").lower()
    if provider == "anthropic":
        api_key = os.getenv("ANTHROPIC_API_KEY", "")
        # default model if not provided
        m = model_name if model_name else "claude-3-5-sonnet-20241022"
        return ChatAnthropic(
            model=m,
            anthropic_api_key=api_key,
            temperature=0.0
        )
    else:  # openrouter / default
        api_key = os.getenv("OPENROUTER_API_KEY", "")
        m = model_name if model_name else "openrouter/free"
        return ChatOpenAI(
            model=m,
            openai_api_key=api_key,
            base_url="https://openrouter.ai/api/v1",
            temperature=0.0,
            default_headers={
                "HTTP-Referer": "http://localhost:8080",
                "X-Title": "AI Gateway Agent"
            }
        )

# def build_langchain_messages(system_prompt: str, history: List[Dict[str, Any]], current_user_msg: str) -> List[BaseMessage]:
#     messages: List[BaseMessage] = [SystemMessage(content=system_prompt)]
    
#     for msg in history:
#         role = msg.get("role")
#         content = msg.get("content", "")
#         if role == "user":
#             messages.append(HumanMessage(content=content))
#         elif role == "assistant":
#             messages.append(AIMessage(content=content))
#         elif role == "system":
#             messages.append(SystemMessage(content=content))
            
#     messages.append(HumanMessage(content=current_user_msg))
#     return messages

def build_langchain_messages(
    system_prompt: str,
    relevant: List[Dict[str, Any]],      # ✅ new param
    history: List[Dict[str, Any]],
    current_user_msg: str
) -> List[BaseMessage]:
    messages: List[BaseMessage] = [SystemMessage(content=system_prompt)]
    
    # Semantic context as real message turns, not raw text
    if relevant:
        messages.append(SystemMessage(
            content="The following are relevant messages from earlier in this conversation:"
        ))
        for msg in relevant:
            role = msg.get("role")
            content = msg.get("content", "")
            if role == "user":
                messages.append(HumanMessage(content=content))
            elif role == "assistant":
                messages.append(AIMessage(content=content))

    # Recent history (chronological)
    for msg in history:
        role = msg.get("role")
        content = msg.get("content", "")
        if role == "user":
            messages.append(HumanMessage(content=content))
        elif role == "assistant":
            messages.append(AIMessage(content=content))
        elif role == "system":
            messages.append(SystemMessage(content=content))

    # Current user turn
    messages.append(HumanMessage(content=current_user_msg))
    return messages

async def get_langchain_tools(mcp_client: MCPClient) -> List[StructuredTool]:
    if not mcp_client.is_enabled():
        return []
    
    try:
        mcp_tools = await mcp_client.list_tools()
        lc_tools = []
        
        for t in mcp_tools:
            name = t["name"]
            desc = t.get("description", f"Call MCP tool: {name}")
            # Dynamic wrapper function
            def make_tool_func(tool_name):
                async def _tool_func(**kwargs):
                    return await mcp_client.call_tool(tool_name, kwargs)
                return _tool_func
            
            # Construct a StructuredTool
            tool = StructuredTool.from_function(
                coroutine=make_tool_func(name),
                name=name,
                description=desc
            )
            lc_tools.append(tool)
            
        return lc_tools
    except Exception as e:
        logger.error(f"Failed to load LangChain tools from MCP: {e}", exc_info=True)
        return []

async def run_agent(
    model_name: str,
    system_prompt: str,
    relevant: List[Dict[str, Any]],
    history: List[Dict[str, Any]],
    user_message: str,
    mcp_client: MCPClient
) -> Tuple[str, Dict[str, Any], Dict[str, Any]]:
    """
    Runs the agent non-streamingly.
    Returns: (assistant_text, usage_metadata, raw_metadata)
    """
    llm = get_llm(model_name)
    tools = await get_langchain_tools(mcp_client)
    
    # Create the ReAct agent
    agent = create_react_agent(llm, tools)
    
    messages = build_langchain_messages(system_prompt, relevant, history, user_message) 
    
    response = await agent.ainvoke({"messages": messages})
    
    # The final message is the assistant's final response
    final_messages = response.get("messages", [])
    if not final_messages:
        return "", {}, {}
        
    last_msg = final_messages[-1]
    assistant_content = last_msg.content
    
    # Try to extract usage metadata
    usage = {}
    if hasattr(last_msg, "response_metadata") and last_msg.response_metadata:
        token_usage = last_msg.response_metadata.get("token_usage", {})
        if token_usage:
            usage = {
                "prompt_tokens": token_usage.get("prompt_tokens", 0),
                "completion_tokens": token_usage.get("completion_tokens", 0),
                "total_tokens": token_usage.get("total_tokens", 0)
            }
            
    # Compile tool call logs in metadata
    tool_calls = []
    for msg in final_messages:
        if hasattr(msg, "tool_calls") and msg.tool_calls:
            for tc in msg.tool_calls:
                tool_calls.append({
                    "name": tc.get("name"),
                    "args": tc.get("args")
                })
                
    raw_metadata = {
        "tool_calls": tool_calls,
        "provider": os.getenv("LLM_PROVIDER", "openrouter"),
        "model": model_name
    }
    
    return assistant_content, usage, raw_metadata

async def run_agent_stream(
    model_name: str,
    system_prompt: str,
    history: List[Dict[str, Any]],
    user_message: str,
    mcp_client: MCPClient
) -> AsyncGenerator[Dict[str, Any], None]:
    """
    Runs the agent in streaming mode.
    Yields events:
      - {"event": "text", "text": "..."}
      - {"event": "tool", "tool": "...", "args": {...}}
      - {"event": "done", "text": "...", "usage": {...}, "raw_metadata": {...}}
    """
    llm = get_llm(model_name)
    tools = await get_langchain_tools(mcp_client)
    
    agent = create_react_agent(llm, tools)
    messages = build_langchain_messages(system_prompt, history, user_message)
    
    accumulated_text = []
    tool_calls = []
    usage = {"prompt_tokens": 0, "completion_tokens": 0, "total_tokens": 0}
    
    # Use standard astream_events to intercept stream chunks
    # We require langchain-core >= 0.1.48 to use astream_events v2
    async for event in agent.astream_events({"messages": messages}, version="v2"):
        kind = event.get("event")
        name = event.get("name")
        
        # Capture streaming text chunks from the final chat model invocation
        if kind == "on_chat_model_stream":
            chunk = event.get("data", {}).get("chunk")
            if chunk and hasattr(chunk, "content") and chunk.content:
                accumulated_text.append(chunk.content)
                yield {"event": "text", "text": chunk.content}
                
        # Capture tool starts
        elif kind == "on_tool_start":
            tool_name = name
            tool_input = event.get("data", {}).get("input", {})
            tool_calls.append({"name": tool_name, "args": tool_input})
            yield {"event": "tool", "tool": tool_name, "args": tool_input}
            
        # Capture token usage from the chat model end events
        elif kind == "on_chat_model_end":
            output = event.get("data", {}).get("output")
            if output and hasattr(output, "response_metadata") and output.response_metadata:
                token_usage = output.response_metadata.get("token_usage", {})
                if token_usage:
                    usage["prompt_tokens"] += token_usage.get("prompt_tokens", 0)
                    usage["completion_tokens"] += token_usage.get("completion_tokens", 0)
                    usage["total_tokens"] += token_usage.get("total_tokens", 0)
                    
    final_text = "".join(accumulated_text)
    raw_metadata = {
        "tool_calls": tool_calls,
        "provider": os.getenv("LLM_PROVIDER", "openrouter"),
        "model": model_name
    }
    
    yield {
        "event": "done",
        "text": final_text,
        "usage": usage,
        "raw_metadata": raw_metadata
    }
