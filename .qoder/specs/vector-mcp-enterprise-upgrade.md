# Vector-MCP-Go Enterprise Upgrade Plan

## Context

**Project:** vector-mcp-go - A deterministic MCP server providing semantic search, code analysis, and mutation capabilities for LLM-assisted development.

**Current State:**
- ~10,938 lines of Go code
- 5 unified MCP tools (search_workspace, lsp_query, analyze_code, workspace_manager, modify_workspace)
- ONNX embeddings (BGE-M3, BGE-Small) with Chromem-Go vector database
- Master-slave RPC architecture for distributed operation
- Tree-sitter AST chunking for 10 languages
- Hybrid search with RRF fusion
- Test coverage: 54.6% average, but 0% on critical components

**User Goals:**
- Upgrade from hobby project to enterprise-grade system
- All areas equally prioritized (security, testing, observability)
- Support medium scale workloads (10k-100k files)
- Explore code-specific models, multi-model support, advanced reranking

**Critical Issues Identified:**
1. Path traversal vulnerability in mutation handlers (CRITICAL)
2. Zero test coverage on LSP, daemon, embedding, api, mutation, watcher packages
3. Silent error suppression in configuration
4. Missing observability (metrics, tracing, performance logging)
5. O(n) lexical search scalability issue
6. No dimension migration support (model switching requires DB deletion)

---

## Phase 1: Security Hardening

### 1.1 Path Traversal Prevention (CRITICAL)

**Problem:** `internal/mcp/handlers_mutation.go` lacks path sanitization - `../../../etc/passwd` could escape project root.

**Files to modify:**
- `internal/mcp/handlers_mutation.go` - Add path validation to all handlers

**New package:** `internal/security/pathguard/sanitizer.go`
```go
func ValidatePath(basePath, targetPath string) (string, error)
func SanitizeFilename(name string) string
func IsAllowedExtension(path string, allowed []string) bool
```

**Implementation:**
1. Add `ValidatePath()` wrapper around all file operations
2. Use `filepath.EvalSymlinks()` to resolve symlinks
3. Check resolved path starts with project root
4. Add maximum path depth limit

### 1.2 Input Validation & Rate Limiting

**Problem:** No rate limiting on API; regex searches accept unbounded patterns (ReDoS risk).

**Files to modify:**
- `internal/api/server.go` - Add rate limiting middleware
- `internal/mcp/handlers_search.go` - Add regex complexity validation

**New packages:**
- `internal/security/ratelimit/tokenbucket.go`
- `internal/security/input/validation.go`

**Implementation:**
1. Token bucket rate limiter per-client-IP
2. Regex complexity scoring (reject patterns with high backtracking)
3. Maximum pattern length enforcement
4. Request size limits on API endpoints

### 1.3 Authentication Framework

**New package:** `internal/auth/middleware.go`

**Files to modify:**
- `internal/config/config.go` - Add `APIKeys`, `RequireAuth` config
- `internal/api/server.go` - Add auth middleware

---

## Phase 2: Comprehensive Test Coverage

### 2.1 Testing Infrastructure

**New package:** `internal/testutil/`
- `mocks/store.go` - MockStore implementing IndexerStore
- `mocks/embedder.go` - MockEmbedder
- `mocks/lsp.go` - MockLSPManager
- `fixtures/project.go` - Temporary project structures

### 2.2 Zero-Coverage Package Tests

**New test files required:**

| Package | Test File | Key Test Cases |
|---------|-----------|----------------|
| `internal/lsp` | `client_test.go` | Process lifecycle, message framing, reconnection, timeout |
| `internal/daemon` | `daemon_test.go` | Master election, RPC serialization, remote store |
| `internal/embedding` | `session_test.go` | ONNX lifecycle, batch processing, error recovery |
| `internal/api` | `server_test.go` | HTTP handlers, CORS, auth middleware |
| `internal/mutation` | `safety_test.go` | Patch verification, diagnostic parsing |
| `internal/watcher` | `watcher_test.go` | Event debouncing, recursive watching |

### 2.3 Coverage Targets

- Core packages (db, indexer, mcp): 80%+
- Security packages: 90%+
- Overall: 75%+

**Build Fix:** Move `scripts/*.go` to `cmd/` subdirectories to resolve multiple main redeclarations.

---

## Phase 3: Observability & Monitoring

### 3.1 Metrics Infrastructure

**New package:** `internal/observability/metrics/prometheus.go`

**Metrics to track:**
- `vector_mcp_search_duration_seconds` (histogram by action)
- `vector_mcp_index_files_total` (counter)
- `vector_mcp_embedding_pool_available` (gauge)
- `vector_mcp_db_records_total` (gauge)
- `vector_mcp_errors_total` (counter by type)

**Files to modify:**
- `internal/api/server.go` - Add `/metrics` endpoint
- `internal/mcp/server.go` - Instrument tool handlers

### 3.2 Enhanced Health Checks

**Files to modify:** `internal/api/server.go`

Add structured health response:
```json
{
  "status": "ok|degraded|unhealthy",
  "checks": {
    "database": {"status": "ok", "latency_ms": 5},
    "embedding_pool": {"status": "ok", "available": 3, "total": 4},
    "lsp_sessions": {"status": "ok", "active": 2},
    "memory": {"status": "ok", "used_percent": 65}
  }
}
```

### 3.3 Structured Logging Enhancement

**Files to modify:**
- `internal/config/config.go` - Add `LogLevel`, `LogFormat` config
- All handler files - Add request-scoped trace IDs

---

## Phase 4: Resource Management & Scalability

### 4.1 Lexical Search Optimization

**Problem:** `LexicalSearch()` in `internal/db/store.go:85-119` fetches ALL records then filters (O(n)).

**New package:** `internal/db/lexical/index.go`

**Implementation:**
1. Add inverted index for symbol names
2. Bleve or Tantivy integration for full-text search
3. Incremental index updates on file changes

### 4.2 Query Result Caching

**New package:** `internal/cache/lru.go`

**Files to modify:**
- `internal/mcp/handlers_search.go` - Check cache before embedding

**Implementation:**
- LRU cache for query → embedding mappings
- Search result cache with TTL
- Cache invalidation on file changes

### 4.3 Dimension Migration Support

**New package:** `internal/db/migration/migrate.go`

**Implementation:**
- Detect dimension change on startup
- Export/import utilities for data migration
- Dual-write support during model transition

---

## Phase 5: Embedding Model Enhancement

### 5.1 Code-Specific Model Support

**Files to modify:**
- `internal/embedding/downloader.go` - Add model configs

**New models to support:**
| Model | Dimension | Use Case |
|-------|-----------|----------|
| `microsoft/codebert-base` | 768 | Code understanding |
| `nomic-ai/nomic-embed-text-v1.5` | 768 | Long context with matryoshka |
| `codellama/CodeLlama-7b-hf` | 4096 | Large context code |

**New package:** `internal/embedding/adapter/`
- Model-specific preprocessing
- Mean pooling vs CLS pooling configuration

### 5.2 Multi-Model Support

**Files to modify:**
- `internal/embedding/session.go` - Support concurrent models
- `internal/config/config.go` - Add multi-model config

**Implementation:**
```go
type EmbeddingConfig struct {
    PrimaryModel   string
    SecondaryModel string  // Optional fallback
    CodeModel      string  // Optional code-specific
    RerankerModel  string
}
```

### 5.3 Advanced Reranking

**Enhancement:** Add diversity-aware reranking to avoid similar results.

---

## Implementation Order

```
Week 1-2:  Security Hardening (Phase 1.1, 1.2)
Week 3-4:  Testing Infrastructure + Core Tests (Phase 2.1, 2.2)
Week 5-6:  Observability (Phase 3.1, 3.2, 3.3)
Week 7-8:  Resource Management (Phase 4.1, 4.2, 4.3)
Week 9-10: Model Enhancement (Phase 5.1, 5.2, 5.3)
Week 11:   Authentication (Phase 1.3) + Final Testing
Week 12:   Documentation + Performance Validation
```

---

## New Package Structure

```
internal/
├── auth/                    # Authentication
│   ├── apikeys.go
│   └── middleware.go
├── cache/                   # Query caching
│   └── lru.go
├── db/
│   ├── migration/           # Dimension migration
│   └── lexical/             # Lexical index
├── embedding/
│   └── adapter/             # Model adapters
├── observability/           # Monitoring
│   ├── metrics/
│   └── tracing/
├── security/                # Security utilities
│   ├── input/
│   ├── pathguard/
│   └── ratelimit/
└── testutil/                # Testing utilities
    └── mocks/
```

---

## Configuration Schema Extension

Add to `internal/config/config.go`:

```go
type Config struct {
    // ... existing fields ...

    // Security
    AllowedWritePaths    []string
    MaxPathDepth         int
    DenySymlinks         bool
    APIKeys              []string
    RequireAuth          bool
    RateLimitRPS         int
    MaxRegexComplexity   int

    // Observability
    LogLevel             string
    MetricsEnabled       bool
    MetricsPort          string

    // Caching
    EmbeddingCacheSize   int
    SearchResultCacheTTL int

    // Multi-model
    CodeModel            string
    SecondaryModel       string
}
```

---

## Verification

### Security Verification
```bash
# Path traversal tests
go test ./internal/security/... -run TestPathTraversal

# Rate limiting
go test ./internal/api/... -run TestRateLimit

# Build verification
go build ./...
```

### Test Coverage Verification
```bash
go test -coverprofile=coverage.out ./...
go tool cover -func=coverage.out | grep total
# Target: >= 75%
```

### Performance Verification
```bash
# Benchmark at scale
go test -bench=BenchmarkLexicalSearch -benchtime=10s

# 100k files should index in < 30 minutes
# Lexical search should be < 100ms at 100k records
```

### Observability Verification
```bash
curl http://localhost:47821/metrics  # Prometheus metrics
curl http://localhost:47821/api/health  # Health check
```

---

## Critical Files Summary

| File | Changes | Priority |
|------|---------|----------|
| `internal/mcp/handlers_mutation.go` | Path validation, security hardening | CRITICAL |
| `internal/api/server.go` | Rate limiting, metrics, auth, health checks | HIGH |
| `internal/db/store.go` | Lexical search optimization, caching hooks | HIGH |
| `internal/config/config.go` | Extended configuration schema | HIGH |
| `internal/embedding/session.go` | Multi-model support, pool management | MEDIUM |
| `main.go` | Graceful shutdown coordination | MEDIUM |

---

## Constraints (from AGENTS.md)

1. **No Generative LLMs** - All embedding models must be deterministic ONNX models
2. **Fat Tool Pattern** - New features as `action` enums in existing tools, not new tools
3. **Rune-Safe Truncation** - Always convert to `[]rune` before truncating strings
4. **Parameter Sanitization** - Clamp numerical limits before array slicing
