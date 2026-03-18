# 📋 Release Notes - v1.0.0 (Refactored)

This release marks a major architectural milestone, refactoring the Vector MCP Go server into a modular, production-ready codebase.

## 🌟 Major Changes

### 🔧 Core Refactoring
- **Modular Internal Packages**: Moved monolithic logic into dedicated packages:
    - `internal/onnx`: Robust runtime initialization.
    - `internal/indexer`: Optimized file scanning and semantic chunking.
    - `internal/worker`: Scalable background task management.
    - `internal/watcher`: Responsive file system monitoring.
    - `internal/mcp`: Cleanly defined MCP server and tool set.
- **Thin Entry Point**: `main.go` is now a minimal layer for dependency orchestration.

### 🔌 Connectivity & Tools
- **New Tool**: `set_project_root` added to allow dynamic codebase switching without restarting the server.
- **Enhanced Descriptions**: All MCP tools now include detailed usage guidance for AI agents.
- **Connectivity**: Improved ping and health check mechanisms.

### 📚 Documentation & Integration
- **Updated README**: Clearer setup instructions and architectural overviews.
- **Agent Skill**: Introduced `skills/vector-context/SKILL.md` to provide AI agents with clear usage patterns and best practices.
- **Example Config**: Added `mcp-config.json.example` for easier integration with Gemini CLI and Antigravity.
- **GoDoc Coverage**: Added comprehensive documentation comments to all exported package members.

## 🚀 Enhancements
- Robust error handling for ONNX library discovery.
- Improved concurrency safety with proper mutex management across the watcher and worker.
- Debounced indexing triggers to optimize system resources during active development.
