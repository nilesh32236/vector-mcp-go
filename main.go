package main

import (
	"context"
	"flag"
	"fmt"
	"os"
	"os/signal"
	"sync"
	"syscall"
	"time"

	"vector-mcp-go/internal/config"
	"vector-mcp-go/internal/db"
	"vector-mcp-go/internal/embedding"
	"vector-mcp-go/internal/indexer"
	"vector-mcp-go/internal/mcp"
	"vector-mcp-go/internal/onnx"
	"vector-mcp-go/internal/watcher"
	"vector-mcp-go/internal/worker"
)

var (
	dbMu        sync.RWMutex
	globalStore *db.Store
	embedPool   *embedding.EmbedderPool
	indexQueue  = make(chan string, 100)
	progressMap sync.Map
	resetChan   = make(chan string, 1)
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

type poolEmbedder struct {
	pool *embedding.EmbedderPool
}

func (pe *poolEmbedder) Embed(ctx context.Context, text string) ([]float32, error) {
	e, err := pe.pool.Get(ctx)
	if err != nil {
		return nil, err
	}
	defer pe.pool.Put(e)
	return e.Embed(ctx, text)
}

func main() {
	if err := onnx.Init(); err != nil {
		fmt.Fprintf(os.Stderr, "Failed to initialize ONNX: %v\n", err)
		os.Exit(1)
	}

	dataDirFlag := flag.String("data-dir", "", "Base directory for DB and models")
	modelsDirFlag := flag.String("models-dir", "", "Specific directory for models")
	dbPathFlag := flag.String("db-path", "", "Specific path for the database")
	indexFlag := flag.Bool("index", false, "Run full codebase indexing and exit")
	flag.Parse()

	cfg := config.LoadConfig(*dataDirFlag, *modelsDirFlag, *dbPathFlag)
	logger := cfg.Logger

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	if _, err := embedding.EnsureModel(cfg.ModelsDir); err != nil {
		logger.Error("Failed to ensure models exist", "error", err)
		os.Exit(1)
	}

	// Graceful shutdown
	sigChan := make(chan os.Signal, 1)
	signal.Notify(sigChan, syscall.SIGINT, syscall.SIGTERM)
	go func() {
		sig := <-sigChan
		logger.Info("Received signal, shutting down", "signal", sig)
		cancel()
		time.Sleep(500 * time.Millisecond)
		os.Exit(0)
	}()

	// Initialize dependencies
	pool, err := embedding.NewEmbedderPool(ctx, cfg.ModelsDir, 2)
	if err != nil {
		logger.Error("Failed to initialize embedder pool", "error", err)
		os.Exit(1)
	}
	embedPool = pool
	defer embedPool.Close()

	embedder := &poolEmbedder{pool: embedPool}
	storeGetter := func(ctx context.Context) (*db.Store, error) {
		return getStore(ctx, cfg)
	}

	// Initialize worker
	idxWorker := worker.NewIndexWorker(cfg, logger, indexQueue, &progressMap, storeGetter, embedder)
	go idxWorker.Start(ctx)

	// CLI logic
	args := flag.Args()
	if len(args) > 0 {
		cmd := args[0]
		if cmd == "status" || cmd == "health" {
			_, err := getStore(ctx, cfg)
			if err != nil {
				logger.Error("DB connection failed", "error", err)
				os.Exit(1)
			}
			if cmd == "status" {
				// We can't easily call runStatus from mcp package here without making it exported or duplication.
				// For now, let's just use the server's status logic if we had it, or just exit.
				// Since we want to keep main.go thin, we might want a CLI package too later.
				fmt.Println("CLI status/health not yet refactored into modular CLI package.")
			}
			return
		}
	}

	if *indexFlag {
		store, _ := getStore(ctx, cfg)
		summary, _ := indexer.IndexFullCodebase(ctx, cfg, store, embedder, &progressMap, logger)
		logger.Info("Standalone index complete", "summary", summary)
		return
	}

	// Start file watcher
	fw, err := watcher.NewFileWatcher(cfg, logger, resetChan, storeGetter, embedder)
	if err != nil {
		logger.Error("Failed to initialize file watcher", "error", err)
		os.Exit(1)
	}
	go fw.Start(ctx)

	// Start MCP server
	resolver := indexer.InitResolver(cfg.ProjectRoot)
	srv := mcp.NewServer(cfg, logger, storeGetter, embedder, indexQueue, &progressMap, resetChan, resolver)
	if err := srv.Serve(); err != nil {
		logger.Error("Server error", "error", err)
		os.Exit(1)
	}
}
