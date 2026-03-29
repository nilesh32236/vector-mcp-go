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
- **Architectural Insights**:
  - **Dead Code Detection**: Identifies unused exported functions and classes.
  - **Dependency Health**: Flags missing external packages in `package.json`.
  - **Visual Mapping**: Generates Mermaid.js diagrams of project architecture.
- **Multi-Instance Optimization**: Intelligent Master/Slave architecture ensures only one instance loads the heavy embedding models and runs the file watcher, drastically reducing RAM usage (~600MB → ~20MB for slave instances).
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

---

## 🏗️ Setup & Installation

### Prerequisites

1. **Go 1.21+**
2. **ONNX Shared Library**: `libonnxruntime.so` (Linux).

### Installation

```bash
git clone https://github.com/nilesh32236/vector-mcp-go.git
cd vector-mcp-go
make build
```

---

## 🔌 MCP Protocol Features

### 💎 Resources
Resources provide structured data and status information to the client.

- **`index://status`**: Real-time indexing progress, record counts, and master/slave status.
- **`config://project`**: Current server configuration, active project root, and model settings.
- **`docs://guide`**: An interactive guide for using the vector-mcp-go server effectively.

### 📝 Prompts
Pre-defined prompt templates to streamline common AI workflows.

- **`generate-docstring`**: Context-aware prompt for writing high-quality documentation.
- **`analyze-architecture`**: High-level architectural analysis and summary prompt.

### 🛠️ Tools

| Tool                      | Description                                                                                                       |
| :------------------------ | :---------------------------------------------------------------------------------------------------------------- |
| `search_codebase`         | **Primary tool**. Semantic & lexical search with reranking.                                                       |
| `get_codebase_skeleton`   | Efficient topological tree view of the project (optimized for large codebases).                                   |
| `handle_filesystem_grep`  | **High-performance concurrent grep** (regex & keyword).                                                           |
| `get_related_context`     | Retrieve imports and dependencies for a specific file.                                                             |
| `trigger_project_index`   | Manually restart indexing for any directory.                                                                     |
| `set_project_root`        | Switch the active project root on the fly.                                                                        |
| `check_dependency_health` | Analyzes dependency manifests against actual imports.                                                              |
| `find_dead_code`          | Locates unused exported symbols.                                                                                  |

---

## ⚙️ Development

A `Makefile` is provided for common development tasks:

```bash
make build    # Build binary with version metadata
make test     # Run the test suite
make run      # Build and execute locally
make version  # Show version/build information
```

---

## ⚙️ Configuration

The following environment variables can be used to customize the server:

| Variable               | Description                                          | Default                        |
| :--------------------- | :--------------------------------------------------- | :----------------------------- |
| `PROJECT_ROOT`         | Absolute path to the project to index.               | Current directory              |
| `DATA_DIR`             | Base directory for DB and models.                    | `~/.local/share/vector-mcp-go` |
| `DISABLE_FILE_WATCHER` | Set to `true` to disable the real-time file watcher. | `false`                        |
| `MODEL_NAME`           | ONNX-compatible model to use for embeddings.         | `Xenova/bge-m3`                |

---

## ⚖️ License

MIT
