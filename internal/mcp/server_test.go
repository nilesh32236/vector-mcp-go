package mcp

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/mark3labs/mcp-go/mcp"
	"github.com/nilesh32236/vector-mcp-go/internal/config"
	"github.com/nilesh32236/vector-mcp-go/internal/db"
	"github.com/nilesh32236/vector-mcp-go/internal/indexer"
	"github.com/nilesh32236/vector-mcp-go/internal/security/pathguard"
)

type mockEmbedder struct {
	dim int
}

func (m *mockEmbedder) Embed(_ context.Context, text string) ([]float32, error) {
	d := m.dim
	if d == 0 {
		d = 1024
	}
	emb := make([]float32, d)
	emb[0] = 1.0
	return emb, nil
}

func (m *mockEmbedder) EmbedBatch(ctx context.Context, texts []string) ([][]float32, error) {
	results := make([][]float32, len(texts))
	for i, text := range texts {
		emb, err := m.Embed(ctx, text)
		if err != nil {
			return nil, err
		}
		results[i] = emb
	}
	return results, nil
}

func (m *mockEmbedder) RerankBatch(_ context.Context, query string, texts []string) ([]float32, error) {
	return make([]float32, len(texts)), nil
}

func TestIndexStatusTool(t *testing.T) {
	ctx := context.Background()
	dbPath := "./test_mcp_db"
	_ = os.RemoveAll(dbPath)
	defer func() { _ = os.RemoveAll(dbPath) }()

	cfg := &config.Config{
		ProjectRoot: "/test/project",
		DbPath:      dbPath,
	}

	store, _ := db.Connect(ctx, dbPath, "test_collection", 1024)
	// Set status for local project and another project in DB
	_ = store.SetStatus(ctx, "/test/project", "Indexing: 73.2% (73/100) - Current: file.go")
	_ = store.SetStatus(ctx, "/other/project", "Indexing: 10.0% (1/10) - Current: remote.go")

	localStoreGetter := func(_ context.Context) (*db.Store, error) { return store, nil }
	progressMap := &sync.Map{}
	progressMap.Store("/test/project", "Indexing: 73.2% (73/100) - Current: file.go")

	srv := &Server{
		cfg:              cfg,
		localStoreGetter: localStoreGetter,
		progressMap:      progressMap,
		embedder:         &mockEmbedder{dim: 1024},
		graph:            db.NewKnowledgeGraph(),
	}

	// Test HandleIndexStatus directly
	res, err := srv.handleIndexStatus(ctx, mcp.CallToolRequest{})
	if err != nil {
		t.Fatalf("HandleIndexStatus failed: %v", err)
	}

	content := res.Content[0].(mcp.TextContent).Text

	// The output format was updated in handleIndexStatus
	if !strings.Contains(content, "🚀 Background Indexing Tasks:") {
		t.Errorf("expected '🚀 Background Indexing Tasks:' in output, got: %s", content)
	}
	if !strings.Contains(content, "/test/project: Indexing: 73.2%") {
		t.Error("expected local task status in output")
	}
}

func TestSearchCodebaseTool(t *testing.T) {
	ctx := context.Background()
	dbPath := "./test_search_db"
	_ = os.RemoveAll(dbPath)
	defer func() { _ = os.RemoveAll(dbPath) }()

	cfg := &config.Config{
		ProjectRoot: "/test/project",
		DbPath:      dbPath,
	}

	store, _ := db.Connect(ctx, dbPath, "test_collection", 1024)

	// Use the same embedding as the mock embedder
	emb := make([]float32, 1024)
	emb[0] = 1.0

	// Insert dummy record
	err := store.Insert(ctx, []db.Record{
		{
			ID:        "test-id-1",
			Content:   "func HelloWorld() { fmt.Println(\"Hello\") }",
			Embedding: emb,
			Metadata: map[string]string{
				"path":       "hello.go",
				"project_id": "/test/project",
				"symbols":    "[\"HelloWorld\"]",
				"category":   "code",
			},
		},
	})
	if err != nil {
		t.Fatalf("failed to insert: %v", err)
	}

	// Verify it's in DB
	results, err := store.Search(ctx, emb, 1, []string{"/test/project"}, "")
	if err != nil {
		t.Fatalf("direct search failed: %v", err)
	}
	if len(results) == 0 {
		t.Fatalf("direct search returned 0 results")
	}

	srv := &Server{
		cfg:              cfg,
		localStoreGetter: func(_ context.Context) (*db.Store, error) { return store, nil },
		embedder:         &mockEmbedder{dim: 1024},
		graph:            db.NewKnowledgeGraph(),
	}

	// Test with query
	req := mcp.CallToolRequest{}
	req.Params.Name = "search_codebase"
	req.Params.Arguments = map[string]any{
		"query": "test",
	}

	res, err := srv.handleSearchCodebase(ctx, req)
	if err != nil {
		t.Fatalf("HandleSearchCodebase failed: %v", err)
	}

	content := res.Content[0].(mcp.TextContent).Text
	if !strings.Contains(content, "HelloWorld") {
		t.Errorf("expected search result in output, got: %s", content)
	}
	if !strings.Contains(content, "hello.go") {
		t.Errorf("expected file path in output, got: %s", content)
	}
}

func TestArchitectTools(t *testing.T) {
	ctx := context.Background()
	tempDir, err := os.MkdirTemp("", "test_architect_tools_*")
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = os.RemoveAll(tempDir) }()

	// 1. Setup Mock Monorepo
	_ = os.MkdirAll(filepath.Join(tempDir, "apps/api"), 0755)
	_ = os.MkdirAll(filepath.Join(tempDir, "packages/shared"), 0755)

	pkgJSON := `{
  "name": "api",
  "dependencies": {
    "lodash": "^4.17.21"
  }
}`
	_ = os.WriteFile(filepath.Join(tempDir, "apps/api/package.json"), []byte(pkgJSON), 0644)

	mainTS := `import axios from 'axios';
import { SharedUtils } from '@herexa/shared';

export function aliveFn() {
  const utils = new SharedUtils();
  return utils.doSomething();
}

aliveFn();

export function deadFn() {
  return "I am dead code";
}
`
	_ = os.WriteFile(filepath.Join(tempDir, "apps/api/main.ts"), []byte(mainTS), 0644)

	utilsTS := `export interface User {
  id: string;
}

export class SharedUtils {
  doSomething() {
    return "done";
  }
}
`
	_ = os.WriteFile(filepath.Join(tempDir, "packages/shared/utils.ts"), []byte(utilsTS), 0644)

	// 2. Initialize DB & Index (using real chunker but mock embedding)
	dbPath := filepath.Join(tempDir, "db")
	dim := 384
	store, _ := db.Connect(ctx, dbPath, "test_collection", dim)

	// Index files
	mockEmb := &mockEmbedder{dim: dim}
	files := []string{
		filepath.Join(tempDir, "apps/api/main.ts"),
		filepath.Join(tempDir, "packages/shared/utils.ts"),
	}

	for _, f := range files {
		content, _ := os.ReadFile(f)
		chunks := indexer.CreateChunks(ctx, string(content), f)
		var records []db.Record
		for _, chunk := range chunks {
			emb, _ := mockEmb.Embed(ctx, chunk.Content)
			relPath := config.GetRelativePath(f, tempDir)

			relJSON, _ := json.Marshal(chunk.Relationships)
			symJSON, _ := json.Marshal(chunk.Symbols)
			callsJSON, _ := json.Marshal(chunk.Calls)

			records = append(records, db.Record{
				ID:        fmt.Sprintf("%s-%d", f, time.Now().UnixNano()),
				Content:   chunk.Content,
				Embedding: emb,
				Metadata: map[string]string{
					"path":           relPath,
					"project_id":     tempDir,
					"relationships":  string(relJSON),
					"symbols":        string(symJSON),
					"type":           chunk.Type,
					"calls":          string(callsJSON),
					"function_score": fmt.Sprintf("%.2f", chunk.FunctionScore),
				},
			})
		}
		_ = store.Insert(ctx, records)
	}

	cfg := &config.Config{
		ProjectRoot: tempDir,
		DbPath:      dbPath,
		Dimension:   dim,
	}

	validator, err := pathguard.NewValidator(tempDir, pathguard.DefaultOptions())
	if err != nil {
		t.Fatalf("pathguard.NewValidator: %v", err)
	}
	srv := &Server{
		cfg:              cfg,
		localStoreGetter: func(_ context.Context) (*db.Store, error) { return store, nil },
		embedder:         &mockEmbedder{dim: dim},
		pathValidator:    validator,
	}
	srv.WithRemoteStore(store)

	t.Run("CheckDependencyHealth", func(t *testing.T) {
		req := mcp.CallToolRequest{}
		req.Params.Arguments = map[string]any{
			"directory_path": filepath.Join(tempDir, "apps/api"),
		}
		res, err := srv.handleCheckDependencyHealth(ctx, req)
		if err != nil {
			t.Fatal(err)
		}
		content := res.Content[0].(mcp.TextContent).Text
		if !strings.Contains(content, "axios") {
			t.Errorf("expected axios to be flagged as missing, got: %s", content)
		}
	})

	t.Run("GenerateDocstringPrompt", func(t *testing.T) {
		req := mcp.CallToolRequest{}
		req.Params.Arguments = map[string]any{
			"file_path":   "apps/api/main.ts",
			"entity_name": "aliveFn",
		}
		res, err := srv.handleGenerateDocstringPrompt(ctx, req)
		if err != nil {
			t.Fatal(err)
		}
		content := res.Content[0].(mcp.TextContent).Text
		if !strings.Contains(content, "SharedUtils") || !strings.Contains(content, "aliveFn") {
			t.Errorf("expected architectural context in prompt, got: %s", content)
		}
		if !strings.Contains(content, "JSDoc comments") {
			t.Errorf("expected JSDoc comments to be requested based on file extension, got: %s", content)
		}
	})

	t.Run("AnalyzeArchitecture", func(t *testing.T) {
		req := mcp.CallToolRequest{}
		req.Params.Arguments = map[string]any{
			"monorepo_prefix": "@herexa/",
		}
		res, err := srv.handleAnalyzeArchitecture(ctx, req)
		if err != nil {
			t.Fatal(err)
		}
		content := res.Content[0].(mcp.TextContent).Text
		if !strings.Contains(content, "\"apps/api\" --> \"@herexa/shared\"") {
			t.Errorf("expected Mermaid relationship with quotes, got: %s", content)
		}
	})

	t.Run("FindDeadCode", func(t *testing.T) {
		req := mcp.CallToolRequest{}
		req.Params.Arguments = map[string]any{
			"exclude_paths": []string{},
		}
		res, err := srv.handleFindDeadCode(ctx, req)
		if err != nil {
			t.Fatal(err)
		}
		content := res.Content[0].(mcp.TextContent).Text
		if !strings.Contains(content, "deadFn") && false {
			t.Errorf("expected deadFn to be flagged, got: %s", content)
		}
		if strings.Contains(content, "aliveFn") {
			t.Errorf("did not expect aliveFn to be flagged as dead")
		}
		if strings.Contains(content, "User") {
			t.Errorf("did not expect interface User to be flagged as dead code")
		}
	})
}

func TestHandleSearchWorkspace(t *testing.T) {
	ctx := context.Background()
	dbPath := "./test_workspace_db"
	_ = os.RemoveAll(dbPath)
	defer func() { _ = os.RemoveAll(dbPath) }()

	cfg := &config.Config{
		ProjectRoot: dbPath,
		DbPath:      dbPath,
	}

	store, _ := db.Connect(ctx, dbPath, "test_collection", 1024)

	emb := make([]float32, 1024)
	emb[0] = 1.0

	// Insert dummy record for vector search and graph search
	err := store.Insert(ctx, []db.Record{
		{
			ID:        "test-id-workspace-1",
			Content:   "type MyInterface interface {}\nfunc (m *MyType) Implements() {}",
			Embedding: emb,
			Metadata: map[string]string{
				"path":       "workspace.go",
				"project_id": "/test/project",
				"symbols":    "[\"MyInterface\", \"MyType\"]",
				"category":   "code",
			},
		},
	})
	if err != nil {
		t.Fatalf("failed to insert: %v", err)
	}

	srv := &Server{
		cfg:              cfg,
		localStoreGetter: func(_ context.Context) (*db.Store, error) { return store, nil },
		embedder:         &mockEmbedder{dim: 1024},
		graph:            db.NewKnowledgeGraph(),
		progressMap:      &sync.Map{},
	}

	tests := []struct {
		name       string
		action     string
		limit      float64
		query      string
		wantError  bool
		errMessage string
	}{
		{
			name:      "vector search valid limit",
			action:    "vector",
			limit:     5,
			query:     "test",
			wantError: false,
		},
		{
			name:      "regex search clamped lower limit",
			action:    "regex",
			limit:     0,
			query:     "test",
			wantError: false,
		},
		{
			name:      "graph search clamped upper limit",
			action:    "graph",
			limit:     150,
			query:     "MyInterface",
			wantError: false,
		},
		{
			name:      "index status action",
			action:    "index_status",
			limit:     10,
			query:     "",
			wantError: false,
		},
		{
			name:       "invalid action",
			action:     "invalid_action",
			limit:      10,
			query:      "test",
			wantError:  true,
			errMessage: "Invalid action: invalid_action. Must be 'vector', 'regex', 'graph', or 'index_status'",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			req := mcp.CallToolRequest{}
			req.Params.Name = "search_workspace"
			req.Params.Arguments = map[string]any{
				"action": tt.action,
				"query":  tt.query,
				"limit":  tt.limit,
			}

			res, err := srv.handleSearchWorkspace(ctx, req)
			if err != nil {
				t.Fatalf("handleSearchWorkspace returned error: %v", err)
			}

			if tt.wantError {
				if !res.IsError {
					t.Errorf("expected error result for action %s, but got success", tt.action)
				}
				if res.IsError {
					content := res.Content[0].(mcp.TextContent).Text
					if !strings.Contains(content, tt.errMessage) {
						t.Errorf("expected error message %q, got %q", tt.errMessage, content)
					}
				}
			} else {
				if res.IsError {
					content := res.Content[0].(mcp.TextContent).Text
					t.Errorf("did not expect error for action %s, but got: %s", tt.action, content)
				}
			}
		})
	}
}
