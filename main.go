package main

import (
	"context"
	"flag"
	"fmt"
	"log/slog"
	"os"
	"os/signal"
	"path/filepath"
	"strings"
	"sync"
	"syscall"
	"time"

	libmcp "github.com/mark3labs/mcp-go/mcp"
	"github.com/nilesh32236/vector-mcp-go/internal/analysis"
	"github.com/nilesh32236/vector-mcp-go/internal/api"
	"github.com/nilesh32236/vector-mcp-go/internal/config"
	"github.com/nilesh32236/vector-mcp-go/internal/daemon"
	"github.com/nilesh32236/vector-mcp-go/internal/db"
	"github.com/nilesh32236/vector-mcp-go/internal/embedding"
	"github.com/nilesh32236/vector-mcp-go/internal/indexer"
	"github.com/nilesh32236/vector-mcp-go/internal/mcp"
	"github.com/nilesh32236/vector-mcp-go/internal/observability/tracing"
	"github.com/nilesh32236/vector-mcp-go/internal/onnx"
	"github.com/nilesh32236/vector-mcp-go/internal/system"
	"github.com/nilesh32236/vector-mcp-go/internal/watcher"
	"github.com/nilesh32236/vector-mcp-go/internal/worker"
)

var (
	Version   = "1.0.0"
	BuildTime = "unset"
	Commit    = "none"
)

// App encapsulates the application's shared resources and lifecycle.
type App struct {
	cfg          *config.Config
	logger       *slog.Logger
	storeMu      sync.RWMutex
	store        *db.Store
	embedPool    *embedding.EmbedderPool
	indexQueue   chan string
	progressMap  *sync.Map
	resetChan    chan string
	isMaster     bool
	daemonClient *daemon.Client
	masterServer *daemon.MasterServer
	mcpServer    *mcp.Server
	apiServer    *api.Server
	analyzer     analysis.Analyzer
	throttler    *system.MemThrottler
	traceProv    *tracing.Provider
	ctx          context.Context
	cancel       context.CancelFunc
}

func NewApp(cfg *config.Config) *App {
	ctx, cancel := context.WithCancel(context.Background())
	return &App{
		cfg:         cfg,
		logger:      cfg.Logger,
		indexQueue:  make(chan string, 100),
		progressMap: &sync.Map{},
		resetChan:   make(chan string, 1),
		ctx:         ctx,
		cancel:      cancel,
		analyzer:    analysis.NewMultiAnalyzer(analysis.NewPatternAnalyzer()),
		throttler:   system.NewMemThrottler(90.0, 512),
	}
}

func (a *App) getStore(ctx context.Context, forceRefresh bool) (*db.Store, error) {
	if !a.isMaster {
		return nil, fmt.Errorf("instance is not master")
	}

	a.storeMu.Lock()
	defer a.storeMu.Unlock()

	if a.store != nil && !forceRefresh {
		return a.store, nil
	}

	store, err := db.Connect(ctx, a.cfg.DbPath, "project_context", a.cfg.Dimension)
	if err != nil {
		return nil, err
	}
	a.store = store
	return a.store, nil
}

func (a *App) Init(socketPath string) error {
	traceProvider, err := tracing.Init(a.ctx)
	if err != nil {
		return fmt.Errorf("failed to initialize tracing: %w", err)
	}
	a.traceProv = traceProvider

	// 1. Master/Slave Detection
	a.masterServer, err = daemon.StartMasterServer(socketPath, nil, a.indexQueue, nil, a.progressMap)
	if err == nil {
		a.isMaster = true
		a.logger.Info("Starting as MASTER instance", "socket", socketPath, "version", Version)
	} else {
		if !strings.Contains(err.Error(), "master already running") {
			return fmt.Errorf("failed to initialize master daemon: %w", err)
		}
		a.isMaster = false
		a.logger.Info("Starting as SLAVE instance (master already running)", "socket", socketPath, "version", Version)
		a.daemonClient = daemon.NewClient(socketPath)
		a.cfg.DisableWatcher = true
	}

	var embedder indexer.Embedder

	if a.isMaster {
		if err := onnx.Init(); err != nil {
			return fmt.Errorf("failed to initialize ONNX: %w", err)
		}

		mc, err := embedding.EnsureModel(a.cfg.ModelsDir, a.cfg.ModelName)
		if err != nil {
			return fmt.Errorf("failed to ensure models: %w", err)
		}
		mc = mc.WithMatryoshkaDimension(a.cfg.MatryoshkaDim)
		a.cfg.Dimension = mc.EffectiveDimension()
		if mc.MatryoshkaDim > 0 {
			a.logger.Info("Matryoshka truncation enabled",
				"model", a.cfg.ModelName,
				"source_dimension", mc.Dimension,
				"effective_dimension", mc.EffectiveDimension(),
			)
		}

		var rerankerMc *embedding.ModelConfig
		if a.cfg.RerankerModelName != "" {
			rmc, err := embedding.EnsureModel(a.cfg.ModelsDir, a.cfg.RerankerModelName)
			if err != nil {
				a.logger.Warn("Failed to ensure reranker model", "error", err)
			} else {
				rerankerMc = &rmc
			}
		}

		pool, err := embedding.NewEmbedderPool(a.ctx, a.cfg.ModelsDir, a.cfg.EmbedderPoolSize, mc, rerankerMc)
		if err != nil {
			return fmt.Errorf("failed to init embedder pool: %w", err)
		}
		a.embedPool = pool
		realEmbedder := &poolEmbedder{pool: a.embedPool}
		embedder = realEmbedder

		store, err := a.getStore(a.ctx, false)
		if err != nil {
			return fmt.Errorf("failed to init store: %w", err)
		}

		a.masterServer.UpdateEmbedder(realEmbedder)
		a.masterServer.UpdateStore(store)

		if a.cfg.EnableLiveIndexing {
			go a.runLiveIndexing(embedder)
		}
	} else {
		embedder = daemon.NewRemoteEmbedder(socketPath)
	}

	// Initialize LSP and Safety components
	a.analyzer = analysis.NewMultiAnalyzer(analysis.NewPatternAnalyzer(), analysis.NewVettingAnalyzer(a.cfg.ProjectRoot))

	// Initialize MCP Server
	resolver := indexer.InitResolver(a.cfg.ProjectRoot)
	storeGetter := func(ctx context.Context) (*db.Store, error) {
		return a.getStore(ctx, false)
	}
	a.mcpServer = mcp.NewServer(a.cfg, a.logger, storeGetter, embedder, a.indexQueue, a.daemonClient, a.progressMap, a.resetChan, resolver, a.throttler)

	if !a.isMaster {
		a.mcpServer.WithRemoteStore(daemon.NewRemoteStore(socketPath))
	}

	// Initialize API Server
	if a.isMaster {
		a.apiServer = api.NewServer(a.cfg, storeGetter, embedder, a.mcpServer)
	}

	return nil
}

func (a *App) runLiveIndexing(embedder indexer.Embedder) {
	a.logger.Info("Starting live indexing scan")
	store, err := a.getStore(a.ctx, false)
	if err != nil {
		a.logger.Error("Live indexing failed to get store", "error", err)
		return
	}
	opts := indexer.IndexerOptions{
		Config:      a.cfg,
		Store:       store,
		Embedder:    embedder,
		ProgressMap: a.progressMap,
		Logger:      a.logger,
	}
	summary, err := indexer.IndexFullCodebase(context.Background(), opts)
	if err != nil {
		a.logger.Error("Live indexing failed", "error", err)
	} else {
		a.logger.Info("Live indexing complete", "files_indexed", summary.FilesIndexed, "files_skipped", summary.FilesSkipped)
		// Populate Knowledge Graph after indexing
		if err := a.mcpServer.PopulateGraph(context.Background()); err != nil {
			a.logger.Error("Failed to populate graph after indexing", "error", err)
		}
	}
}

func (a *App) Start(indexFlag, daemonFlag bool) error {
	if a.isMaster {
		// Start API
		go func() {
			if err := a.apiServer.Start(); err != nil {
				a.logger.Error("API server error", "error", err)
			}
		}()

		// Start Worker
		idxWorker := worker.NewIndexWorker(a.cfg, a.logger, a.indexQueue, a.progressMap, func(ctx context.Context) (*db.Store, error) {
			return a.getStore(ctx, false)
		}, a.mcpServer.GetEmbedder())
		go idxWorker.Start(a.ctx)

		// Start Watcher
		if !a.cfg.DisableWatcher {
			sg := func(ctx context.Context) (*db.Store, error) {
				return a.getStore(ctx, false)
			}
			st, _ := sg(a.ctx)
			distiller := analysis.NewDistiller(st, a.mcpServer.GetEmbedder(), a.logger)
			fw, err := watcher.NewFileWatcher(a.cfg, a.logger, a.resetChan, sg, a.mcpServer.GetEmbedder(), a.analyzer, distiller, func(level libmcp.LoggingLevel, data any, logger string) {
				a.mcpServer.SendNotification(level, data, logger)
			})
			if err == nil {
				go fw.Start(a.ctx)
			} else {
				a.logger.Error("Failed to start watcher", "error", err)
			}
		}

		// Pre-populate graph from existing Store if already indexed
		if err := a.mcpServer.PopulateGraph(a.ctx); err != nil {
			a.logger.Warn("Initial graph population failed (maybe DB is empty)", "error", err)
		}
	}

	if indexFlag {
		if !a.isMaster {
			return fmt.Errorf("indexing requires master instance")
		}
		store, _ := a.getStore(a.ctx, false)
		opts := indexer.IndexerOptions{
			Config:      a.cfg,
			Store:       store,
			Embedder:    a.mcpServer.GetEmbedder(),
			ProgressMap: a.progressMap,
			Logger:      a.logger,
		}
		_, err := indexer.IndexFullCodebase(a.ctx, opts)
		return err
	}

	if daemonFlag {
		a.logger.Info("Daemon mode active")
		<-a.ctx.Done()
		return nil
	}

	return a.mcpServer.Serve()
}

func (a *App) Stop() {
	a.logger.Info("Shutting down...")
	a.cancel()
	shutdownCtx, shutdownCancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer shutdownCancel()

	if a.apiServer != nil {
		if err := a.apiServer.Shutdown(shutdownCtx); err != nil {
			a.logger.Warn("Failed to shut down API server", "error", err)
		}
	}
	if a.embedPool != nil {
		a.embedPool.Close()
	}
	if a.isMaster && a.masterServer != nil {
		a.masterServer.Close()
	}
	if a.throttler != nil {
		a.throttler.Stop()
	}
	if a.traceProv != nil {
		if err := a.traceProv.Shutdown(shutdownCtx); err != nil {
			a.logger.Warn("Failed to shut down tracer provider", "error", err)
		}
	}
	// Give time for background tasks to clean up
	time.Sleep(200 * time.Millisecond)
}

func main() {
	dataDirFlag := flag.String("data-dir", "", "Base directory for DB and models")
	modelsDirFlag := flag.String("models-dir", "", "Specific directory for models")
	dbPathFlag := flag.String("db-path", "", "Specific path for the database")
	indexFlag := flag.Bool("index", false, "Run full codebase indexing and exit")
	daemonFlag := flag.Bool("daemon", false, "Run as background daemon (master worker) without MCP stdio server")
	versionFlag := flag.Bool("version", false, "Print version and exit")
	flag.Parse()

	if *versionFlag {
		fmt.Printf("vector-mcp-go version %s (commit: %s, built: %s)\n", Version, Commit, BuildTime)
		return
	}

	cfg := config.LoadConfig(*dataDirFlag, *modelsDirFlag, *dbPathFlag)
	app := NewApp(cfg)

	// Graceful shutdown
	sigChan := make(chan os.Signal, 1)
	signal.Notify(sigChan, syscall.SIGINT, syscall.SIGTERM)
	go func() {
		sig := <-sigChan
		cfg.Logger.Info("Received signal", "signal", sig)
		app.Stop()
		os.Exit(0)
	}()

	socketPath := filepath.Join(cfg.DataDir, "daemon.sock")
	if err := app.Init(socketPath); err != nil {
		cfg.Logger.Error("Initialization failed", "error", err)
		os.Exit(1)
	}

	if err := app.Start(*indexFlag, *daemonFlag); err != nil {
		cfg.Logger.Error("Application error", "error", err)
		os.Exit(1)
	}
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

func (pe *poolEmbedder) EmbedQuery(ctx context.Context, text string) ([]float32, error) {
	e, err := pe.pool.Get(ctx)
	if err != nil {
		return nil, err
	}
	defer pe.pool.Put(e)
	return e.EmbedQuery(ctx, text)
}

func (pe *poolEmbedder) EmbedBatch(ctx context.Context, texts []string) ([][]float32, error) {
	e, err := pe.pool.Get(ctx)
	if err != nil {
		return nil, err
	}
	defer pe.pool.Put(e)
	return e.EmbedBatch(ctx, texts)
}

func (pe *poolEmbedder) RerankBatch(ctx context.Context, query string, texts []string) ([]float32, error) {
	e, err := pe.pool.Get(ctx)
	if err != nil {
		return nil, err
	}
	defer pe.pool.Put(e)
	return e.RerankBatch(ctx, query, texts)
}
