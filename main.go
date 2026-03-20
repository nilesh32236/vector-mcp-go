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

func getStore(ctx context.Context, cfg *config.Config, forceRefresh bool, isMaster bool, socketPath string) (*db.Store, error) {
	if !isMaster {
		// Slave instances always use RemoteStore, no local connection.
		return nil, nil // We'll handle RemoteStore separately in srv initialization or wrap it.
	}

	dbMu.Lock()
	defer dbMu.Unlock()
	if globalStore != nil && !forceRefresh {
		return globalStore, nil
	}
	store, err := db.Connect(ctx, cfg.DbPath, "project_context", cfg.Dimension)
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
	daemonFlag := flag.Bool("daemon", false, "Run as background daemon (master worker) without MCP stdio server")
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
	var masterServer *daemon.MasterServer
	var err error

	// Try to listen on the socket to see if we are the master
	// Pass nil for store initially, we'll update it after connecting.
	masterServer, err = daemon.StartMasterServer(socketPath, nil, indexQueue, nil, &progressMap)
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

	// 2. Master-only Initialization (Heavy RAM usage here)
	if isMaster {
		if err := onnx.Init(); err != nil {
			logger.Error("Failed to initialize ONNX", "error", err)
			os.Exit(1)
		}

		mc, err := embedding.EnsureModel(cfg.ModelsDir, cfg.ModelName)
		if err != nil {
			logger.Error("Failed to ensure models exist", "error", err)
			os.Exit(1)
		}
		cfg.Dimension = mc.Dimension

		pool, err := embedding.NewEmbedderPool(ctx, cfg.ModelsDir, cfg.EmbedderPoolSize, mc)
		if err != nil {
			logger.Error("Failed to initialize embedder pool", "error", err)
			os.Exit(1)
		}
		embedPool = pool

		realEmbedder := &poolEmbedder{pool: embedPool}
		embedder = realEmbedder

		// Initialize store for Master
		store, err := getStore(ctx, cfg, false, true, socketPath)
		if err != nil {
			logger.Error("Failed to initialize store", "error", err)
			os.Exit(1)
		}

		// Update daemon with real embedder and store
		masterServer.UpdateEmbedder(realEmbedder)
		masterServer.UpdateStore(store)
	}

	// 3. Define store getters that handle local/remote transparency
	storeGetter := func(ctx context.Context) (*db.Store, error) {
		if isMaster {
			return getStore(ctx, cfg, false, true, socketPath)
		}
		// This is a bit of a hack because srv expects *db.Store,
		// but we'll modify internal/mcp/server.go to handle indexer.Store interface or similar if needed.
		// For now, we'll keep it as is and see if we can cast or change the type.
		return nil, fmt.Errorf("slave instance cannot use local store getter")
	}

	// Start MCP server
	resolver := indexer.InitResolver(cfg.ProjectRoot)
	// We need to pass the remote store to the MCP server if we are a slave.
	srv := mcp.NewServer(cfg, logger, storeGetter, nil, embedder, indexQueue, daemonClient, &progressMap, resetChan, resolver)
	if !isMaster {
		remoteStore := daemon.NewRemoteStore(socketPath)
		srv.WithRemoteStore(remoteStore)
	}

	// Start API server (Master only, or Slaves can proxy if needed, but usually MCP is the main interface)
	if isMaster {
		apiSrv := api.NewServer(cfg, storeGetter, embedder, srv.MCPServer)
		go func() {
			if err := apiSrv.Start(); err != nil {
				logger.Error("API server error", "error", err)
			}
		}()
	}

	// Graceful shutdown
	sigChan := make(chan os.Signal, 1)
	signal.Notify(sigChan, syscall.SIGINT, syscall.SIGTERM)
	go func() {
		sig := <-sigChan
		logger.Info("Received signal, shutting down", "signal", sig)
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

	// Standalone indexing logic
	if *indexFlag {
		if !isMaster {
			logger.Error("Standalone indexing can only be run when no other instance is active (Master only)")
			os.Exit(1)
		}
		store, _ := getStore(ctx, cfg, false, true, socketPath)
		summary, _ := indexer.IndexFullCodebase(ctx, cfg, store, embedder, &progressMap, logger)
		logger.Info("Standalone index complete", "summary", summary)
		return
	}

	// Start file watcher
	if !cfg.DisableWatcher && isMaster {
		store, _ := getStore(ctx, cfg, false, true, socketPath)
		// watcher expects a store getter, let's wrap it
		sg := func(ctx context.Context) (*db.Store, error) { return store, nil }
		fw, err := watcher.NewFileWatcher(cfg, logger, resetChan, sg, embedder)
		if err != nil {
			logger.Error("Failed to initialize file watcher", "error", err)
			os.Exit(1)
		}
		go fw.Start(ctx)
	} else if cfg.DisableWatcher {
		logger.Info("File watcher is disabled (Slave instance or explicit config)")
	}

	if *daemonFlag {
		logger.Info("Daemon mode active - background workers running. Waiting for signal...")
		// Just wait for the context to be canceled or signal to be received.
		<-ctx.Done()
		return
	}

	if err := srv.Serve(); err != nil {
		logger.Error("Server error", "error", err)
		os.Exit(1)
	}
}
