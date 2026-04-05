# Vector MCP Go

A high-performance, purely deterministic Model Context Protocol (MCP) server written in Go. This server provides advanced semantic search, architectural analysis, and codebase mutation capabilities directly to your LLM agent.

## Architecture

`vector-mcp-go` is designed with a **"Fat Tool" pattern**, reducing the fragmented surface area of tools to minimize LLM context exhaustion and improve tool-selection accuracy. The server relies exclusively on the client LLM for generative reasoning, operating 100% deterministically.

It leverages **local ONNX embeddings** via `bge-m3` to provide extremely fast, privacy-preserving semantic understanding without external API calls.

## Core Fat Tools

The server exports five consolidated tools:

1. **`search_workspace`**: A unified search engine routing across semantic (`vector`), exact match (`regex`), code relationship (`graph`), and indexing states (`index_status`).
2. **`lsp_query`**: Deep Language Server Protocol integration providing precise absolute references, type hierarchies, definitions, and impact blast-radius analysis.
3. **`analyze_code`**: Fast codebase diagnostics routing to AST parsing (`ast_skeleton`), dead code checks (`dead_code`), duplication checks (`duplicate_code`), and manifest validation (`dependencies`).
4. **`workspace_manager`**: Project lifecycle commands allowing the agent to switch roots, trigger local indexing, and fetch deep system diagnostic reports.
5. **`modify_workspace`**: Safe file mutation commands offering code patching, file creation, linting, and LSP-driven patch verification to ensure safe iterative development.

## Setup & Execution

### Prerequisites
- Go 1.22 or higher
- C++ build tools (for CGO ONNX runtime integration)

### Build
```bash
make build
```

### Run
```bash
./bin/server
```

## Agentic Memory

The server also supports an Agentic Memory system allowing the LLM to store structural decisions and context directly into the local `chromem-go` LanceDB instance.
