# 📋 Release Notes - v1.1.0

This release introduces significant architectural improvements, enhanced connectivity options, and critical stability fixes for the Vector MCP Go server.

## 🌟 Major Features

### 📡 Hybrid HTTP/SSE Transport

- **Streamable-HTTP Implementation**: Replaced process-based `stdio` with a spec-compliant HTTP transport.
- **SSE Support**: Reliable, URL-based connections for modern AI agents.
- **CORS Compatibility**: Full support for browser-based clients with exposed `Mcp-Session-Id` headers.

### 🛡️ Production Daemon Mode

- **Master/Slave Architecture**: Optimized multi-instance handling with a central daemon.
- **Systemd Integration**: Official support for running as a managed background service.
- **Automatic Indexing**: Improved background worker orchestration for zero-downtime indexing.

## 🔧 Critical Fixes

### 🧠 ONNX Model Compatibility

- **Fixed Missing Inputs**: Added the mandatory `token_type_ids` input to the embedding session.
- **Resolved Runtime Crashes**: Fixed "Missing Input: token_type_ids" errors that previously caused indexing failures in certain transformer models.

### 📄 Tool Schema Validation

- **JSON Schema Compliance**: Added required `items` fields to all array-type tool parameters.
- **Gemini Integration**: Resolved "missing field" validation errors during tool discovery.

## 🚀 Enhancements

- Updated health checks to return precise version tracking.
- Improved error logging and audit trails in the API layer.
- Optimized file watcher to handle rapid monorepo changes more efficiently.
