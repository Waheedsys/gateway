import os
import shlex
import logging
from typing import List, Dict, Any
from mcp import ClientSession, StdioServerParameters
from mcp.client.stdio import stdio_client

logger = logging.getLogger("gateway.mcp")

class MCPClient:
    def __init__(self):
        self.command = os.getenv("MCP_SERVER_COMMAND", "").strip()

    def is_enabled(self) -> bool:
        return bool(self.command)

    async def list_tools(self) -> List[Dict[str, Any]]:
        if not self.is_enabled():
            return []
        
        # Replace backslashes in command path if windows-style
        cmd_str = self.command
        args = shlex.split(cmd_str)
        if not args:
            return []
        
        server_params = StdioServerParameters(
            command=args[0],
            args=args[1:]
        )
        
        try:
            async with stdio_client(server_params) as (read, write):
                async with ClientSession(read, write) as session:
                    await session.initialize()
                    result = await session.list_tools()
                    tools = []
                    # result is ListToolsResult
                    for tool in getattr(result, 'tools', []):
                        tools.append({
                            "name": tool.name,
                            "description": tool.description or "",
                            "inputSchema": tool.inputSchema or {}
                        })
                    return tools
        except Exception as e:
            logger.error(f"Failed to list MCP tools: {e}", exc_info=True)
            raise

    async def call_tool(self, name: str, arguments: Dict[str, Any]) -> str:
        if not self.is_enabled():
            raise ValueError("MCP client is not enabled (MCP_SERVER_COMMAND is empty)")
        
        args = shlex.split(self.command)
        if not args:
            raise ValueError("MCP server command is empty")
            
        server_params = StdioServerParameters(
            command=args[0],
            args=args[1:]
        )
        
        try:
            async with stdio_client(server_params) as (read, write):
                async with ClientSession(read, write) as session:
                    await session.initialize()
                    result = await session.call_tool(name, arguments)
                    
                    content_list = getattr(result, 'content', [])
                    texts = []
                    is_error = getattr(result, 'isError', False)
                    for item in content_list:
                        text = getattr(item, 'text', '')
                        if text:
                            texts.append(text)
                    out = "\n".join(texts)
                    if is_error:
                        raise RuntimeError(out)
                    return out
        except Exception as e:
            logger.error(f"Failed to call MCP tool {name}: {e}", exc_info=True)
            raise
