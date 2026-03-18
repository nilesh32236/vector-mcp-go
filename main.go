package main

import (
	"context"
	"encoding/json"
	"flag"
	"fmt"
	"log/slog"
	"os"
	"os/signal"
	"path/filepath"
	"runtime"
	"strings"
	"sync"
	"syscall"
	"time"

	"vector-mcp-go/internal/config"
	"vector-mcp-go/internal/db"
	"vector-mcp-go/internal/embedding"
	"vector-mcp-go/internal/indexer"

	"github.com/fsnotify/fsnotify"
	"github.com/joho/godotenv"
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

const maxContextTokens = 10000

func estimateTokens(text string) int {
	return (len(strings.Fields(text)) * 4) / 3
}

var (
	dbMu             sync.RWMutex
	globalStore      *db.Store
	embedPool        *embedding.EmbedderPool
	monorepoResolver *indexer.WorkspaceResolver

	// Phase 6: Background Indexing
	indexQueue  = make(chan string, 100)
	progressMap sync.Map // path -> string status
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
	_ = godotenv.Load()
	if runtime.GOOS == "linux" {
		libPath := os.Getenv("ONNX_LIB_PATH")
		if libPath == "" {
			// 1. Try relative to CWD (for go run)
			cwd, _ := os.Getwd()
			libPath = filepath.Join(cwd, "lib", "libonnxruntime.so")

			if _, err := os.Stat(libPath); os.IsNotExist(err) {
				// 2. Try relative to executable
				execPath, _ := os.Executable()
				execDir := filepath.Dir(execPath)
				libPath = filepath.Join(execDir, "lib", "libonnxruntime.so")

				if _, err := os.Stat(libPath); os.IsNotExist(err) {
					// 3. Try standard local path
					home, _ := os.UserHomeDir()
					libPath = filepath.Join(home, ".local", "share", "vector-mcp-go", "lib", "libonnxruntime.so")
				}
			}
		}
		fmt.Fprintf(os.Stderr, "DEBUG: Using ONNX shared library path: %s\n", libPath)
		onnxruntime_go.SetSharedLibraryPath(libPath)
	}
	err := onnxruntime_go.InitializeEnvironment()
	if err != nil {
		fmt.Fprintf(os.Stderr, "Error initializing ONNX runtime: %v\n", err)
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

func startIndexingWorker(cfg *config.Config, logger *slog.Logger) {
	for path := range indexQueue {
		func() {
			defer func() {
				if r := recover(); r != nil {
					logger.Error("Indexing worker panicked", "path", path, "recover", r)
					progressMap.Store(path, "Failed (Panic)")
					if store, err := getStore(context.Background(), cfg); err == nil {
						store.SetStatus(context.Background(), path, "Failed (Panic)")
					}
				}
			}()

			logger.Info("Starting background indexing", "path", path)
			progressMap.Store(path, "Initializing...")
			if store, err := getStore(context.Background(), cfg); err == nil {
				store.SetStatus(context.Background(), path, "Initializing...")
			}

			targetCfg := &config.Config{
				ProjectRoot: path,
				DbPath:      cfg.DbPath,
				ModelsDir:   cfg.ModelsDir,
				Logger:      cfg.Logger,
			}

			summary, err := IndexFullCodebase(context.Background(), targetCfg, logger)
			if err != nil {
				logger.Error("Background indexing failed", "path", path, "error", err)
				progressMap.Store(path, fmt.Sprintf("Error: %v", err))
				if store, err := getStore(context.Background(), cfg); err == nil {
					store.SetStatus(context.Background(), path, fmt.Sprintf("Error: %v", err))
				}
				return
			}

			status := fmt.Sprintf("Completed: %d indexed, %d skipped", summary.FilesIndexed, summary.FilesSkipped)
			progressMap.Store(path, status)
			if store, err := getStore(context.Background(), cfg); err == nil {
				store.SetStatus(context.Background(), path, status)
			}
			logger.Info("Background indexing complete", "path", path, "summary", summary)
		}()
	}
}
func main() {
	dataDirFlag := flag.String("data-dir", "", "Base directory for DB and models (defaults to ~/.local/share/vector-mcp-go)")
	modelsDirFlag := flag.String("models-dir", "", "Specific directory for models (defaults to data-dir/models)")
	dbPathFlag := flag.String("db-path", "", "Specific path for the database (defaults to data-dir/lancedb)")
	indexFlag := flag.Bool("index", false, "Run full codebase indexing and exit")
	flag.Parse()

	cfg := config.LoadConfig(*dataDirFlag, *modelsDirFlag, *dbPathFlag)
	logger := cfg.Logger

	// Initialize monorepo resolver
	monorepoResolver = indexer.InitResolver(cfg.ProjectRoot)

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	// Ensure models exist
	_, err := embedding.EnsureModel(cfg.ModelsDir)
	if err != nil {
		logger.Error("Failed to ensure models exist", "error", err)
		os.Exit(1)
	}

	// Handle Graceful Shutdown
	sigChan := make(chan os.Signal, 1)
	signal.Notify(sigChan, syscall.SIGINT, syscall.SIGTERM)
	go func() {
		sig := <-sigChan
		logger.Info("Received signal, shutting down gracefully", "signal", sig)
		cancel()
		time.Sleep(500 * time.Millisecond)
		os.Exit(0)
	}()

	// Start Background Worker
	go startIndexingWorker(cfg, logger)

	// CLI Support for maintenance
	args := flag.Args()
	if len(args) > 0 {
		cmd := args[0]
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
			for i := 1; i < len(args); i++ {
				if args[i] == "-query" && i+1 < len(args) {
					query = args[i+1]
					i++
				} else if args[i] == "-topK" && i+1 < len(args) {
					fmt.Sscanf(args[i+1], "%d", &topK)
					i++
				}
			}
			if query == "" {
				fmt.Println("Usage: ./vector-mcp-go retrieve_context -query \"your query\" [-topK 5]")
				return
			}
			res, err := runRetrieveContext(ctx, store, query, topK, []string{cfg.ProjectRoot})
			if err != nil {
				fmt.Printf("Error: %v\n", err)
			} else {
				fmt.Println(res)
			}
			return
		}
	}

	// Initialize Embedder Pool for MCP mode
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

	go watchFiles(cfg, logger)

	s := server.NewMCPServer("vector-mcp-go", "1.0.0", server.WithLogging())

	// 0. ping
	s.AddTool(mcp.NewTool("ping", mcp.WithDescription("Check server connectivity")),
		func(ctx context.Context, request mcp.CallToolRequest) (*mcp.CallToolResult, error) {
			return mcp.NewToolResultText("pong"), nil
		})

	// 1. trigger_project_index
	s.AddTool(mcp.NewTool("trigger_project_index",
		mcp.WithDescription("Trigger a full background index of a project."),
		mcp.WithString("project_path", mcp.Description("Absolute path to the project root")),
	), func(ctx context.Context, request mcp.CallToolRequest) (*mcp.CallToolResult, error) {
		path := request.GetString("project_path", "")
		if path == "" {
			return mcp.NewToolResultError("project_path is required"), nil
		}
		indexQueue <- path
		return mcp.NewToolResultText(fmt.Sprintf("Initial indexing triggered in the background for %s.", path)), nil
	})

	// 2. store_context
	s.AddTool(mcp.NewTool("store_context",
		mcp.WithDescription("Store general project rules, architectural decisions, or shared context for other agents to read."),
		mcp.WithString("text", mcp.Description("The text context to store.")),
		mcp.WithString("project_id", mcp.Description("The project this context belongs to. Defaults to the current project.")),
	), func(ctx context.Context, request mcp.CallToolRequest) (*mcp.CallToolResult, error) {
		text := request.GetString("text", "")
		if text == "" {
			return mcp.NewToolResultError("text is required"), nil
		}
		projectID := request.GetString("project_id", cfg.ProjectRoot)

		emb, err := getEmbedding(ctx, text)
		if err != nil {
			return mcp.NewToolResultError(fmt.Sprintf("failed to generate embedding: %v", err)), nil
		}

		store, err := getStore(ctx, cfg)
		if err != nil {
			return mcp.NewToolResultError(err.Error()), nil
		}

		err = store.Insert(ctx, []db.Record{{
			ID:        fmt.Sprintf("context-%d", time.Now().UnixNano()),
			Content:   fmt.Sprintf("// Shared Context\n%s", text),
			Embedding: emb,
			Metadata: map[string]string{
				"project_id": projectID,
				"type":       "shared_knowledge",
			},
		}})
		if err != nil {
			return mcp.NewToolResultError(fmt.Sprintf("failed to store context: %v", err)), nil
		}

		return mcp.NewToolResultText("Context successfully stored in the global brain."), nil
	})

	// 3. get_related_context
	s.AddTool(mcp.NewTool("get_related_context",
		mcp.WithDescription("Retrieve context for a file and its local dependencies, optionally cross-referencing other projects."),
		mcp.WithString("filePath", mcp.Description("The relative path of the file to analyze")),
	), func(ctx context.Context, request mcp.CallToolRequest) (*mcp.CallToolResult, error) {
		filePath := request.GetString("filePath", "")
		store, err := getStore(ctx, cfg)
		if err != nil {
			return mcp.NewToolResultError(err.Error()), nil
		}

		pids := []string{cfg.ProjectRoot}
		crossProjs := request.GetStringSlice("cross_reference_projects", nil)
		if len(crossProjs) > 0 {
			pids = append(pids, crossProjs...)
		}

		records, err := store.GetByPath(ctx, filePath, cfg.ProjectRoot)
		if err != nil || len(records) == 0 {
			return mcp.NewToolResultText(fmt.Sprintf("No context found for file: %s", filePath)), nil
		}

		uniqueDeps := make(map[string]string)
		allImportStrings := make(map[string]bool)
		allSymbols := make(map[string]bool)
		for _, r := range records {
			var deps []string
			if relStr := r.Metadata["relationships"]; relStr != "" {
				if err := json.Unmarshal([]byte(relStr), &deps); err == nil {
					for _, d := range deps {
						allImportStrings[d] = true
						if strings.HasPrefix(d, "./") || strings.HasPrefix(d, "../") {
							uniqueDeps[d] = filepath.Join(filepath.Dir(filePath), d)
						} else if physPath, ok := monorepoResolver.Resolve(d); ok {
							uniqueDeps[d] = physPath
						}
					}
				}
			}
			var symbols []string
			if symStr := r.Metadata["symbols"]; symStr != "" {
				if err := json.Unmarshal([]byte(symStr), &symbols); err == nil {
					for _, s := range symbols {
						allSymbols[s] = true
					}
				}
			}
		}

		var out strings.Builder
		out.WriteString("<context>\n")
		currentTokenCount := 0
		var omittedFiles []string

		out.WriteString(fmt.Sprintf("  <file path=\"%s\">\n    <metadata>\n", filePath))
		
		// Dependencies
		var depList []string
		for d := range allImportStrings { depList = append(depList, d) }
		depListJSON, _ := json.Marshal(depList)
		out.WriteString(fmt.Sprintf("      <dependencies>%s</dependencies>\n", string(depListJSON)))
		
		// Symbols
		var symList []string
		for s := range allSymbols { symList = append(symList, s) }
		symListJSON, _ := json.Marshal(symList)
		out.WriteString(fmt.Sprintf("      <symbols>%s</symbols>\n", string(symListJSON)))
		
		out.WriteString("    </metadata>\n")
		
		for _, r := range records {
			tokens := estimateTokens(r.Content)
			if currentTokenCount+tokens > maxContextTokens {
				omittedFiles = append(omittedFiles, filePath)
				continue
			}
			out.WriteString("    <code_chunk>\n" + r.Content + "\n    </code_chunk>\n")
			currentTokenCount += tokens
		}
		out.WriteString("  </file>\n")

		if len(uniqueDeps) > 0 {
			allRecords, _ := store.GetAllRecords(ctx)
			for importPath, physPath := range uniqueDeps {
				matchPath := strings.TrimSuffix(physPath, filepath.Ext(physPath))
				out.WriteString(fmt.Sprintf("  <file path=\"%s\" resolved_from=\"%s\">\n", physPath, importPath))
				foundAny := false
				
				// Collect file metadata first
				fileDeps := make(map[string]bool)
				fileSymbols := make(map[string]bool)
				var fileChunks []db.Record

				for _, dr := range allRecords {
					projMatch := false
					for _, pid := range pids {
						if dr.Metadata["project_id"] == pid {
							projMatch = true
							break
						}
					}
					if projMatch && (dr.Metadata["path"] == physPath || strings.Contains(dr.Metadata["path"], matchPath)) {
						fileChunks = append(fileChunks, dr)
						var dps []string
						if err := json.Unmarshal([]byte(dr.Metadata["relationships"]), &dps); err == nil {
							for _, d := range dps { fileDeps[d] = true }
						}
						var sys []string
						if err := json.Unmarshal([]byte(dr.Metadata["symbols"]), &sys); err == nil {
							for _, s := range sys { fileSymbols[s] = true }
						}
					}
				}

				if len(fileChunks) > 0 {
					out.WriteString("    <metadata>\n")
					var fdList []string
					for d := range fileDeps { fdList = append(fdList, d) }
					fdJSON, _ := json.Marshal(fdList)
					out.WriteString(fmt.Sprintf("      <dependencies>%s</dependencies>\n", string(fdJSON)))
					
					var fsList []string
					for s := range fileSymbols { fsList = append(fsList, s) }
					fsJSON, _ := json.Marshal(fsList)
					out.WriteString(fmt.Sprintf("      <symbols>%s</symbols>\n", string(fsJSON)))
					out.WriteString("    </metadata>\n")

					for _, dr := range fileChunks {
						tokens := estimateTokens(dr.Content)
						if currentTokenCount+tokens > maxContextTokens {
							omittedFiles = append(omittedFiles, dr.Metadata["path"])
							continue
						}
						out.WriteString("    <code_chunk>\n" + dr.Content + "\n    </code_chunk>\n")
						currentTokenCount += tokens
						foundAny = true
					}
				}

				if !foundAny && currentTokenCount < maxContextTokens {
					out.WriteString("    <error>No indexed chunks found.</error>\n")
				}
				out.WriteString("  </file>\n")
			}
		}

		if len(omittedFiles) > 0 {
			out.WriteString("  <omitted_matches>\n    <files>")
			omittedJSON, _ := json.Marshal(omittedFiles)
			out.WriteString(string(omittedJSON) + "</files>\n  </omitted_matches>\n")
		}
		out.WriteString("</context>")
		return mcp.NewToolResultText(out.String()), nil
	})

	// 3. find_duplicate_code
	s.AddTool(mcp.NewTool("find_duplicate_code",
		mcp.WithDescription("Scans a specific path to find duplicated logic across namespaces."),
		mcp.WithString("target_path", mcp.Description("The relative path to check")),
	), func(ctx context.Context, request mcp.CallToolRequest) (*mcp.CallToolResult, error) {
		targetPath := request.GetString("target_path", "")
		pids := []string{cfg.ProjectRoot}
		crossProjs := request.GetStringSlice("cross_reference_projects", nil)
		if len(crossProjs) > 0 {
			pids = append(pids, crossProjs...)
		}

		store, _ := getStore(ctx, cfg)
		allRecords, _ := store.GetAllRecords(ctx)
		var targetChunks []db.Record
		for _, r := range allRecords {
			if r.Metadata["project_id"] == cfg.ProjectRoot && strings.HasPrefix(r.Metadata["path"], targetPath) {
				targetChunks = append(targetChunks, r)
			}
		}

		var out strings.Builder
		out.WriteString(fmt.Sprintf("<duplicate_analysis target=\"%s\">\n", targetPath))
		found := false
		for _, tc := range targetChunks {
			emb, _ := getEmbedding(ctx, tc.Content)
			matches, _, _ := store.SearchWithScore(ctx, emb, 5, pids)
			for _, m := range matches {
				if m.Similarity > 0.93 && (m.Metadata["path"] != tc.Metadata["path"] || m.Metadata["project_id"] != tc.Metadata["project_id"]) {
					out.WriteString(fmt.Sprintf("  <finding>\n    <original file=\"%s\">%s</original>\n", tc.Metadata["path"], tc.Metadata["path"]))
					out.WriteString(fmt.Sprintf("    <match file=\"%s\" project=\"%s\" score=\"%f\">%s</match>\n  </finding>\n", m.Metadata["path"], m.Metadata["project_id"], m.Similarity, m.Metadata["path"]))
					found = true
				}
			}
		}
		if !found {
			out.WriteString("  <summary>No duplicates found.</summary>\n")
		}
		out.WriteString("</duplicate_analysis>")
		return mcp.NewToolResultText(out.String()), nil
	})

	// 4. delete_context
	s.AddTool(mcp.NewTool("delete_context",
		mcp.WithDescription("Delete specific shared memory context, or completely wipe a project's vector index."),
		mcp.WithString("target_path", mcp.Description("The exact file path, context ID, or 'ALL' to clear the whole project.")),
		mcp.WithString("project_id", mcp.Description("The project ID to target. Defaults to the current project.")),
	), func(ctx context.Context, request mcp.CallToolRequest) (*mcp.CallToolResult, error) {
		targetPath := request.GetString("target_path", "")
		if targetPath == "" {
			return mcp.NewToolResultError("target_path is required"), nil
		}
		projectID := request.GetString("project_id", cfg.ProjectRoot)

		store, err := getStore(ctx, cfg)
		if err != nil {
			return mcp.NewToolResultError(err.Error()), nil
		}

		if targetPath == "ALL" {
			err = store.ClearProject(ctx, projectID)
			if err != nil {
				return mcp.NewToolResultError(fmt.Sprintf("failed to clear project: %v", err)), nil
			}
			return mcp.NewToolResultText(fmt.Sprintf("Successfully wiped all vectors for project: %s", projectID)), nil
		}

		err = store.DeleteByPath(ctx, targetPath, projectID)
		if err != nil {
			return mcp.NewToolResultError(fmt.Sprintf("failed to delete context: %v", err)), nil
		}

		return mcp.NewToolResultText(fmt.Sprintf("Deleted context/file: %s from project: %s", targetPath, projectID)), nil
	})

	// 5. index_status
	s.AddTool(mcp.NewTool("index_status", mcp.WithDescription("Check index status and background progress.")),
		func(ctx context.Context, request mcp.CallToolRequest) (*mcp.CallToolResult, error) {
			store, _ := getStore(ctx, cfg)
			res, _ := runStatus(ctx, store, cfg)

			bgStatus := "\n🛰️ Background Tasks:\n"
			hasBg := false
			progressMap.Range(func(key, value interface{}) bool {
				bgStatus += fmt.Sprintf("- %s: %s\n", key, value)
				hasBg = true
				return true
			})
			if !hasBg {
				bgStatus += "- No active background indexing.\n"
			}
			return mcp.NewToolResultText(res + bgStatus), nil
		})

	// 6. get_codebase_skeleton
	s.AddTool(mcp.NewTool("get_codebase_skeleton",
		mcp.WithDescription("Returns a topological tree map of the directory structure to help understand the project layout."),
		mcp.WithString("project_path", mcp.Description("Absolute path to the project root (optional, defaults to current project root).")),
	), func(ctx context.Context, request mcp.CallToolRequest) (*mcp.CallToolResult, error) {
		targetPath := request.GetString("project_path", cfg.ProjectRoot)
		if targetPath == "" {
			targetPath = cfg.ProjectRoot
		}

		var out strings.Builder
		out.WriteString(fmt.Sprintf("%s\n", filepath.Base(targetPath)))

		itemCount := 0
		maxItems := 1000
		maxDepth := 5
		truncated := false

		err := filepath.WalkDir(targetPath, func(path string, d os.DirEntry, err error) error {
			if err != nil {
				return nil
			}

			if path == targetPath {
				return nil
			}

			relPath, err := filepath.Rel(targetPath, path)
			if err != nil {
				return nil
			}

			depth := strings.Count(relPath, string(os.PathSeparator)) + 1

			if d.IsDir() {
				if indexer.IsIgnoredDir(d.Name()) {
					return filepath.SkipDir
				}
				if depth > maxDepth {
					return filepath.SkipDir
				}
			} else {
				if indexer.IsIgnoredFile(d.Name()) {
					return nil
				}
				if depth > maxDepth {
					return nil
				}
			}

			if itemCount >= maxItems {
				truncated = true
				return filepath.SkipDir
			}
			itemCount++

			indent := strings.Repeat("│   ", depth-1)
			out.WriteString(fmt.Sprintf("%s├── %s\n", indent, d.Name()))

			return nil
		})

		if err != nil {
			return mcp.NewToolResultError(fmt.Sprintf("Error walking directory: %v", err)), nil
		}

		if truncated {
			out.WriteString("... (tree truncated for size limit)\n")
		}

		return mcp.NewToolResultText(fmt.Sprintf("<codebase_skeleton>\n%s</codebase_skeleton>", out.String())), nil
	})
	logger.Info("MCP Server listening on stdio...")
	if err := server.ServeStdio(s); err != nil {
		logger.Error("Server error", "error", err)
	}
}

func runRetrieveContext(ctx context.Context, store *db.Store, query string, topK int, projectIDs []string) (string, error) {
	emb, err := getEmbedding(ctx, query)
	if err != nil {
		return "", err
	}
	results, err := store.Search(ctx, emb, topK, projectIDs)
	if err != nil {
		return "", err
	}
	var out string
	for _, r := range results {
		out += fmt.Sprintf("--- %s (%s) ---\n%s\n", r.Metadata["path"], r.Metadata["project_id"], r.Content)
	}
	return out, nil
}

func runHealth(ctx context.Context, store *db.Store) (string, error) {
	allRecords, _ := store.GetAllRecords(ctx)
	var out string = "📊 Project Health Overview:\n"
	count := 0
	for _, r := range allRecords {
		if r.Metadata["type"] == "task" {
			out += fmt.Sprintf("- [%s] %s: %s\n", r.Metadata["status"], r.Metadata["taskId"], r.Metadata["notes"])
			count++
		}
	}
	if count == 0 {
		return "No tasks found.", nil
	}
	return out, nil
}

func runStatus(ctx context.Context, store *db.Store, cfg *config.Config) (string, error) {
	diskFiles, _ := indexer.ScanFiles(cfg.ProjectRoot)
	dbMapping, _ := store.GetPathHashMapping(ctx, cfg.ProjectRoot)
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
	var deleted []string
	for dbPath := range dbMapping {
		if !diskPaths[dbPath] {
			deleted = append(deleted, dbPath)
		}
	}

	var out strings.Builder
	out.WriteString(fmt.Sprintf("🔍 Index Status for %s:\n", cfg.ProjectRoot))
	out.WriteString(fmt.Sprintf("✅ Fully Indexed: %d\n🔄 Modified: %d\n📂 Missing: %d\n🗑️ Deleted: %d\n", len(indexed), len(updated), len(missing), len(deleted)))

	if len(missing) > 0 {
		out.WriteString("\n📂 Missing Files (Next to index):\n")
		for i, f := range missing {
			if i >= 10 {
				out.WriteString("  ... and more\n")
				break
			}
			out.WriteString(fmt.Sprintf("  - %s\n", f))
		}
	}

	if len(updated) > 0 {
		out.WriteString("\n🔄 Modified Files (Need update):\n")
		for i, f := range updated {
			if i >= 10 {
				out.WriteString("  ... and more\n")
				break
			}
			out.WriteString(fmt.Sprintf("  - %s\n", f))
		}
	}

	status, _ := store.GetStatus(ctx, cfg.ProjectRoot)
	if status != "" {
		out.WriteString(fmt.Sprintf("\n🛰️ Background Status (from DB): %s\n", status))
	}

	return out.String(), nil
}


func watchFiles(cfg *config.Config, logger *slog.Logger) {
	watcher, _ := fsnotify.NewWatcher()
	defer watcher.Close()
	watchRecursive := func(root string) {
		filepath.WalkDir(root, func(path string, d os.DirEntry, err error) error {
			if d != nil && d.IsDir() && !indexer.IsIgnoredDir(d.Name()) {
				watcher.Add(path)
			}
			return nil
		})
	}
	watchRecursive(cfg.ProjectRoot)
	for {
		select {
		case event, ok := <-watcher.Events:
			if !ok {
				return
			}
			if event.Has(fsnotify.Create) {
				info, _ := os.Stat(event.Name)
				if info != nil && info.IsDir() && !indexer.IsIgnoredDir(info.Name()) {
					watcher.Add(event.Name)
				}
			}
			if event.Has(fsnotify.Write) {
				ext := filepath.Ext(event.Name)
				if ext == ".go" || ext == ".ts" || ext == ".tsx" || ext == ".js" || ext == ".jsx" || ext == ".md" {
					indexSingleFile(context.Background(), event.Name, cfg, logger)
				}
			}
			if event.Has(fsnotify.Remove) || event.Has(fsnotify.Rename) {
				relPath := config.GetRelativePath(event.Name, cfg.ProjectRoot)
				store, err := getStore(context.Background(), cfg)
				if err == nil {
					store.DeleteByPath(context.Background(), relPath, cfg.ProjectRoot)
					logger.Info("File removed from vector index", "path", relPath)
				}
			}
		case <-time.After(1 * time.Hour): // Keep alive
		}
	}
}

func IndexFullCodebase(ctx context.Context, cfg *config.Config, logger *slog.Logger) (IndexSummary, error) {
	summary := IndexSummary{Status: "completed"}
	store, _ := getStore(ctx, cfg)
	
	store.SetStatus(ctx, cfg.ProjectRoot, "Scanning files and cleaning index...")
	
	files, err := indexer.ScanFiles(cfg.ProjectRoot)
	if err != nil {
		return summary, err
	}
	summary.FilesProcessed = len(files)

	hashMapping, _ := store.GetPathHashMapping(ctx, cfg.ProjectRoot)
	var toIndex []string
	for _, path := range files {
		relPath := config.GetRelativePath(path, cfg.ProjectRoot)
		currentHash, _ := indexer.GetHash(path)
		if existingHash, ok := hashMapping[relPath]; ok && existingHash == currentHash {
			summary.FilesSkipped++
			continue
		}
		toIndex = append(toIndex, path)
	}

	for dbPath := range hashMapping {
		found := false
		for _, absPath := range files {
			if config.GetRelativePath(absPath, cfg.ProjectRoot) == dbPath {
				found = true
				break
			}
		}
		if !found {
			store.DeleteByPath(ctx, dbPath, cfg.ProjectRoot)
		}
	}

	if len(toIndex) == 0 {
		store.SetStatus(ctx, cfg.ProjectRoot, fmt.Sprintf("Completed: %d files skipped (up to date)", summary.FilesSkipped))
		return summary, nil
	}

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

	go func() {
		for _, path := range toIndex {
			tasks <- path
		}
		close(tasks)
	}()

	go func() {
		wg.Wait()
		close(results)
	}()

	var batch []db.Record
	processed := 0
	totalToIndex := len(toIndex)
	for r := range results {
		processed++
		if r.err != "" {
			summary.Errors = append(summary.Errors, r.err)
		}
		if r.indexed {
			summary.FilesIndexed++
			batch = append(batch, r.records...)
		}
		if r.skipped {
			summary.FilesSkipped++
		}

		if len(batch) >= 50 {
			logger.Info("Inserting batch of records", "count", len(batch))
			store.Insert(ctx, batch)
			batch = batch[:0]
		}

		// Real-time progress update
		progress := float64(processed) / float64(totalToIndex) * 100
		status := fmt.Sprintf("Indexing: %.1f%% (%d/%d) - Current: %s", progress, processed, totalToIndex, r.relPath)
		progressMap.Store(cfg.ProjectRoot, status)
		store.SetStatus(ctx, cfg.ProjectRoot, status)
	}

	if len(batch) > 0 {
		store.Insert(ctx, batch)
	}

	return summary, nil
}


type result struct {
	indexed bool
	skipped bool
	err     string
	relPath string
	records []db.Record
}

func processFile(ctx context.Context, path string, cfg *config.Config, store *db.Store) result {
	defer func() {
		if r := recover(); r != nil {
			slog.Error("Panic processing file", "path", path, "recover", r)
		}
	}()

	relPath := config.GetRelativePath(path, cfg.ProjectRoot)
	currentHash, err := indexer.GetHash(path)
	if err != nil {
		return result{err: err.Error(), relPath: relPath}
	}

	existingHash, _ := store.GetFileHash(ctx, relPath, cfg.ProjectRoot)
	if existingHash == currentHash {
		return result{skipped: true, relPath: relPath}
	}

	content, err := os.ReadFile(path)
	if err != nil {
		return result{err: err.Error(), relPath: relPath}
	}

	chunks := indexer.CreateChunks(string(content), relPath)
	var records []db.Record
	for _, chunk := range chunks {
		emb, err := getEmbedding(ctx, chunk.Content)
		if err != nil {
			continue
		}
		relJSON, _ := json.Marshal(chunk.Relationships)
		symJSON, _ := json.Marshal(chunk.Symbols)
		records = append(records, db.Record{
			ID:      fmt.Sprintf("%s-%s-%d", cfg.ProjectRoot, relPath, time.Now().UnixNano()),
			Content: chunk.Content,
			Embedding: emb,
			Metadata: map[string]string{
				"path":          relPath,
				"project_id":    cfg.ProjectRoot,
				"hash":          currentHash,
				"relationships": string(relJSON),
				"symbols":       string(symJSON),
			},
		})
	}

	if len(records) > 0 {
		store.DeleteByPath(ctx, relPath, cfg.ProjectRoot)
		return result{indexed: true, records: records, relPath: relPath}
	}
	return result{relPath: relPath}
}


func indexSingleFile(ctx context.Context, path string, cfg *config.Config, logger *slog.Logger) (IndexSummary, error) {
	store, err := getStore(ctx, cfg)
	if err != nil {
		return IndexSummary{}, err
	}
	res := processFile(ctx, path, cfg, store)
	if res.err != "" {
		return IndexSummary{Status: "error"}, nil
	}
	if res.indexed {
		store.Insert(ctx, res.records)
	}
	return IndexSummary{Status: "completed", FilesIndexed: 1}, nil
}

func runStandaloneIndex(cfg *config.Config, logger *slog.Logger) {
	summary, _ := IndexFullCodebase(context.Background(), cfg, logger)
	logger.Info("Standalone index complete", "summary", summary)
}
