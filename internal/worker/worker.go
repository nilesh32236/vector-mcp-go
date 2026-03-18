package worker

import (
	"context"
	"fmt"
	"log/slog"
	"sync"

	"github.com/nilesh32236/vector-mcp-go/internal/config"
	"github.com/nilesh32236/vector-mcp-go/internal/db"
	"github.com/nilesh32236/vector-mcp-go/internal/indexer"
)

// IndexWorker handles background indexing tasks.
type IndexWorker struct {
	cfg         *config.Config
	logger      *slog.Logger
	indexQueue  chan string
	progressMap *sync.Map
	storeGetter func(ctx context.Context) (*db.Store, error)
	embedder    indexer.Embedder
}

// NewIndexWorker creates a new IndexWorker.
func NewIndexWorker(cfg *config.Config, logger *slog.Logger, queue chan string, progress *sync.Map, storeGetter func(ctx context.Context) (*db.Store, error), embedder indexer.Embedder) *IndexWorker {
	return &IndexWorker{
		cfg:         cfg,
		logger:      logger,
		indexQueue:  queue,
		progressMap: progress,
		storeGetter: storeGetter,
		embedder:    embedder,
	}
}

// Start starts the indexing worker goroutine.
func (w *IndexWorker) Start(ctx context.Context) {
	for path := range w.indexQueue {
		w.processPath(ctx, path)
	}
}

func (w *IndexWorker) processPath(ctx context.Context, path string) {
	defer func() {
		if r := recover(); r != nil {
			w.logger.Error("Indexing worker panicked", "path", path, "recover", r)
			w.progressMap.Store(path, "Failed (Panic)")
			if store, err := w.storeGetter(ctx); err == nil {
				store.SetStatus(ctx, path, "Failed (Panic)")
			}
		}
	}()

	w.logger.Info("Starting background indexing", "path", path)
	w.progressMap.Store(path, "Initializing...")
	if store, err := w.storeGetter(ctx); err == nil {
		store.SetStatus(ctx, path, "Initializing...")
	}

	targetCfg := &config.Config{
		ProjectRoot: path,
		DbPath:      w.cfg.DbPath,
		ModelsDir:   w.cfg.ModelsDir,
		Logger:      w.cfg.Logger,
	}

	store, err := w.storeGetter(ctx)
	if err != nil {
		w.logger.Error("Background indexing failed: could not get store", "path", path, "error", err)
		return
	}

	summary, err := indexer.IndexFullCodebase(ctx, targetCfg, store, w.embedder, w.progressMap, w.logger)
	if err != nil {
		w.logger.Error("Background indexing failed", "path", path, "error", err)
		w.progressMap.Store(path, fmt.Sprintf("Error: %v", err))
		store.SetStatus(ctx, path, fmt.Sprintf("Error: %v", err))
		return
	}

	status := fmt.Sprintf("Completed: %d indexed, %d skipped", summary.FilesIndexed, summary.FilesSkipped)
	w.progressMap.Store(path, status)
	store.SetStatus(ctx, path, status)
	w.logger.Info("Background indexing complete", "path", path, "summary", summary)
}
