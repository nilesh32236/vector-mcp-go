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

// IndexStatus represents the state of a background indexing job.
type IndexStatus string

const (
	StatusInitializing IndexStatus = "Initializing..."
	StatusPanic        IndexStatus = "Failed (Panic)"
	StatusError        IndexStatus = "Error"
	StatusCompleted    IndexStatus = "Completed"
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
	for {
		select {
		case <-ctx.Done():
			w.logger.Info("Index worker stopping due to context cancellation")
			return
		case path, ok := <-w.indexQueue:
			if !ok {
				w.logger.Info("Index worker stopping: index queue closed")
				return
			}
			w.processPath(ctx, path)
		}
	}
}

func (w *IndexWorker) processPath(ctx context.Context, path string) {
	defer func() {
		if r := recover(); r != nil {
			w.logger.Error("Indexing worker panicked", "path", path, "recover", r)
			w.progressMap.Store(path, string(StatusPanic))
			if store, err := w.storeGetter(ctx); err == nil {
				store.SetStatus(ctx, path, string(StatusPanic))
			}
		}
	}()

	w.logger.Info("Starting background indexing", "path", path)
	w.progressMap.Store(path, string(StatusInitializing))
	store, err := w.storeGetter(ctx)
	if err != nil {
		w.logger.Error("Background indexing failed: could not get store", "path", path, "error", err)
		w.progressMap.Store(path, fmt.Sprintf("%s: could not get store: %v", StatusError, err))
		return
	}
	store.SetStatus(ctx, path, string(StatusInitializing))

	targetCfg := &config.Config{
		ProjectRoot: path,
		DbPath:      w.cfg.DbPath,
		ModelsDir:   w.cfg.ModelsDir,
		Logger:      w.cfg.Logger,
	}

	summary, err := indexer.IndexFullCodebase(ctx, targetCfg, store, w.embedder, w.progressMap, w.logger)
	if err != nil {
		w.logger.Error("Background indexing failed", "path", path, "error", err)
		errMsg := fmt.Sprintf("%s: %v", StatusError, err)
		w.progressMap.Store(path, errMsg)
		store.SetStatus(ctx, path, errMsg)
		return
	}

	status := fmt.Sprintf("%s: %d indexed, %d skipped", StatusCompleted, summary.FilesIndexed, summary.FilesSkipped)
	w.progressMap.Store(path, status)
	store.SetStatus(ctx, path, status)
	w.logger.Info("Background indexing complete", "path", path, "summary", summary)
}
