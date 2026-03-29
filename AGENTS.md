# Agent Guidelines for `vector-mcp-go`

If you are an AI assistant or agent modifying this repository, you **must** strictly adhere to the following rules:

1. **No Generative LLMs:** Do not add integrations for Ollama, Gemini, OpenAI, Anthropic, or any other generative LLM. This server must remain strictly deterministic. The client LLM should handle all generative reasoning.
2. **Fat Tool Pattern:** New features must be integrated as `action` enums into the existing unified tools (`search_workspace`, `lsp_query`, `analyze_code`, `workspace_manager`, `modify_workspace`), rather than creating new standalone tools. Minimize the tool registration surface area.
3. **Rune-Safe Truncation:** When truncating strings to prevent character overflow, always convert the string to a `[]rune` slice first to prevent multi-byte UTF-8 corruption panics.
   ```go
   contentStr := originalString
   r := []rune(contentStr)
   if len(r) > 10000 {
       contentStr = string(r[:10000]) + "\n... [Truncated for length]"
   }
   ```
4. **Parameter Sanitization:** Always validate and clamp numerical limits (like `topK` or `limit`) from MCP requests before using them in array slicing operations to prevent out-of-bounds panics.
