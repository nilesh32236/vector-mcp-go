package main

import (
	"context"
	"encoding/json"
	"flag"
	"fmt"
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
	embedMu sync.Mutex
	dbMu    sync.RWMutex
	globalStore *db.Store
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
		
		// Fallback to hardcoded for dev
		if _, err := os.Stat(libPath); os.IsNotExist(err) {
			libPath = "/home/nilesh/Documents/vector-mcp-go/lib/libonnxruntime.so"
		}
		
		fmt.Fprintf(os.Stderr, "🔧 Setting ONNX library path: %s\n", libPath)
		onnxruntime_go.SetSharedLibraryPath(libPath)
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

	if *indexFlag {
		runStandaloneIndex(cfg)
		return
	}

	// Support CLI commands for maintenance
	args := flag.Args()
	if len(args) > 0 {
		cmd := args[0]
		ctx := context.Background()
		store, err := getStore(ctx, cfg)
		if err != nil {
			fmt.Fprintf(os.Stderr, "❌ DB Error: %v\n", err)
			os.Exit(1)
		}

		switch cmd {
		case "status":
			res, _ := runStatus(ctx, store, cfg)
			fmt.Println(res)
			return
		case "cleanup":
			res, _ := runCleanup(ctx, store, cfg)
			fmt.Println(res)
			return
		case "health":
			res, _ := runHealth(ctx, store)
			fmt.Println(res)
			return
		}
	}

	// Only redirect to log file when running as a server
	logFile, err := os.OpenFile("/home/nilesh/Documents/vector-mcp-go/server_crash.log", os.O_CREATE|os.O_WRONLY|os.O_APPEND, 0666)
	if err == nil {
		os.Stderr = logFile
	}
	
	defer func() {
		if r := recover(); r != nil {
			fmt.Fprintf(os.Stderr, "FATAL PANIC in main: %v\n", r)
			os.Exit(1)
		}
	}()

	s := server.NewMCPServer(
		"vector-mcp-go",
		"1.2.0",
		server.WithLogging(),
	)

	fmt.Fprintf(os.Stderr, "🚀 Go-Indexer: Initializing server over stdio...\n")

	// 0. ping
	s.AddTool(mcp.NewTool("ping",
		mcp.WithDescription("Check server connectivity"),
	), func(ctx context.Context, request mcp.CallToolRequest) (*mcp.CallToolResult, error) {
		return mcp.NewToolResultText("pong"), nil
	})

	// 1. store_context
	s.AddTool(mcp.NewTool("store_context",
		mcp.WithDescription("Store text content in the project brain"),
		mcp.WithString("text", mcp.Description("The text content to store")),
		mcp.WithString("type", mcp.Description("Type of information (documentation, knowledge, task)")),
	), func(ctx context.Context, request mcp.CallToolRequest) (*mcp.CallToolResult, error) {
		text := request.GetString("text", "")
		infoType := request.GetString("type", "knowledge")
		
		embedder, err := embedding.NewEmbedder(cfg.ModelsDir)
		if err != nil {
			return mcp.NewToolResultError(fmt.Sprintf("Failed to load embedder: %v", err)), nil
		}
		defer embedder.Close()

		emb, err := embedder.Embed(ctx, text)
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
			Metadata: map[string]string{
				"type": infoType,
			},
		}})
		if err != nil {
			return mcp.NewToolResultError(err.Error()), nil
		}
		return mcp.NewToolResultText("Context stored successfully."), nil
	})

	// 2. retrieve_context
	s.AddTool(mcp.NewTool("retrieve_context",
		mcp.WithDescription("Retrieve relevant context from the project brain"),
		mcp.WithString("query", mcp.Description("The search query")),
		mcp.WithNumber("topK", mcp.Description("Number of results (default 5)")),
	), func(ctx context.Context, request mcp.CallToolRequest) (*mcp.CallToolResult, error) {
		query := request.GetString("query", "")
		topK := request.GetInt("topK", 5)

		embedder, err := embedding.NewEmbedder(cfg.ModelsDir)
		if err != nil {
			return mcp.NewToolResultError(fmt.Sprintf("Failed to load embedder: %v", err)), nil
		}
		defer embedder.Close()

		emb, err := embedder.Embed(ctx, query)
		if err != nil {
			return mcp.NewToolResultError(fmt.Sprintf("Embedding failed: %v", err)), nil
		}
		
		store, err := getStore(ctx, cfg)
		if err != nil {
			return mcp.NewToolResultError(fmt.Sprintf("DB connection failed: %v", err)), nil
		}
		
		results, err := store.Search(ctx, emb, topK*2) // Get more for keyword boosting
		if err != nil {
			return mcp.NewToolResultError(fmt.Sprintf("Search failed: %v", err)), nil
		}
		
		// Simple keyword boost
		queryLower := strings.ToLower(query)
		words := strings.Fields(queryLower)
		type scoredResult struct {
			record db.Record
			score  float32
		}
		var scored []scoredResult
		for _, r := range results {
			var boost float32
			contentLower := strings.ToLower(r.Content)
			for _, w := range words {
				if strings.Contains(contentLower, w) {
					boost += 0.2
				}
				if strings.Contains(strings.ToLower(r.Metadata["path"]), w) {
					boost += 0.5
				}
			}
			scored = append(scored, scoredResult{record: r, score: boost})
		}
		// Sort by boost
		for i := 0; i < len(scored); i++ {
			for j := i + 1; j < len(scored); j++ {
				if scored[i].score < scored[j].score {
					scored[i], scored[j] = scored[j], scored[i]
				}
			}
		}

		var out string
		limit := topK
		if len(scored) < limit {
			limit = len(scored)
		}
		for i := 0; i < limit; i++ {
			r := scored[i].record
			out += fmt.Sprintf("--- %s ---\nIndexed: %s\nRelations: %s\n%s\n", 
				r.Metadata["path"], r.Metadata["last_indexed"], r.Metadata["relationships"], r.Content)
		}
		if out == "" {
			out = "No relevant context found."
		}
		return mcp.NewToolResultText(out), nil
	})

	// 3. update_task_status
	s.AddTool(mcp.NewTool("update_task_status",
		mcp.WithDescription("Update the status of a specific task within the project brain"),
		mcp.WithString("taskId", mcp.Description("The description or ID of the task")),
		mcp.WithString("status", mcp.Description("The current status (pending, in-progress, completed, blocked)")),
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

	// 4. get_project_health
	s.AddTool(mcp.NewTool("get_project_health",
		mcp.WithDescription("Get a high-level overview of the project's health and task completion status"),
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

	// 5. index_codebase
	s.AddTool(mcp.NewTool("index_codebase",
		mcp.WithDescription("Index the entire project codebase"),
		mcp.WithString("projectId", mcp.Description("Project identifier")),
	), func(ctx context.Context, request mcp.CallToolRequest) (*mcp.CallToolResult, error) {
		fmt.Fprintf(os.Stderr, "🚀 Go-Indexer: Running full index...\n")
		summary, err := IndexFullCodebase(ctx, cfg)
		if err != nil {
			return mcp.NewToolResultError(err.Error()), nil
		}
		summaryJSON, _ := json.MarshalIndent(summary, "", "  ")
		return mcp.NewToolResultText(string(summaryJSON)), nil
	})

	// 6. index_file
	s.AddTool(mcp.NewTool("index_file",
		mcp.WithDescription("Index a specific file for incremental updates"),
		mcp.WithString("filePath", mcp.Description("Relative path to file")),
	), func(ctx context.Context, request mcp.CallToolRequest) (*mcp.CallToolResult, error) {
		relPath := request.GetString("filePath", "")
		absPath := filepath.Join(cfg.ProjectRoot, relPath)
		fmt.Fprintf(os.Stderr, "🚀 Go-Indexer: Indexing single file: %s\n", relPath)
		summary, err := indexSingleFile(ctx, absPath, cfg)
		if err != nil {
			return mcp.NewToolResultError(err.Error()), nil
		}
		summaryJSON, _ := json.MarshalIndent(summary, "", "  ")
		return mcp.NewToolResultText(string(summaryJSON)), nil
	})

	// 7. index_status
	s.AddTool(mcp.NewTool("index_status",
		mcp.WithDescription("Check synchronization status between filesystem and vector index"),
	), func(ctx context.Context, request mcp.CallToolRequest) (*mcp.CallToolResult, error) {
		fmt.Fprintf(os.Stderr, "🚀 Go-Indexer: Checking index status...\n")
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

	// 8. cleanup_index
	s.AddTool(mcp.NewTool("cleanup_index",
		mcp.WithDescription("Remove stale entries from the vector index for files that no longer exist on disk"),
	), func(ctx context.Context, request mcp.CallToolRequest) (*mcp.CallToolResult, error) {
		fmt.Fprintf(os.Stderr, "🚀 Go-Indexer: Running index cleanup...\n")
		store, err := getStore(ctx, cfg)
		if err != nil {
			return mcp.NewToolResultError(err.Error()), nil
		}
		res, err := runCleanup(ctx, store, cfg)
		if err != nil {
			return mcp.NewToolResultError(err.Error()), nil
		}
		return mcp.NewToolResultText(res), nil
	})

	fmt.Fprintf(os.Stderr, "📶 MCP Server listening on stdio...\n")
	if err := server.ServeStdio(s); err != nil {
		fmt.Fprintf(os.Stderr, "🛑 Server error: %v\n", err)
	}
	fmt.Fprintf(os.Stderr, "👋 Server exiting.\n")
}

func runHealth(ctx context.Context, store *db.Store) (string, error) {
	allRecords, err := store.GetAllRecords(ctx)
	if err != nil {
		return "", err
	}
	
	var tasks []db.Record
	for _, r := range allRecords {
		if r.Metadata["type"] == "task" {
			tasks = append(tasks, r)
		}
	}
	
	if len(tasks) == 0 {
		return "No tasks found in project brain.", nil
	}
	var out string = "📊 Project Health Overview:\n"
	for _, t := range tasks {
		out += fmt.Sprintf("- [%s] %s: %s\n", t.Metadata["status"], t.Metadata["taskId"], t.Metadata["notes"])
	}
	return out, nil
}

func runStatus(ctx context.Context, store *db.Store, cfg *config.Config) (string, error) {
	files, err := indexer.ScanFiles(cfg.ProjectRoot)
	if err != nil {
		return "", err
	}

	type StatusResult struct {
		TotalFiles    int      `json:"total_files"`
		SyncedFiles   int      `json:"synced_files"`
		OutdatedFiles []string `json:"outdated_files"`
		MissingFiles  []string `json:"missing_files"`
	}
	result := StatusResult{TotalFiles: len(files)}

	dbRecords, _ := store.GetAllRecords(ctx)
	dbPaths := make(map[string]string)
	for _, r := range dbRecords {
		dbPaths[r.Metadata["path"]] = r.Metadata["hash"]
	}

	for _, path := range files {
		relPath := config.GetRelativePath(path, cfg.ProjectRoot)
		currentHash, _ := indexer.GetHash(path)

		dbHash, exists := dbPaths[relPath]
		if !exists {
			result.MissingFiles = append(result.MissingFiles, relPath)
		} else if dbHash != currentHash {
			result.OutdatedFiles = append(result.OutdatedFiles, relPath)
		} else {
			result.SyncedFiles++
		}
	}
	
	resJSON, _ := json.MarshalIndent(result, "", "  ")
	return string(resJSON), nil
}

func runCleanup(ctx context.Context, store *db.Store, cfg *config.Config) (string, error) {
	records, err := store.GetAllRecords(ctx)
	if err != nil {
		return "", err
	}

	pathsSeen := make(map[string]bool)
	removedCount := 0
	for _, r := range records {
		path := r.Metadata["path"]
		if path == "" || pathsSeen[path] {
			continue
		}
		pathsSeen[path] = true
		
		absPath := filepath.Join(cfg.ProjectRoot, path)
		if _, err := os.Stat(absPath); os.IsNotExist(err) {
			fmt.Fprintf(os.Stderr, "🗑️ Removing stale index for deleted file: %s\n", path)
			store.DeleteByPath(ctx, path)
			removedCount++
		}
	}
	return fmt.Sprintf("Cleanup complete. Removed %d stale file entries.", removedCount), nil
}

func runStandaloneIndex(cfg *config.Config) {
	summary, err := IndexFullCodebase(context.Background(), cfg)
	if err != nil {
		fmt.Fprintf(os.Stderr, "❌ Indexing failed: %v\n", err)
		return
	}
	summaryJSON, _ := json.MarshalIndent(summary, "", "  ")
	fmt.Fprintf(os.Stderr, "✅ Go-Indexer: Standalone indexing complete:\n%s\n", string(summaryJSON))
}

func safeEmbed(ctx context.Context, embedder *embedding.Embedder, text string) ([][]float32, error) {
	embedMu.Lock()
	emb, err := embedder.Embed(ctx, text)
	embedMu.Unlock()

	if err != nil && strings.Contains(err.Error(), "tokenizer panic") {
		// If it's a small chunk already, just fail
		if len(text) < 500 {
			return nil, err
		}
		// Split in half and try again
		mid := len(text) / 2
		// Find a newline to split at for better context preservation
		nl := strings.LastIndex(text[:mid], "\n")
		if nl != -1 {
			mid = nl
		}
		
		fmt.Fprintf(os.Stderr, "🔄 Tokenizer panic detected. Splitting chunk (%d chars) into two parts...\n", len(text))
		
		part1, err1 := safeEmbed(ctx, embedder, text[:mid])
		part2, err2 := safeEmbed(ctx, embedder, text[mid:])
		
		if err1 != nil && err2 != nil {
			return nil, fmt.Errorf("both split parts failed: %v", err1)
		}
		
		var combined [][]float32
		if err1 == nil {
			combined = append(combined, part1...)
		}
		if err2 == nil {
			combined = append(combined, part2...)
		}
		return combined, nil
	}
	
	if err != nil {
		return nil, err
	}
	return [][]float32{emb}, nil
}

func IndexFullCodebase(ctx context.Context, cfg *config.Config) (IndexSummary, error) {
	startTime := time.Now()
	summary := IndexSummary{Status: "completed"}
	
	fmt.Fprintf(os.Stderr, "🚀 Go-Indexer: Initializing local Chromem-go at %s...\n", cfg.DbPath)
	store, err := getStore(ctx, cfg)
	if err != nil {
		return summary, err
	}
	fmt.Fprintf(os.Stderr, "🧠 Go-Indexer: Loading BGE-M3 Embedder...\n")
	embedder, err := embedding.NewEmbedder(cfg.ModelsDir)
	if err != nil {
		return summary, fmt.Errorf("failed to load embedder: %w", err)
	}
	defer embedder.Close()

	files, err := indexer.ScanFiles(cfg.ProjectRoot)
	if err != nil {
		return summary, err
	}
	summary.FilesProcessed = len(files)
	fmt.Fprintf(os.Stderr, "📁 Found %d files. Generating real embeddings...\n", len(files))

	// Concurrency: Use a worker pool for file processing
	type task struct {
		path string
		idx  int
	}
	type result struct {
		indexed bool
		skipped bool
		err     string
	}

	taskChan := make(chan task, len(files))
	resultChan := make(chan result, len(files))
	
	numWorkers := runtime.NumCPU()
	if numWorkers > 8 {
		numWorkers = 8
	}
	var wg sync.WaitGroup

	for w := 0; w < numWorkers; w++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for t := range taskChan {
				relPath := config.GetRelativePath(t.path, cfg.ProjectRoot)
				hash, err := indexer.GetHash(t.path)
				if err != nil {
					resultChan <- result{err: fmt.Sprintf("failed to hash %s: %v", relPath, err)}
					continue
				}

				// Check if either old format (hash-0) or new format (hash-0-0) exists
				existing, _ := store.GetByID(ctx, fmt.Sprintf("%s-0", hash))
				if existing.ID == "" {
					existing, _ = store.GetByID(ctx, fmt.Sprintf("%s-0-0", hash))
				}

				if existing.ID != "" {
					fmt.Fprintf(os.Stderr, "[%d/%d] ⏩ Skipping (already indexed): %s\n", t.idx+1, len(files), relPath)
					resultChan <- result{skipped: true}
					continue
				}

				// Cleanup OLD hash versions of this same path if they exist
				fmt.Fprintf(os.Stderr, "[%d/%d] 📄 Indexing: %s\n", t.idx+1, len(files), relPath)
				store.DeleteByPath(ctx, relPath)
				
				content, err := os.ReadFile(t.path)
				if err != nil {
					resultChan <- result{err: fmt.Sprintf("failed to read %s: %v", relPath, err)}
					continue
				}
				
				chunks := indexer.CreateChunks(string(content), filepath.Ext(t.path))
				var records []db.Record

				for j, chunk := range chunks {
					embs, err := safeEmbed(ctx, embedder, chunk.Content)
					if err != nil {
						fmt.Fprintf(os.Stderr, "⚠️ Skipping unrecoverable problematic block in %s: %v\n", relPath, err)
						continue
					}
					
					for k, emb := range embs {
						records = append(records, db.Record{
							ID:        fmt.Sprintf("%s-%d-%d", hash, j, k),
							Content:   chunk.Content, // Note: In split cases, content should technically be split too, 
							                         // but for simplicity we keep chunk content for now or update it.
							Embedding: emb,
							Metadata: map[string]string{
								"path":          relPath,
								"hash":          hash,
								"symbols":       fmt.Sprintf("%v", chunk.Symbols),
								"relationships": fmt.Sprintf("%v", chunk.Relationships),
								"last_indexed":  time.Now().Format(time.RFC3339),
							},
						})
					}
				}
				if len(records) > 0 {
					err = store.Insert(ctx, records)
					if err != nil {
						resultChan <- result{err: fmt.Sprintf("failed to insert %s: %v", relPath, err)}
					} else {
						resultChan <- result{indexed: true}
					}
				} else {
					resultChan <- result{skipped: true}
				}
			}
		}()
	}

	for i, path := range files {
		taskChan <- task{path: path, idx: i}
	}
	close(taskChan)

	go func() {
		wg.Wait()
		close(resultChan)
	}()

	for r := range resultChan {
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

func indexSingleFile(ctx context.Context, path string, cfg *config.Config) (IndexSummary, error) {
	startTime := time.Now()
	summary := IndexSummary{Status: "completed", FilesProcessed: 1}
	
	store, err := getStore(ctx, cfg)
	if err != nil {
		return summary, err
	}
	embedder, err := embedding.NewEmbedder(cfg.ModelsDir)
	if err != nil {
		return summary, err
	}
	defer embedder.Close()

	relPath := config.GetRelativePath(path, cfg.ProjectRoot)
	hash, err := indexer.GetHash(path)
	if err != nil {
		return summary, err
	}

	// Always delete existing indexed versions of this path to ensure accuracy
	store.DeleteByPath(ctx, relPath)
	
	content, err := os.ReadFile(path)
	if err != nil {
		return summary, err
	}
	
	chunks := indexer.CreateChunks(string(content), filepath.Ext(path))
	var records []db.Record
	for j, chunk := range chunks {
		embs, err := safeEmbed(ctx, embedder, chunk.Content)
		if err != nil {
			fmt.Fprintf(os.Stderr, "⚠️ Skipping problematic block in %s: %v\n", relPath, err)
			summary.Errors = append(summary.Errors, fmt.Sprintf("%s: %v", relPath, err))
			continue
		}
		
		for k, emb := range embs {
			records = append(records, db.Record{
				ID:        fmt.Sprintf("%s-%d-%d", hash, j, k),
				Content:   chunk.Content,
				Embedding: emb,
				Metadata: map[string]string{
					"path":          relPath,
					"hash":          hash,
					"symbols":       fmt.Sprintf("%v", chunk.Symbols),
					"relationships": fmt.Sprintf("%v", chunk.Relationships),
					"last_indexed":  time.Now().Format(time.RFC3339),
				},
			})
		}
	}
	if len(records) > 0 {
		err = store.Insert(ctx, records)
		if err != nil {
			return summary, err
		}
		summary.FilesIndexed = 1
	} else {
		summary.FilesSkipped = 1
	}
	
	summary.DurationMs = time.Since(startTime).Milliseconds()
	return summary, nil
}

