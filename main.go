package main

import (
	"context"
	"flag"
	"fmt"
	"os"
	"os/signal"
	"path/filepath"
	"sync"
	"syscall"
	"time"

	"github.com/nilesh32236/vector-mcp-go/internal/api"
	"github.com/nilesh32236/vector-mcp-go/internal/config"
	"github.com/nilesh32236/vector-mcp-go/internal/daemon"
	"github.com/nilesh32236/vector-mcp-go/internal/db"
	"github.com/nilesh32236/vector-mcp-go/internal/embedding"
	"github.com/nilesh32236/vector-mcp-go/internal/indexer"
	"github.com/nilesh32236/vector-mcp-go/internal/mcp"
	"github.com/nilesh32236/vector-mcp-go/internal/onnx"
	"github.com/nilesh32236/vector-mcp-go/internal/watcher"
	"github.com/nilesh32236/vector-mcp-go/internal/worker"
)

var (
	dbMu        sync.RWMutex
	globalStore *db.Store
	embedPool   *embedding.EmbedderPool
	indexQueue  = make(chan string, 100)
	progressMap sync.Map
	resetChan   = make(chan string, 1)
)

func getStore(ctx context.Context, cfg *config.Config, forceRefresh bool) (*db.Store, error) {
	dbMu.Lock()
	defer dbMu.Unlock()
	if globalStore != nil && !forceRefresh {
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
	dataDirFlag := flag.String("data-dir", "", "Base directory for DB and models")
	modelsDirFlag := flag.String("models-dir", "", "Specific directory for models")
	dbPathFlag := flag.String("db-path", "", "Specific path for the database")
	indexFlag := flag.Bool("index", false, "Run full codebase indexing and exit")
	flag.Parse()

	cfg := config.LoadConfig(*dataDirFlag, *modelsDirFlag, *dbPathFlag)
	logger := cfg.Logger

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	// 1. Master/Slave Detection using Unix Socket
	socketPath := filepath.Join(cfg.DataDir, "daemon.sock")
	var isMaster bool
	var daemonClient *daemon.Client
	var embedder indexer.Embedder

	// Try to listen on the socket to see if we are the master
	masterServer, err := daemon.StartMasterServer(socketPath, nil, indexQueue)
	if err == nil {
		isMaster = true
		logger.Info("Starting as MASTER instance", "socket", socketPath)
		defer masterServer.Close()
	} else {
		isMaster = false
		logger.Info("Starting as SLAVE instance (master already running)", "socket", socketPath)
		daemonClient = daemon.NewClient(socketPath)
		embedder = daemon.NewRemoteEmbedder(socketPath)
		cfg.DisableWatcher = true
	}

	// 2. Conditional Initialization
	if isMaster {
		if err := onnx.Init(); err != nil {
			logger.Error("Failed to initialize ONNX", "error", err)
			os.Exit(1)
		}

		if _, err := embedding.EnsureModel(cfg.ModelsDir); err != nil {
			logger.Error("Failed to ensure models exist", "error", err)
			os.Exit(1)
		}

		pool, err := embedding.NewEmbedderPool(ctx, cfg.ModelsDir, 2)
		if err != nil {
			logger.Error("Failed to initialize embedder pool", "error", err)
			os.Exit(1)
		}
		embedPool = pool
		// We don't close embedPool here because we need it for the daemon and server.
		// It will be closed on process exit.

		realEmbedder := &poolEmbedder{pool: embedPool}
		embedder = realEmbedder

		// Update daemon with real embedder
		masterServer.UpdateEmbedder(realEmbedder)
	}

	// Initialize dependencies
	storeGetter := func(ctx context.Context) (*db.Store, error) {
		return getStore(ctx, cfg, false)
	}
	freshStoreGetter := func(ctx context.Context) (*db.Store, error) {
		return getStore(ctx, cfg, true)
	}

	// Start API server
	apiSrv := api.NewServer(cfg, storeGetter, embedder)
	go func() {
		if err := apiSrv.Start(); err != nil {
			logger.Error("API server error", "error", err)
		}
	}()

	// Graceful shutdown
	sigChan := make(chan os.Signal, 1)
	signal.Notify(sigChan, syscall.SIGINT, syscall.SIGTERM)
	go func() {
		sig := <-sigChan
		logger.Info("Received signal, shutting down", "signal", sig)

		shutdownCtx, shutdownCancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer shutdownCancel()

		if err := apiSrv.Shutdown(shutdownCtx); err != nil {
			logger.Error("API server shutdown error", "error", err)
		}

		cancel()
		if isMaster && masterServer != nil {
			masterServer.Close()
		}
		time.Sleep(500 * time.Millisecond)
		os.Exit(0)
	}()

	// Initialize worker (only Master processes the queue)
	if isMaster {
		idxWorker := worker.NewIndexWorker(cfg, logger, indexQueue, &progressMap, storeGetter, embedder)
		go idxWorker.Start(ctx)
	}

	// CLI logic
	args := flag.Args()
	if len(args) > 0 {
		cmd := args[0]
		if cmd == "status" || cmd == "health" {
			_, err := getStore(ctx, cfg, false)
			if err != nil {
				logger.Error("DB connection failed", "error", err)
				os.Exit(1)
			}
			if cmd == "status" {
				fmt.Println("CLI status/health not yet refactored into modular CLI package.")
			}
			return
		}
	}

	if *indexFlag {
		if !isMaster {
			logger.Error("Standalone indexing can only be run when no other instance is active (Master only)")
			os.Exit(1)
		}
		store, _ := getStore(ctx, cfg, false)
		summary, _ := indexer.IndexFullCodebase(ctx, cfg, store, embedder, &progressMap, logger)
		logger.Info("Standalone index complete", "summary", summary)
		return
	}

	// Start file watcher
	if !cfg.DisableWatcher && isMaster {
		fw, err := watcher.NewFileWatcher(cfg, logger, resetChan, storeGetter, embedder)
		if err != nil {
			logger.Error("Failed to initialize file watcher", "error", err)
			os.Exit(1)
		}
		go fw.Start(ctx)
	} else if cfg.DisableWatcher {
		logger.Info("File watcher is disabled (Slave instance or explicit config)")
	}

	// Start MCP server
	resolver := indexer.InitResolver(cfg.ProjectRoot)
	srv := mcp.NewServer(cfg, logger, storeGetter, freshStoreGetter, embedder, indexQueue, daemonClient, &progressMap, resetChan, resolver)
	if err := srv.Serve(); err != nil {
		logger.Error("Server error", "error", err)
		os.Exit(1)
	}
}
