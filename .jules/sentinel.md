## 2025-05-18 - Overly Permissive CORS Configuration
**Vulnerability:** Found `Access-Control-Allow-Origin: *` hardcoded in multiple places for HTTP servers and MCP stream transports.
**Learning:** Hardcoded wildcard CORS enables CSRF and cross-origin data leakage, which is especially critical for a local agentic API server interfacing with sensitive codebase data.
**Prevention:** Avoid hardcoding `*` for CORS. Instead, configure allowed origins dynamically using an environment variable (like `ALLOWED_ORIGINS`) and parse them securely, defaulting to restricted access.
