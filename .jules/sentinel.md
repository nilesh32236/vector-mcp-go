## 2025-05-18 - Overly Permissive CORS Configuration
**Vulnerability:** Found `Access-Control-Allow-Origin: *` hardcoded in multiple places for HTTP servers and MCP stream transports.
**Learning:** Hardcoded wildcard CORS enables CSRF and cross-origin data leakage, which is especially critical for a local agentic API server interfacing with sensitive codebase data.
**Prevention:** Avoid hardcoding `*` for CORS. Instead, configure allowed origins dynamically using an environment variable (like `ALLOWED_ORIGINS`) and parse them securely, defaulting to restricted access.

## 2026-04-06 - Path Traversal in MCP Handlers
**Vulnerability:** Path Traversal vulnerabilities in `handleCheckDependencyHealth`, `handleGetCodeHistory`, and `handleGetCodebaseSkeleton` handlers where user input was manually joined with the project root or trusted if absolute.
**Learning:** `filepath.Join` or `filepath.IsAbs` is insufficient to prevent path traversal. The `s.pathValidator` (which uses `pathguard`) provides robust and centralized validation but was not applied consistently across all handlers, leading to security gaps in read-only and analytical endpoints.
**Prevention:** Always use `s.pathValidator.ValidatePath` (or equivalent robust path sanitization) for any file path derived from user input in MCP server handlers, regardless of whether the operation is read-only or mutative.
