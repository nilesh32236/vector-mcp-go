package main

import (
	"context"
	"flag"
	"fmt"
	"log/slog"
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
	"github.com/nilesh32236/vector-mcp-go/internal/llm"
	"github.com/nilesh32236/vector-mcp-go/internal/mcp"
	"github.com/nilesh32236/vector-mcp-go/internal/onnx"
	"github.com/nilesh32236/vector-mcp-go/internal/watcher"
	"github.com/nilesh32236/vector-mcp-go/internal/worker"
)

// Dependencies encapsulates the application's shared resources,
// eliminating global state and facilitating dependency injection.
type Dependencies struct {
	cfg         *config.Config
	logger      *slog.Logger
	store       *db.Store
	embedPool   *embedding.EmbedderPool
	indexQueue  chan string
	progressMap *sync.Map
	resetChan   chan string
	dbMu        *sync.RWMutex
}

func (d *Dependencies) getStore(ctx context.Context, forceRefresh bool, isMaster bool) (*db.Store, error) {
	if !isMaster {
		return nil, nil
	}

	d.dbMu.Lock()
	defer d.dbMu.Unlock()
	if d.store != nil && !forceRefresh {
		return d.store, nil
	}
	store, err := db.Connect(ctx, d.cfg.DbPath, "project_context", d.cfg.Dimension)
	if err != nil {
		return nil, err
	}
	d.store = store
	return d.store, nil
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

func (pe *poolEmbedder) EmbedBatch(ctx context.Context, texts []string) ([][]float32, error) {
	e, err := pe.pool.Get(ctx)
	if err != nil {
		return nil, err
	}
	defer pe.pool.Put(e)
	return e.EmbedBatch(ctx, texts)
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

	deps := &Dependencies{
		cfg:         cfg,
		logger:      logger,
		indexQueue:  make(chan string, 100),
		progressMap: &sync.Map{},
		resetChan:   make(chan string, 1),
		dbMu:        &sync.RWMutex{},
	}

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
	masterServer, err = daemon.StartMasterServer(socketPath, nil, deps.indexQueue, nil, deps.progressMap)
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

	// 2. Master-only Initialization
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

		var rerankerMc *embedding.ModelConfig
		if cfg.RerankerModelName != "" {
			rmc, err := embedding.EnsureModel(cfg.ModelsDir, cfg.RerankerModelName)
			if err != nil {
				logger.Warn("Failed to ensure reranker model, continuing without it", "error", err)
			} else {
				rerankerMc = &rmc
			}
		}

		pool, err := embedding.NewEmbedderPool(ctx, cfg.ModelsDir, cfg.EmbedderPoolSize, mc, rerankerMc)
		if err != nil {
			logger.Error("Failed to initialize embedder pool", "error", err)
			os.Exit(1)
		}
		deps.embedPool = pool

		realEmbedder := &poolEmbedder{pool: deps.embedPool}
		embedder = realEmbedder

		// Initialize store for Master
		store, err := deps.getStore(ctx, false, true)
		if err != nil {
			logger.Error("Failed to initialize store", "error", err)
			os.Exit(1)
		}

		// Update daemon with real embedder and store
		masterServer.UpdateEmbedder(realEmbedder)
		masterServer.UpdateStore(store)

		// 4. Background Indexing on Startup (Live Indexing)
		if cfg.EnableLiveIndexing {
			logger.Info("Live Indexing enabled - starting initial codebase scan in background")
			go func() {
				store, err := deps.getStore(ctx, false, true)
				if err != nil {
					logger.Error("Failed to get store for live indexing", "error", err)
					return
				}
				opts := indexer.IndexerOptions{
					Config:      cfg,
					Store:       store,
					Embedder:    embedder,
					ProgressMap: deps.progressMap,
					Logger:      logger,
				}
				summary, err := indexer.IndexFullCodebase(context.Background(), opts)
				if err != nil {
					logger.Error("Live indexing failed", "error", err)
				} else {
					logger.Info("Live indexing complete", "files_indexed", summary.FilesIndexed, "files_skipped", summary.FilesSkipped)
				}
			}()
		}
	}

	// 3. Define store getters that handle local/remote transparency
	storeGetter := func(ctx context.Context) (*db.Store, error) {
		if isMaster {
			return deps.getStore(ctx, false, true)
		}
		return nil, fmt.Errorf("slave instance cannot use local store getter")
	}

	// Start MCP server
	resolver := indexer.InitResolver(cfg.ProjectRoot)
	srv := mcp.NewServer(cfg, logger, storeGetter, embedder, deps.indexQueue, daemonClient, deps.progressMap, deps.resetChan, resolver)
	if !isMaster {
		remoteStore := daemon.NewRemoteStore(socketPath)
		srv.WithRemoteStore(remoteStore)
	}

	// Start API server (Master only)
	if isMaster {
		apiSrv := api.NewServer(cfg, storeGetter, embedder, srv)
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
		idxWorker := worker.NewIndexWorker(cfg, logger, deps.indexQueue, deps.progressMap, storeGetter, embedder)
		go idxWorker.Start(ctx)
	}

	// Standalone indexing logic
	if *indexFlag {
		if !isMaster {
			logger.Error("Standalone indexing can only be run when no other instance is active (Master only)")
			os.Exit(1)
		}
		store, err := deps.getStore(ctx, false, true)
		if err != nil {
			logger.Error("Failed to get store for indexing", "error", err)
			os.Exit(1)
		}
		opts := indexer.IndexerOptions{
			Config:      cfg,
			Store:       store,
			Embedder:    embedder,
			ProgressMap: deps.progressMap,
			Logger:      logger,
		}
		summary, err := indexer.IndexFullCodebase(ctx, opts)
		if err != nil {
			logger.Error("Full indexing failed", "error", err)
			os.Exit(1)
		}
		logger.Info("Standalone index complete", "summary", summary)
		return
	}

	// Start file watcher
	if !cfg.DisableWatcher && isMaster {
		store, err := deps.getStore(ctx, false, true)
		if err != nil {
			logger.Error("Failed to get store for watcher", "error", err)
			os.Exit(1)
		}
		sg := func(ctx context.Context) (*db.Store, error) { return store, nil }
		fw, err := watcher.NewFileWatcher(cfg, logger, deps.resetChan, sg, embedder)
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
		<-ctx.Done()
		return
	}

	if err := srv.Serve(); err != nil {
		logger.Error("Server error", "error", err)
		os.Exit(1)
	}
}
func (pe *poolEmbedder) RerankBatch(ctx context.Context, query string, texts []string) ([]float32, error) {
	e, err := pe.pool.Get(ctx)
	if err != nil {
		return nil, err
	}
	defer pe.pool.Put(e)
	return e.RerankBatch(ctx, query, texts)
}
