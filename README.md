# vector-mcp-go

A high-performance, purely deterministic [Model Context Protocol (MCP)](https://modelcontextprotocol.io/) server written in Go. Provides semantic search, architectural analysis, code mutation, and LSP-powered intelligence directly to your LLM agent — with zero generative AI dependencies.

## Architecture

`vector-mcp-go` uses a **"Fat Tool" pattern**: five consolidated MCP tools replace dozens of fragmented ones, reducing LLM context exhaustion and improving tool-selection accuracy. All reasoning is delegated to the client LLM; the server operates 100% deterministically.

Local ONNX embeddings via `bge-m3` / `bge-small-en-v1.5` provide fast, privacy-preserving semantic understanding without external API calls.

### Design Principles

- **No Generative LLMs** — all embedding models are deterministic ONNX models
- **Fat Tool Pattern** — new features are added as `action` enums inside existing tools, not as new tools
- **Rune-Safe Truncation** — strings are always converted to `[]rune` before truncation to prevent UTF-8 panics
- **Parameter Sanitization** — all numerical limits from MCP requests are clamped before array operations

---

## Core MCP Tools

| Tool | Description |
|------|-------------|
| `search_workspace` | Unified search engine: `vector` (semantic), `regex` (exact), `graph` (code relationships), `index_status` |
| `lsp_query` | LSP integration: definitions, references, type hierarchies, diagnostics, blast-radius analysis |
| `analyze_code` | Codebase diagnostics: `ast_skeleton`, `dead_code`, `duplicate_code`, `dependencies` |
| `workspace_manager` | Project lifecycle: switch roots, trigger indexing, system diagnostics |
| `modify_workspace` | Safe file mutation: patch, create, lint, LSP-verified patch application |

---

## Features

### Semantic Search
- ONNX-based local embeddings (no API calls, no data leaving your machine)
- Hybrid search with Reciprocal Rank Fusion (RRF) combining vector + lexical results
- Tree-sitter AST chunking for 10 programming languages
- chromem-go / LanceDB vector store

### Multi-Model Embedding
- Primary model: `BAAI/bge-small-en-v1.5` (default) or `Xenova/bge-m3`
- Code-specific models: `microsoft/codebert-base`, `nomic-ai/nomic-embed-text-v1.5`, IBM `granite-embedding-english-r2`
- Cross-encoder reranking: `cross-encoder/ms-marco-MiniLM-L-6-v2`, `Xenova/bge-reranker-v2-m3`
- Model routing via `internal/embedding/router.go`

### Security
- **Path traversal prevention** — `internal/security/pathguard` validates all file paths against project root using `filepath.EvalSymlinks`
- **Rate limiting** — token bucket per-client-IP (`internal/security/ratelimit`), default 30 req/s burst 60
- **Input validation** — regex complexity scoring, pattern length limits, request size limits (`internal/security/input`)

### Observability
- Prometheus metrics endpoint at `/metrics` (`internal/observability/metrics`)
- Structured health checks at `/api/health` with per-component status (database, embedder, rate limiter)
- Readiness (`/api/ready`) and liveness (`/api/live`) probes
- JSON structured logging via `log/slog`

### Caching
- LRU cache for query→embedding mappings (`internal/cache/lru.go`)
- Configurable cache size and TTL

### Distributed Operation
- Master-slave RPC architecture for multi-instance deployments (`internal/daemon`)
- Unix socket IPC

### Language Server Protocol
- Deep LSP integration for Go, TypeScript, Python, Rust, Java, Ruby, C/C++
- Precise symbol definitions, references, rename, diagnostics

---

## Project Structure

```
.
├── main.go
├── cmd/                          # Entry points
├── scripts/                      # Shell utilities (model comparison, benchmarks)
├── internal/
│   ├── api/                      # HTTP REST server + MCP HTTP transport
│   │   ├── server.go
│   │   ├── server_test.go
│   │   └── handlers_tools.go
│   ├── cache/                    # LRU query/embedding cache
│   │   ├── lru.go
│   │   └── lru_test.go
│   ├── config/                   # Configuration loading (.env + env vars)
│   ├── daemon/                   # Master-slave RPC daemon
│   │   ├── daemon.go
│   │   └── daemon_test.go
│   ├── db/                       # Vector store (chromem-go/LanceDB) + graph
│   │   ├── store.go
│   │   └── graph.go
│   ├── embedding/                # ONNX embedding sessions + multi-model support
│   │   ├── session.go
│   │   ├── multimodel.go
│   │   ├── reranker.go
│   │   ├── router.go
│   │   └── downloader.go
│   ├── indexer/                  # File scanner + Tree-sitter AST chunker
│   │   ├── scanner.go
│   │   └── chunker.go
│   ├── lsp/                      # LSP client (process lifecycle, JSON-RPC framing)
│   │   ├── client.go
│   │   └── client_test.go
│   ├── mcp/                      # MCP tool handlers (fat tool pattern)
│   │   ├── server.go
│   │   ├── handlers_search.go
│   │   ├── handlers_analysis.go
│   │   ├── handlers_mutation.go
│   │   ├── handlers_lsp.go
│   │   ├── handlers_project.go
│   │   └── ...
│   ├── mutation/                 # Patch safety verification
│   │   ├── safety.go
│   │   └── safety_test.go
│   ├── observability/
│   │   └── metrics/              # Prometheus metrics
│   │       ├── prometheus.go
│   │       └── prometheus_test.go
│   ├── security/
│   │   ├── pathguard/            # Path traversal prevention
│   │   ├── ratelimit/            # Token bucket rate limiter
│   │   └── input/                # Input validation (regex complexity, size limits)
│   ├── testutil/
│   │   ├── mocks/                # MockStore, MockEmbedder, MockLSPManager
│   │   └── fixtures/             # Temporary project structures for tests
│   ├── watcher/                  # File system watcher with debouncing
│   │   ├── watcher.go
│   │   └── watcher_test.go
│   └── worker/                   # Worker pool
```

---

## Setup

### Prerequisites

- Go 1.22+
- C++ build tools (CGO required for ONNX runtime)
- `libonnxruntime` shared library

### Build

```bash
make build
# or
go build -o bin/server .
```

### Run

```bash
./bin/server
```

### Configuration

All configuration is via environment variables (or a `.env` file in the working directory):

| Variable | Default | Description |
|----------|---------|-------------|
| `PROJECT_ROOT` | `$CWD` | Root directory to index |
| `DATA_DIR` | `~/.local/share/vector-mcp-go` | Data storage directory |
| `DB_PATH` | `$DATA_DIR/lancedb` | Vector database path |
| `MODELS_DIR` | `$DATA_DIR/models` | ONNX model cache directory |
| `MODEL_NAME` | `BAAI/bge-small-en-v1.5` | Primary embedding model |
| `RERANKER_MODEL_NAME` | `cross-encoder/ms-marco-MiniLM-L-6-v2` | Reranker model (`none` to disable) |
| `EMBEDDER_POOL_SIZE` | `1` | Number of concurrent embedding sessions |
| `API_PORT` | `47821` | HTTP API port |
| `HF_TOKEN` | _(empty)_ | HuggingFace token for gated models |
| `DISABLE_FILE_WATCHER` | `false` | Disable automatic re-indexing on file changes |
| `ENABLE_LIVE_INDEXING` | `false` | Enable live incremental indexing |
| `LOG_PATH` | `$DATA_DIR/server.log` | Log file path |

---

## MCP Client Configuration

Add to your MCP client config (e.g., Claude Desktop, Cursor, Kiro):

```json
{
  "mcpServers": {
    "vector-mcp-go": {
      "command": "/path/to/bin/server",
      "env": {
        "PROJECT_ROOT": "/path/to/your/project"
      }
    }
  }
}
```

For HTTP transport (Streamable-HTTP):

```json
{
  "mcpServers": {
    "vector-mcp-go": {
      "url": "http://localhost:47821/sse"
    }
  }
}
```

---

## API Endpoints

| Method | Path | Description |
|--------|------|-------------|
| `GET` | `/api/health` | Structured health check (database, embedder, rate limiter) |
| `GET` | `/api/ready` | Readiness probe |
| `GET` | `/api/live` | Liveness probe |
| `GET` | `/metrics` | Prometheus metrics |
| `POST` | `/api/search` | Direct semantic search |
| `POST` | `/api/context` | Context retrieval |
| `GET` | `/api/tools/list` | List available MCP tools |
| `POST` | `/api/tools/call` | Call an MCP tool directly |
| `GET` | `/api/tools/status` | Index status |
| `POST` | `/api/tools/index` | Trigger re-indexing |
| `GET` | `/api/tools/skeleton` | AST skeleton of a file |
| `GET/POST/DELETE` | `/sse`, `/message` | MCP Streamable-HTTP transport |

---

## Agentic Memory

The server supports an agentic memory system allowing the LLM to persist structural decisions and context into the local vector store. Use the `workspace_manager` tool with the `store_memory` / `recall_memory` actions.

---

## Testing

```bash
# Run all tests
go test ./...

# With coverage
go test -coverprofile=coverage.out ./...
go tool cover -func=coverage.out | grep total

# Run security tests specifically
go test ./internal/security/... -v

# Benchmarks
go test -bench=BenchmarkLexicalSearch -benchtime=10s ./internal/db/...
```

### Test Coverage Targets

| Package | Target |
|---------|--------|
| `internal/security/...` | 90%+ |
| `internal/db/...`, `internal/indexer/...`, `internal/mcp/...` | 80%+ |
| Overall | 75%+ |

---

## Supported Languages (AST Chunking)

Go, TypeScript/JavaScript, Python, Rust, Java, C/C++, Ruby, Kotlin, Swift, PHP

---

## Roadmap

- [ ] `internal/auth/` — API key authentication middleware
- [ ] `internal/db/lexical/` — Inverted index for O(1) lexical search (currently O(n))
- [ ] `internal/db/migration/` — Dimension migration support for model switching without DB deletion
- [ ] Extended `Config` struct — security, observability, caching, and multi-model fields
- [ ] `LogLevel` / `LogFormat` config options
- [ ] Metrics endpoint wired into API server

---

## Contributing

Follow the constraints in [AGENTS.md](./AGENTS.md):

1. No generative LLM integrations — ONNX-only, deterministic
2. New features go into existing tool `action` enums, not new tools
3. Always use `[]rune` conversion before string truncation
4. Clamp all numerical limits before array slicing
