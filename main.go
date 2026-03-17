package main

import (
	"context"
	"encoding/json"
	"flag"
	"fmt"
	"log/slog"
	"os"
	"path/filepath"
	"runtime"
	"sync"
	"time"

	"vector-mcp-go/internal/config"
	"vector-mcp-go/internal/db"
	"vector-mcp-go/internal/embedding"
	"vector-mcp-go/internal/indexer"

	"github.com/fsnotify/fsnotify"
	"github.com/mark3labs/mcp-go/mcp"
	"github.com/mark3labs/mcp-go/server"
	"github.com/yalue/onnxruntime_go"
)

type IndexSummary struct {
	Status         string   `json:"status"`
	FilesProcessed int      `json:"files_processed"`
	FilesIndexed   int      `json:"files_indexed"`
	FilesSkipped   int      `json:"files_skipped"`
	Errors         []string `json:"errors"`
	DurationMs     int64    `json:"duration_ms"`
}

var (
	dbMu        sync.RWMutex
	globalStore *db.Store
	embedPool   *embedding.EmbedderPool
)

func getStore(ctx context.Context, cfg *config.Config) (*db.Store, error) {
	dbMu.Lock()
	defer dbMu.Unlock()
	if globalStore != nil {
		return globalStore, nil
	}
	store, err := db.Connect(ctx, cfg.DbPath, "project_context")
	if err != nil {
		return nil, err
	}
	globalStore = store
	return globalStore, nil
}

func init() {
	if runtime.GOOS == "linux" {
		execPath, _ := os.Executable()
		execDir := filepath.Dir(execPath)
		libPath := filepath.Join(execDir, "lib", "libonnxruntime.so")
		if _, err := os.Stat(libPath); os.IsNotExist(err) {
			home, _ := os.UserHomeDir()
			libPath = filepath.Join(home, ".local", "share", "vector-mcp-go", "lib", "libonnxruntime.so")
		}
		onnxruntime_go.SetSharedLibraryPath(libPath)
	}
	err := onnxruntime_go.InitializeEnvironment()
	if err != nil {
		fmt.Fprintf(os.Stderr, "Error initializing ONNX runtime: %v\n", err)
	}
}

func main() {
	cfg := config.LoadConfig()
	logger := cfg.Logger

	indexFlag := flag.Bool("index", false, "Run full codebase indexing and exit")
	flag.Parse()

	ctx := context.Background()
	
	// CLI Support for maintenance
	args := flag.Args()
	if len(args) > 0 {
		cmd := args[0]
		store, err := getStore(ctx, cfg)
		if err != nil {
			logger.Error("DB connection failed", "error", err)
			os.Exit(1)
		}
		switch cmd {
		case "status":
			res, _ := runStatus(ctx, store, cfg)
			fmt.Println(res)
			return
		case "health":
			res, _ := runHealth(ctx, store)
			fmt.Println(res)
			return
		}
	}

	// Initialize Embedder Pool
	pool, err := embedding.NewEmbedderPool(ctx, cfg.ModelsDir, 2)
	if err != nil {
		logger.Error("Failed to initialize embedder pool", "error", err)
		os.Exit(1)
	}
	embedPool = pool
	defer embedPool.Close()

	if *indexFlag {
		runStandaloneIndex(cfg, logger)
		return
	}

	// Start File Watcher
	go watchFiles(cfg, logger)

	s := server.NewMCPServer(
		"vector-mcp-go",
		"2.1.0",
		server.WithLogging(),
	)

	// Tool: ping
	s.AddTool(mcp.NewTool("ping",
		mcp.WithDescription("Check server connectivity"),
	), func(ctx context.Context, request mcp.CallToolRequest) (*mcp.CallToolResult, error) {
		return mcp.NewToolResultText("pong"), nil
	})

	// Tool: store_context
	s.AddTool(mcp.NewTool("store_context",
		mcp.WithDescription("Store text content in the project brain"),
		mcp.WithString("text", mcp.Description("The text content to store")),
		mcp.WithString("type", mcp.Description("Type of information (documentation, knowledge, task)")),
	), func(ctx context.Context, request mcp.CallToolRequest) (*mcp.CallToolResult, error) {
		text := request.GetString("text", "")
		infoType := request.GetString("type", "knowledge")
		emb, err := getEmbedding(ctx, text)
		if err != nil {
			return mcp.NewToolResultError(fmt.Sprintf("Embedding failed: %v", err)), nil
		}
		store, err := getStore(ctx, cfg)
		if err != nil {
			return mcp.NewToolResultError(fmt.Sprintf("DB connection failed: %v", err)), nil
		}
		err = store.Insert(ctx, []db.Record{{
			ID:        fmt.Sprintf("manual-%d", time.Now().UnixNano()),
			Content:   text,
			Embedding: emb,
			Metadata: map[string]string{"type": infoType},
		}})
		if err != nil {
			return mcp.NewToolResultError(err.Error()), nil
		}
		return mcp.NewToolResultText("Context stored successfully."), nil
	})

	// Tool: retrieve_context
	s.AddTool(mcp.NewTool("retrieve_context",
		mcp.WithDescription("Retrieve relevant context from the project brain"),
		mcp.WithString("query", mcp.Description("The search query")),
		mcp.WithNumber("topK", mcp.Description("Number of results (default 5)")),
	), func(ctx context.Context, request mcp.CallToolRequest) (*mcp.CallToolResult, error) {
		query := request.GetString("query", "")
		topK := request.GetInt("topK", 5)
		emb, err := getEmbedding(ctx, query)
		if err != nil {
			return mcp.NewToolResultError(fmt.Sprintf("Embedding failed: %v", err)), nil
		}
		store, err := getStore(ctx, cfg)
		if err != nil {
			return mcp.NewToolResultError(fmt.Sprintf("DB connection failed: %v", err)), nil
		}
		results, err := store.Search(ctx, emb, topK)
		if err != nil {
			return mcp.NewToolResultError(fmt.Sprintf("Search failed: %v", err)), nil
		}
		var out string
		for _, r := range results {
			out += fmt.Sprintf("--- %s ---\n%s\n", r.Metadata["path"], r.Content)
		}
		if out == "" {
			out = "No relevant context found."
		}
		return mcp.NewToolResultText(out), nil
	})

	// Tool: update_task_status
	s.AddTool(mcp.NewTool("update_task_status",
		mcp.WithDescription("Update the status of a specific task within the project brain"),
		mcp.WithString("taskId", mcp.Description("The description or ID of the task")),
		mcp.WithString("status", mcp.Description("The current status")),
		mcp.WithString("notes", mcp.Description("Progress notes")),
	), func(ctx context.Context, request mcp.CallToolRequest) (*mcp.CallToolResult, error) {
		taskId := request.GetString("taskId", "")
		status := request.GetString("status", "pending")
		notes := request.GetString("notes", "")
		store, err := getStore(ctx, cfg)
		if err != nil {
			return mcp.NewToolResultError(err.Error()), nil
		}
		err = store.Insert(ctx, []db.Record{{
			ID:      fmt.Sprintf("task-%s", taskId),
			Content: fmt.Sprintf("Task: %s\nStatus: %s\nNotes: %s", taskId, status, notes),
			Metadata: map[string]string{
				"type": "task",
				"taskId": taskId,
				"status": status,
				"notes": notes,
			},
			Embedding: make([]float32, 1024),
		}})
		if err != nil {
			return mcp.NewToolResultError(err.Error()), nil
		}
		return mcp.NewToolResultText(fmt.Sprintf("Task '%s' updated.", taskId)), nil
	})

	// Tool: get_project_health
	s.AddTool(mcp.NewTool("get_project_health",
		mcp.WithDescription("Get a high-level overview of the project's health"),
	), func(ctx context.Context, request mcp.CallToolRequest) (*mcp.CallToolResult, error) {
		store, err := getStore(ctx, cfg)
		if err != nil {
			return mcp.NewToolResultError(err.Error()), nil
		}
		res, err := runHealth(ctx, store)
		if err != nil {
			return mcp.NewToolResultError(err.Error()), nil
		}
		return mcp.NewToolResultText(res), nil
	})

	// Tool: index_codebase
	s.AddTool(mcp.NewTool("index_codebase",
		mcp.WithDescription("Index the entire project codebase"),
		mcp.WithString("projectId", mcp.Description("Optional absolute path to project root to index")),
	), func(ctx context.Context, request mcp.CallToolRequest) (*mcp.CallToolResult, error) {
		projectId := request.GetString("projectId", "")
		targetCfg := cfg
		if projectId != "" {
			targetCfg = &config.Config{
				ProjectRoot: projectId,
				DbPath:      cfg.DbPath,
				ModelsDir:   cfg.ModelsDir,
				Logger:      cfg.Logger,
			}
		}
		summary, err := IndexFullCodebase(ctx, targetCfg, logger)
		if err != nil {
			return mcp.NewToolResultError(err.Error()), nil
		}
		summaryJSON, _ := json.MarshalIndent(summary, "", "  ")
		return mcp.NewToolResultText(string(summaryJSON)), nil
	})

	// Tool: index_file
	s.AddTool(mcp.NewTool("index_file",
		mcp.WithDescription("Index a specific file for incremental updates"),
		mcp.WithString("filePath", mcp.Description("Relative or absolute path to file")),
		mcp.WithString("projectId", mcp.Description("Optional absolute path to project root")),
	), func(ctx context.Context, request mcp.CallToolRequest) (*mcp.CallToolResult, error) {
		relPath := request.GetString("filePath", "")
		projectId := request.GetString("projectId", "")
		
		targetCfg := cfg
		if projectId != "" {
			targetCfg = &config.Config{
				ProjectRoot: projectId,
				DbPath:      cfg.DbPath,
				ModelsDir:   cfg.ModelsDir,
				Logger:      cfg.Logger,
			}
		}

		absPath := relPath
		if !filepath.IsAbs(relPath) {
			absPath = filepath.Join(targetCfg.ProjectRoot, relPath)
		}

		summary, err := indexSingleFile(ctx, absPath, targetCfg, logger)
		if err != nil {
			return mcp.NewToolResultError(err.Error()), nil
		}
		summaryJSON, _ := json.MarshalIndent(summary, "", "  ")
		return mcp.NewToolResultText(string(summaryJSON)), nil
	})

	logger.Info("MCP Server listening on stdio...")
	if err := server.ServeStdio(s); err != nil {
		logger.Error("Server error", "error", err)
	}
}

func getEmbedding(ctx context.Context, text string) ([]float32, error) {
	e, err := embedPool.Get(ctx)
	if err != nil {
		return nil, err
	}
	defer embedPool.Put(e)
	return e.Embed(ctx, text)
}

func runHealth(ctx context.Context, store *db.Store) (string, error) {
	allRecords, err := store.GetAllRecords(ctx)
	if err != nil {
		return "", err
	}
	var out string = "📊 Project Health Overview:\n"
	count := 0
	for _, r := range allRecords {
		if r.Metadata["type"] == "task" {
			out += fmt.Sprintf("- [%s] %s: %s\n", r.Metadata["status"], r.Metadata["taskId"], r.Metadata["notes"])
			count++
		}
	}
	if count == 0 {
		return "No tasks found in project brain.", nil
	}
	return out, nil
}

func runStatus(ctx context.Context, store *db.Store, cfg *config.Config) (string, error) {
	files, err := indexer.ScanFiles(cfg.ProjectRoot)
	if err != nil {
		return "", err
	}
	return fmt.Sprintf("Index status: %d files in project root, %d records in database.", len(files), store.Count()), nil
}

func watchFiles(cfg *config.Config, logger *slog.Logger) {
	watcher, err := fsnotify.NewWatcher()
	if err != nil {
		logger.Error("Failed to create watcher", "error", err)
		return
	}
	defer watcher.Close()
	
	watcher.Add(cfg.ProjectRoot)
	for {
		select {
		case event, ok := <-watcher.Events:
			if !ok { return }
			if event.Has(fsnotify.Write) {
				ext := filepath.Ext(event.Name)
				if ext == ".go" || ext == ".ts" || ext == ".tsx" || ext == ".js" || ext == ".jsx" {
					logger.Info("Auto re-indexing modified file", "file", event.Name)
					indexSingleFile(context.Background(), event.Name, cfg, logger)
				}
			}
		case err, ok := <-watcher.Errors:
			if !ok { return }
			logger.Error("Watcher error", "error", err)
		}
	}
}

func IndexFullCodebase(ctx context.Context, cfg *config.Config, logger *slog.Logger) (IndexSummary, error) {
	startTime := time.Now()
	summary := IndexSummary{Status: "completed"}
	store, err := getStore(ctx, cfg)
	if err != nil { return summary, err }
	files, err := indexer.ScanFiles(cfg.ProjectRoot)
	if err != nil { return summary, err }
	summary.FilesProcessed = len(files)
	var wg sync.WaitGroup
	results := make(chan result, len(files))
	tasks := make(chan string, len(files))
	for i := 0; i < runtime.NumCPU(); i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for path := range tasks {
				results <- processFile(ctx, path, cfg, store)
			}
		}()
	}
	for _, path := range files { tasks <- path }
	close(tasks)
	go func() { wg.Wait(); close(results) }()
	for r := range results {
		if r.err != "" { summary.Errors = append(summary.Errors, r.err)
		} else if r.indexed { summary.FilesIndexed++
		} else { summary.FilesSkipped++ }
	}
	summary.DurationMs = time.Since(startTime).Milliseconds()
	return summary, nil
}

type result struct { indexed bool; err string }

func processFile(ctx context.Context, path string, cfg *config.Config, store *db.Store) result {
	relPath := config.GetRelativePath(path, cfg.ProjectRoot)
	cfg.Logger.Info("Indexing file", "path", relPath)

	content, err := os.ReadFile(path)
	if err != nil {
		return result{err: err.Error()}
	}
	chunks := indexer.CreateChunks(string(content), filepath.Ext(path))
	var records []db.Record
	for _, chunk := range chunks {
		emb, err := getEmbedding(ctx, chunk.Content)
		if err != nil { continue }
		records = append(records, db.Record{
			ID: fmt.Sprintf("%s-%d", relPath, time.Now().UnixNano()),
			Content: chunk.Content,
			Embedding: emb,
			Metadata: map[string]string{"path": relPath},
		})
	}
	if len(records) > 0 {
		store.DeleteByPath(ctx, relPath)
		err = store.Insert(ctx, records)
		if err != nil { return result{err: err.Error()} }
		return result{indexed: true}
	}
	return result{}
}

func indexSingleFile(ctx context.Context, path string, cfg *config.Config, logger *slog.Logger) (IndexSummary, error) {
	store, err := getStore(ctx, cfg)
	if err != nil { return IndexSummary{}, err }
	res := processFile(ctx, path, cfg, store)
	if res.err != "" { return IndexSummary{Status: "error", Errors: []string{res.err}}, nil }
	return IndexSummary{Status: "completed", FilesProcessed: 1, FilesIndexed: 1}, nil
}

func runStandaloneIndex(cfg *config.Config, logger *slog.Logger) {
	summary, err := IndexFullCodebase(context.Background(), cfg, logger)
	if err != nil { logger.Error("Standalone indexing failed", "error", err); return }
	logger.Info("Standalone indexing complete", "summary", summary)
}
