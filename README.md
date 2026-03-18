# 🛰️ Vector MCP Go

A modular, high-performance Model Context Protocol (MCP) server for **local semantic search** and **project context management**. It leverages local embeddings (ONNX) and vector storage to provide AI agents with a "global brain" of your codebase while keeping all source code on your machine.

---

## 🚀 Key Features

- **Local Embeddings**: Powered by `bge-m3` via ONNX Runtime for high-quality semantic understanding without external API calls.
- **Modular Architecture**: Refactored for scalability with dedicated packages for indexing, background tasks, and file monitoring.
- **Dynamic Project Switching**: Switch active project roots on the fly using the `set_project_root` tool.
- **Deep Code Analysis**:
    - **Symbol Extraction**: Automatically identifies Go/TS/JS symbols for scoped retrieval.
    - **Relationship Mapping**: Traces imports and dependencies to provide holistic context.
    - **Semantic Chunking**: Intelligent overlap-based chunking preserves meaning across boundaries.
- **Real-time Indexing**: Built-in debounced file watcher synchronizes the vector index as you save.
- **Safety & Performance**: Non-blocking background workers and embedder resource pooling ensure server stability.

---

## 🏗️ Architecture Overview

The project is organized into modular internal packages for maintainability:

- **`internal/onnx`**: Handles ONNX runtime initialization and shared library discovery.
- **`internal/indexer`**: Core logic for file scanning, hashing, and semantic chunking.
- **`internal/worker`**: Manages background indexing tasks via a priority queue.
- **`internal/watcher`**: Monitors file system events with debounced trigger logic.
- **`internal/mcp`**: Defines the MCP server and tool set.
- **`internal/db`**: Vector storage abstraction (Connect, Insert, Search).
- **`internal/config`**: Configuration management and path resolution.

---

## 🛠️ Setup

### Prerequisites
1. **Go 1.21+**
2. **ONNX Shared Library**: `libonnxruntime.so` (Linux). The server attempts to discover this in several standard locations.

### Installation
```bash
# Build the binary
go build -o vector-mcp-go main.go

# Initialize (downloads necessary models on first run)
./vector-mcp-go status
```

### Environment Configuration
Optionally set `ONNX_LIB_PATH` if the library is in a custom location:
```bash
export ONNX_LIB_PATH="/custom/path/to/libonnxruntime.so"
```

---

## 🔌 MCP Tools

| Tool | Description |
| :--- | :--- |
| `ping` | Check server connectivity. |
| `set_project_root` | Dynamically switch the active project root and reset the file watcher. |
| `trigger_project_index` | Manually trigger a full background index of a project path. |
| `get_related_context` | Retrieve code chunks and dependencies for a specific file. |
| `store_context` | Save architectural decisions, rules, or shared context globally. |
| `find_duplicate_code` | Scan for logic duplication across namespaces. |
| `get_codebase_skeleton` | View a topological tree of the project structure. |
| `index_status` | Monitor indexing progress and database health. |
| `delete_context` | Remove specific files or wipe entire project indices. |

---

## ⚖️ License
MIT
