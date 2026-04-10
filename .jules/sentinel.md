## 2025-04-10 - Missing Path Validations in MCP Handlers
**Vulnerability:** Path Traversal vulnerabilities in `handlers_safety.go` and `handlers_analysis_extended.go` where user-provided paths were read/used directly without validation.
**Learning:** The handlers mapped to MCP tools (`mcp.CallToolRequest`) extract paths using `request.GetString("path", "")` and often pass them directly to core modules (like LSP providers or file readers).
**Prevention:** ALWAYS apply `s.validatePath(path)` to any user-provided path parameter inside the MCP handler layer before using it or passing it to underlying services.
