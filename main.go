package main

import (
	"context"
	"flag"
	"fmt"
	"os"
	"path/filepath"
	"runtime"

	"vector-mcp-go/internal/config"
	"vector-mcp-go/internal/db"
	"vector-mcp-go/internal/embedding"
	"vector-mcp-go/internal/indexer"

	"github.com/mark3labs/mcp-go/mcp"
	"github.com/mark3labs/mcp-go/server"
	"github.com/yalue/onnxruntime_go"
)

func init() {
	if runtime.GOOS == "linux" {
		onnxruntime_go.SetSharedLibraryPath("/home/nilesh/Documents/vector-mcp-go/lib/libonnxruntime.so")
	}
	err := onnxruntime_go.InitializeEnvironment()
	if err != nil {
		fmt.Fprintf(os.Stderr, "Error initializing ONNX runtime: %v\n", err)
	}
}

func main() {
	cfg := config.LoadConfig()

	indexFlag := flag.Bool("index", false, "Run full codebase indexing and exit")
	flag.Parse()

	_, err := embedding.EnsureModel(cfg.ModelsDir)
	if err != nil {
		fmt.Fprintf(os.Stderr, "Error managing model: %v\n", err)
	}

	if *indexFlag {
		runStandaloneIndex(cfg)
		return
	}

	s := server.NewMCPServer(
		"vector-mcp-go",
		"1.2.0",
		server.WithLogging(),
	)

	// Tools
	s.AddTool(mcp.NewTool("store_context",
		mcp.WithDescription("Store text content in the project brain"),
		mcp.WithString("text", mcp.Description("The text content to store")),
		mcp.WithString("type", mcp.Description("Type of information (documentation, knowledge, task)")),
	), func(ctx context.Context, request mcp.CallToolRequest) (*mcp.CallToolResult, error) {
		text := request.GetString("text", "")
		infoType := request.GetString("type", "knowledge")
		embedder, _ := embedding.NewEmbedder(cfg.ModelsDir)
		defer embedder.Close()
		emb, _ := embedder.Embed(ctx, text)
		store, _ := db.Connect(ctx, cfg.DbPath, "project_context")
		err := store.Insert(ctx, []db.Record{{
			ID:        fmt.Sprintf("manual-%d", runtime.NumGoroutine()), // Simple ID
			Content:   text,
			Embedding: emb,
			Metadata: map[string]string{
				"type": infoType,
			},
		}})
		if err != nil {
			return mcp.NewToolResultError(err.Error()), nil
		}
		return mcp.NewToolResultText("Context stored successfully."), nil
	})

	s.AddTool(mcp.NewTool("retrieve_context",
		mcp.WithDescription("Retrieve relevant context from the project brain"),
		mcp.WithString("query", mcp.Description("The search query")),
		mcp.WithNumber("topK", mcp.Description("Number of results (default 5)")),
	), func(ctx context.Context, request mcp.CallToolRequest) (*mcp.CallToolResult, error) {
		query := request.GetString("query", "")
		topK := request.GetInt("topK", 5)
		embedder, _ := embedding.NewEmbedder(cfg.ModelsDir)
		defer embedder.Close()
		emb, _ := embedder.Embed(ctx, query)
		store, _ := db.Connect(ctx, cfg.DbPath, "project_context")
		results, _ := store.Search(ctx, emb, topK)
		var out string
		for _, r := range results {
			out += fmt.Sprintf("--- %s ---\n%s\n", r.Metadata["path"], r.Content)
		}
		return mcp.NewToolResultText(out), nil
	})

	s.AddTool(mcp.NewTool("update_task_status",
		mcp.WithDescription("Update the status of a specific task within the project brain"),
		mcp.WithString("taskId", mcp.Description("The description or ID of the task")),
		mcp.WithString("status", mcp.Description("The current status (pending, in-progress, completed, blocked)")),
		mcp.WithString("notes", mcp.Description("Progress notes")),
	), func(ctx context.Context, request mcp.CallToolRequest) (*mcp.CallToolResult, error) {
		taskId := request.GetString("taskId", "")
		status := request.GetString("status", "pending")
		notes := request.GetString("notes", "")
		store, _ := db.Connect(ctx, cfg.DbPath, "project_context")
		err := store.Insert(ctx, []db.Record{{
			ID:      fmt.Sprintf("task-%s", taskId),
			Content: fmt.Sprintf("Task: %s\nStatus: %s\nNotes: %s", taskId, status, notes),
			Metadata: map[string]string{
				"type":   "task",
				"taskId": taskId,
				"status": status,
				"notes":  notes,
			},
			Embedding: make([]float32, 1024),
		}})
		if err != nil {
			return mcp.NewToolResultError(err.Error()), nil
		}
		return mcp.NewToolResultText(fmt.Sprintf("Task '%s' updated to %s.", taskId, status)), nil
	})

	s.AddTool(mcp.NewTool("get_project_health",
		mcp.WithDescription("Get a high-level overview of the project's health and task completion status"),
	), func(ctx context.Context, request mcp.CallToolRequest) (*mcp.CallToolResult, error) {
		store, _ := db.Connect(ctx, cfg.DbPath, "project_context")
		tasks, _ := store.GetByMetadata(ctx, "type", "task")
		if len(tasks) == 0 {
			return mcp.NewToolResultText("No tasks found in project brain."), nil
		}
		var out string = "📊 Project Health Overview:\n"
		for _, t := range tasks {
			out += fmt.Sprintf("- [%s] %s: %s\n", t.Metadata["status"], t.Metadata["taskId"], t.Metadata["notes"])
		}
		return mcp.NewToolResultText(out), nil
	})

	s.AddTool(mcp.NewTool("index_codebase",
		mcp.WithDescription("Index the entire project codebase"),
		mcp.WithString("projectId", mcp.Description("Project identifier")),
	), func(ctx context.Context, request mcp.CallToolRequest) (*mcp.CallToolResult, error) {
		fmt.Fprintf(os.Stderr, "🚀 Go-Indexer: Running full index...\n")
		err := IndexFullCodebase(ctx, cfg)
		if err != nil {
			return mcp.NewToolResultError(err.Error()), nil
		}
		return mcp.NewToolResultText("Codebase indexed successfully."), nil
	})

	s.AddTool(mcp.NewTool("index_file",
		mcp.WithDescription("Index a specific file for incremental updates"),
		mcp.WithString("filePath", mcp.Description("Relative path to file")),
	), func(ctx context.Context, request mcp.CallToolRequest) (*mcp.CallToolResult, error) {
		relPath := request.GetString("filePath", "")
		absPath := filepath.Join(cfg.ProjectRoot, relPath)
		fmt.Fprintf(os.Stderr, "🚀 Go-Indexer: Indexing single file: %s\n", relPath)
		err := indexSingleFile(ctx, absPath, cfg)
		if err != nil {
			return mcp.NewToolResultError(err.Error()), nil
		}
		return mcp.NewToolResultText(fmt.Sprintf("File '%s' indexed successfully.", relPath)), nil
	})

	if err := server.ServeStdio(s); err != nil {
		fmt.Fprintf(os.Stderr, "Server error: %v\n", err)
		os.Exit(1)
	}
}

func runStandaloneIndex(cfg *config.Config) {
	err := IndexFullCodebase(context.Background(), cfg)
	if err != nil {
		fmt.Fprintf(os.Stderr, "❌ Indexing failed: %v\n", err)
		return
	}
	fmt.Fprintf(os.Stderr, "✅ Go-Indexer: Standalone indexing complete.\n")
}

func IndexFullCodebase(ctx context.Context, cfg *config.Config) error {
	fmt.Fprintf(os.Stderr, "🚀 Go-Indexer: Initializing local Chromem-go at %s...\n", cfg.DbPath)

	store, err := db.Connect(ctx, cfg.DbPath, "project_context")
	if err != nil {
		return err
	}

	fmt.Fprintf(os.Stderr, "🧠 Go-Indexer: Loading BGE-M3 Embedder...\n")
	embedder, err := embedding.NewEmbedder(cfg.ModelsDir)
	if err != nil {
		return fmt.Errorf("failed to load embedder: %w", err)
	}
	defer embedder.Close()

	files, err := indexer.ScanFiles(cfg.ProjectRoot)
	if err != nil {
		return err
	}

	fmt.Fprintf(os.Stderr, "📁 Found %d files. Generating real embeddings...\n", len(files))

	for i, path := range files {
		relPath := config.GetRelativePath(path, cfg.ProjectRoot)
		hash, _ := indexer.GetHash(path)

		// Incremental check: Does this file's hash already exist for index 0?
		existing, _ := store.GetByID(ctx, fmt.Sprintf("%s-0", hash))
		if existing.ID != "" {
			fmt.Fprintf(os.Stderr, "[%d/%d] ⏩ Skipping (already indexed): %s\n", i+1, len(files), relPath)
			continue
		}

		fmt.Fprintf(os.Stderr, "[%d/%d] 📄 Indexing: %s\n", i+1, len(files), relPath)
		content, _ := os.ReadFile(path)

		chunks := indexer.CreateChunks(string(content), filepath.Ext(path))
		var records []db.Record

		for j, chunk := range chunks {
			emb, err := embedder.Embed(ctx, chunk.Content)
			if err != nil {
				fmt.Fprintf(os.Stderr, "⚠️ Skipping problematic block in %s: %v\n", relPath, err)
				continue
			}

			records = append(records, db.Record{
				ID:        fmt.Sprintf("%s-%d", hash, j),
				Content:   chunk.Content,
				Embedding: emb,
				Metadata: map[string]string{
					"path":    relPath,
					"hash":    hash,
					"symbols": fmt.Sprintf("%v", chunk.Symbols),
				},
			})
		}

		if len(records) > 0 {
			err = store.Insert(ctx, records)
			if err != nil {
				fmt.Fprintf(os.Stderr, "⚠️ Error inserting %s: %v\n", relPath, err)
			}
		}
	}

	return nil
}

func indexSingleFile(ctx context.Context, path string, cfg *config.Config) error {
	store, err := db.Connect(ctx, cfg.DbPath, "project_context")
	if err != nil {
		return err
	}
	embedder, err := embedding.NewEmbedder(cfg.ModelsDir)
	if err != nil {
		return err
	}
	defer embedder.Close()

	relPath := config.GetRelativePath(path, cfg.ProjectRoot)
	hash, err := indexer.GetHash(path)
	if err != nil {
		return err
	}

	content, err := os.ReadFile(path)
	if err != nil {
		return err
	}

	chunks := indexer.CreateChunks(string(content), filepath.Ext(path))
	var records []db.Record

	for j, chunk := range chunks {
		emb, err := embedder.Embed(ctx, chunk.Content)
		if err != nil {
			fmt.Fprintf(os.Stderr, "⚠️ Skipping problematic block in %s: %v\n", relPath, err)
			continue
		}

		records = append(records, db.Record{
			ID:        fmt.Sprintf("%s-%d", hash, j),
			Content:   chunk.Content,
			Embedding: emb,
			Metadata: map[string]string{
				"path":    relPath,
				"hash":    hash,
				"symbols": fmt.Sprintf("%v", chunk.Symbols),
			},
		})
	}

	if len(records) > 0 {
		return store.Insert(ctx, records)
	}
	return nil
}
