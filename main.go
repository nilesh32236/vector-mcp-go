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
	"strings"
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
		
		// Commands that don't need the embedder pool
		if cmd == "status" || cmd == "health" {
			store, err := getStore(ctx, cfg)
			if err != nil {
				logger.Error("DB connection failed", "error", err)
				os.Exit(1)
			}
			if cmd == "status" {
				res, _ := runStatus(ctx, store, cfg)
				fmt.Println(res)
			} else {
				res, _ := runHealth(ctx, store)
				fmt.Println(res)
			}
			return
		}

		// Initialize Embedder Pool for commands that need it
		pool, err := embedding.NewEmbedderPool(ctx, cfg.ModelsDir, 1)
		if err != nil {
			logger.Error("Failed to initialize embedder pool", "error", err)
			os.Exit(1)
		}
		embedPool = pool
		defer embedPool.Close()

		store, err := getStore(ctx, cfg)
		if err != nil {
			logger.Error("DB connection failed", "error", err)
			os.Exit(1)
		}

		switch cmd {
		case "retrieve_context":
			query := ""
			topK := 5
			if len(args) > 1 {
				for i := 1; i < len(args); i++ {
					if args[i] == "-query" && i+1 < len(args) {
						query = args[i+1]
						i++
					} else if args[i] == "-topK" && i+1 < len(args) {
						fmt.Sscanf(args[i+1], "%d", &topK)
						i++
					}
				}
			}
			if query == "" {
				fmt.Println("Usage: ./vector-mcp-go retrieve_context -query \"your query\" [-topK 5]")
				return
			}
			res, err := runRetrieveContext(ctx, store, query, topK)
			if err != nil {
				fmt.Printf("Error: %v\n", err)
			} else {
				fmt.Println(res)
			}
			return
		}
	}

	// Default initialization for MCP server mode
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

	// Tool: get_related_context
	s.AddTool(mcp.NewTool("get_related_context",
		mcp.WithDescription("Retrieve context for a file and its local dependencies"),
		mcp.WithString("filePath", mcp.Description("The relative path of the file to analyze")),
	), func(ctx context.Context, request mcp.CallToolRequest) (*mcp.CallToolResult, error) {
		filePath := request.GetString("filePath", "")
		store, err := getStore(ctx, cfg)
		if err != nil {
			return mcp.NewToolResultError(err.Error()), nil
		}

		// 1. Get all chunks for the target file
		records, err := store.GetByPath(ctx, filePath)
		if err != nil {
			return mcp.NewToolResultError(fmt.Sprintf("Failed to get records for %s: %v", filePath, err)), nil
		}
		if len(records) == 0 {
			return mcp.NewToolResultText(fmt.Sprintf("No context found for file: %s", filePath)), nil
		}

		// 2. Extract unique relationships (local dependencies)
		uniqueDeps := make(map[string]bool)
		for _, r := range records {
			var deps []string
			relStr := r.Metadata["relationships"]
			if relStr != "" {
				if err := json.Unmarshal([]byte(relStr), &deps); err == nil {
					for _, d := range deps {
						// Filter for local imports:
						// - Starts with module name (vector-mcp-go)
						// - Or is a relative path (./ or ../)
						// - Or doesn't contain a dot in the first segment (simplified stdlib check)
						// Actually, standard library is usually like "fmt", "os", etc.
						// External is "github.com/..."
						isLocal := strings.HasPrefix(d, "vector-mcp-go") || 
						           strings.HasPrefix(d, "./") || 
						           strings.HasPrefix(d, "../")
						
						if isLocal {
							uniqueDeps[d] = true
						}
					}
				}
			}
		}

		var out strings.Builder
		out.WriteString(fmt.Sprintf("### Summary for %s\n", filePath))
		out.WriteString(fmt.Sprintf("Found %d chunks in this file.\n\n", len(records)))
		for i, r := range records {
			if i < 2 { // Show first 2 chunks as summary
				out.WriteString(fmt.Sprintf("Chunk %d:\n%s\n\n", i+1, r.Content))
			}
		}

		if len(uniqueDeps) > 0 {
			out.WriteString("### Dependencies Context:\n")
			allRecords, _ := store.GetAllRecords(ctx)
			for dep := range uniqueDeps {
				// Try to find the file path for this dependency
				depPath := dep
				if strings.HasPrefix(dep, "vector-mcp-go/") {
					depPath = strings.TrimPrefix(dep, "vector-mcp-go/")
				}

				// Search for top 3 relevant chunks from this dependency
				foundAny := false
				count := 0
				for _, dr := range allRecords {
					if strings.Contains(dr.Metadata["path"], depPath) {
						out.WriteString(fmt.Sprintf("#### From dependency: %s (Path: %s)\n", dep, dr.Metadata["path"]))
						out.WriteString(fmt.Sprintf("%s\n\n", dr.Content))
						foundAny = true
						count++
						if count >= 3 { break }
					}
				}
				if !foundAny {
					out.WriteString(fmt.Sprintf("#### Dependency: %s (No indexed chunks found)\n\n", dep))
				}
			}
		} else {
			out.WriteString("\n*No local dependencies found for this file.*\n")
		}

		return mcp.NewToolResultText(out.String()), nil
	})

	// Tool: index_status
	s.AddTool(mcp.NewTool("index_status",
		mcp.WithDescription("Check synchronization status between filesystem and vector index"),
	), func(ctx context.Context, request mcp.CallToolRequest) (*mcp.CallToolResult, error) {
		store, err := getStore(ctx, cfg)
		if err != nil {
			return mcp.NewToolResultError(err.Error()), nil
		}
		res, err := runStatus(ctx, store, cfg)
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

func runRetrieveContext(ctx context.Context, store *db.Store, query string, topK int) (string, error) {
	emb, err := getEmbedding(ctx, query)
	if err != nil {
		return "", fmt.Errorf("embedding failed: %w", err)
	}
	
	results, err := store.Search(ctx, emb, topK)
	if err != nil {
		return "", fmt.Errorf("search failed: %w", err)
	}
	
	var out string
	for _, r := range results {
		out += fmt.Sprintf("--- %s ---\n%s\n", r.Metadata["path"], r.Content)
	}
	if out == "" {
		out = "No relevant context found."
	}
	return out, nil
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
	// 1. Get current files from disk
	diskFiles, err := indexer.ScanFiles(cfg.ProjectRoot)
	if err != nil {
		return "", err
	}

	// 2. Get all indexed files from DB
	dbMapping, err := store.GetPathHashMapping(ctx)
	if err != nil {
		return "", err
	}

	var indexed, updated, missing []string
	diskPaths := make(map[string]bool)

	for _, absPath := range diskFiles {
		relPath := config.GetRelativePath(absPath, cfg.ProjectRoot)
		diskPaths[relPath] = true
		
		currentHash, _ := indexer.GetHash(absPath)
		
		if dbHash, exists := dbMapping[relPath]; exists {
			if dbHash == currentHash {
				indexed = append(indexed, relPath)
			} else {
				updated = append(updated, relPath)
			}
		} else {
			missing = append(missing, relPath)
		}
	}

	// 3. Find deleted files (in DB but not on disk)
	var deleted []string
	for dbPath := range dbMapping {
		if !diskPaths[dbPath] {
			deleted = append(deleted, dbPath)
		}
	}

	var out strings.Builder
	out.WriteString("🔍 Detailed Index Status:\n")
	out.WriteString(fmt.Sprintf("✅ Fully Indexed: %d files\n", len(indexed)))
	out.WriteString(fmt.Sprintf("🔄 Outdated/Modified: %d files\n", len(updated)))
	out.WriteString(fmt.Sprintf("📂 Missing from Index: %d files\n", len(missing)))
	out.WriteString(fmt.Sprintf("🗑️ Deleted from Disk: %d files\n", len(deleted)))
	
	if len(updated) > 0 || len(missing) > 0 || len(deleted) > 0 {
		out.WriteString("\n💡 Recommendation: Run './vector-mcp-go -index' to synchronize.")
	}

	return out.String(), nil
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

	// Pre-fetch hash mapping for fast lookup
	hashMapping, _ := store.GetPathHashMapping(ctx)
	
	var toIndex []string
	processedCount := 0
	for _, path := range files {
		relPath := config.GetRelativePath(path, cfg.ProjectRoot)
		currentHash, _ := indexer.GetHash(path)
		
		if existingHash, ok := hashMapping[relPath]; ok && existingHash == currentHash {
			summary.FilesSkipped++
			processedCount++
			if processedCount%100 == 0 || processedCount == len(files) {
				fmt.Printf("⏳ Progress: %d/%d files processed (Fast-skip unchanged)...\n", processedCount, len(files))
			}
			continue
		}
		toIndex = append(toIndex, path)
	}

	// 3. Prune deleted files
	deletedCount := 0
	for dbPath := range hashMapping {
		isStillOnDisk := false
		for _, absPath := range files {
			if config.GetRelativePath(absPath, cfg.ProjectRoot) == dbPath {
				isStillOnDisk = true
				break
			}
		}
		if !isStillOnDisk {
			store.DeleteByPath(ctx, dbPath)
			deletedCount++
		}
	}
	if deletedCount > 0 {
		fmt.Printf("🧹 Pruned %d deleted files from index.\n", deletedCount)
	}

	if len(toIndex) == 0 {
		fmt.Printf("✅ All %d files are already indexed and up-to-date.\n", len(files))
		summary.DurationMs = time.Since(startTime).Milliseconds()
		return summary, nil
	}

	fmt.Printf("📂 Found %d new or modified files to index.\n", len(toIndex))

	var wg sync.WaitGroup
	results := make(chan result, len(toIndex))
	tasks := make(chan string, len(toIndex))
	for i := 0; i < runtime.NumCPU(); i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for path := range tasks {
				results <- processFile(ctx, path, cfg, store)
			}
		}()
	}
	for _, path := range toIndex { tasks <- path }
	close(tasks)
	
	go func() { wg.Wait(); close(results) }()
	
	for r := range results {
		processedCount++
		if processedCount%50 == 0 || processedCount == len(files) {
			fmt.Printf("⏳ Progress: %d/%d files processed...\n", processedCount, len(files))
		}
		if r.err != "" {
			summary.Errors = append(summary.Errors, r.err)
		} else if r.indexed {
			summary.FilesIndexed++
		} else if r.skipped {
			summary.FilesSkipped++
		}
	}
	summary.DurationMs = time.Since(startTime).Milliseconds()
	return summary, nil
}

type result struct {
	indexed bool
	skipped bool
	err     string
}

func processFile(ctx context.Context, path string, cfg *config.Config, store *db.Store) result {
	relPath := config.GetRelativePath(path, cfg.ProjectRoot)
	
	// 1. Calculate current hash
	currentHash, err := indexer.GetHash(path)
	if err != nil {
		return result{err: err.Error()}
	}

	// 2. Check if already indexed with same hash
	existingHash, _ := store.GetFileHash(ctx, relPath)
	if existingHash == currentHash {
		// Skip re-indexing
		return result{skipped: true}
	}

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
		relJSON, _ := json.Marshal(chunk.Relationships)
		symJSON, _ := json.Marshal(chunk.Symbols)
		records = append(records, db.Record{
			ID: fmt.Sprintf("%s-%d", relPath, time.Now().UnixNano()),
			Content: chunk.Content,
			Embedding: emb,
			Metadata: map[string]string{
				"path":          relPath,
				"hash":          currentHash,
				"relationships": string(relJSON),
				"symbols":       string(symJSON),
			},
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
	fmt.Printf("🚀 Starting full indexing for: %s\n", cfg.ProjectRoot)
	fmt.Println("📂 Detailed logs: tail -f ~/.local/share/vector-mcp-go/server.log")
	
	summary, err := IndexFullCodebase(context.Background(), cfg, logger)
	if err != nil {
		fmt.Printf("❌ Indexing failed: %v\n", err)
		logger.Error("Standalone indexing failed", "error", err)
		return
	}
	
	fmt.Printf("✅ Indexing complete! Processed %d files.\n", summary.FilesProcessed)
	logger.Info("Standalone indexing complete", "summary", summary)
}
