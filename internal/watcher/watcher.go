package watcher

import (
	"context"
	"log/slog"
	"os"
	"path/filepath"
	"sync"
	"time"

	"github.com/fsnotify/fsnotify"
	"github.com/nilesh32236/vector-mcp-go/internal/config"
	"github.com/nilesh32236/vector-mcp-go/internal/db"
	"github.com/nilesh32236/vector-mcp-go/internal/indexer"
)

// FileWatcher monitors file system events and triggers indexing.
type FileWatcher struct {
	cfg              *config.Config
	logger           *slog.Logger
	tracker          *fsnotify.Watcher
	eventChan        chan fsnotify.Event
	watcherResetChan chan string
	watcherMu        sync.Mutex
	currentRoot      string
	storeGetter      func(ctx context.Context) (*db.Store, error)
	embedder         indexer.Embedder
}

// NewFileWatcher creates a new FileWatcher.
func NewFileWatcher(cfg *config.Config, logger *slog.Logger, resetChan chan string, storeGetter func(ctx context.Context) (*db.Store, error), embedder indexer.Embedder) (*FileWatcher, error) {
	watcher, err := fsnotify.NewWatcher()
	if err != nil {
		return nil, err
	}
	return &FileWatcher{
		cfg:              cfg,
		logger:           logger,
		tracker:          watcher,
		eventChan:        make(chan fsnotify.Event, 1000),
		watcherResetChan: resetChan,
		storeGetter:      storeGetter,
		embedder:         embedder,
	}, nil
}

// Start starts the file watcher and debounce goroutines.
func (fw *FileWatcher) Start(ctx context.Context) {
	fw.resetWatcher(fw.cfg.ProjectRoot)

	// Debounce goroutine
	go fw.runDebounce(ctx)

	for {
		select {
		case newRoot := <-fw.watcherResetChan:
			fw.resetWatcher(newRoot)
		case event, ok := <-fw.tracker.Events:
			if !ok {
				return
			}
			if event.Has(fsnotify.Create) {
				info, _ := os.Stat(event.Name)
				if info != nil && info.IsDir() && !indexer.IsIgnoredDir(info.Name()) {
					fw.tracker.Add(event.Name)
					fw.watchRecursive(event.Name)
				}
			}
			fw.eventChan <- event
		case <-ctx.Done():
			fw.tracker.Close()
			return
		}
	}
}

func (fw *FileWatcher) watchRecursive(root string) {
	fw.watcherMu.Lock()
	defer fw.watcherMu.Unlock()
	filepath.WalkDir(root, func(path string, d os.DirEntry, err error) error {
		if d != nil && d.IsDir() && !indexer.IsIgnoredDir(d.Name()) {
			fw.tracker.Add(path)
		}
		return nil
	})
}

func (fw *FileWatcher) resetWatcher(newRoot string) {
	fw.watcherMu.Lock()
	if fw.currentRoot != "" {
		filepath.WalkDir(fw.currentRoot, func(path string, d os.DirEntry, err error) error {
			if d != nil && d.IsDir() {
				fw.tracker.Remove(path)
			}
			return nil
		})
	}
	fw.currentRoot = newRoot
	fw.watcherMu.Unlock()
	fw.watchRecursive(newRoot)
	fw.logger.Info("File watcher reset to new project root", "root", newRoot)
}

func (fw *FileWatcher) runDebounce(ctx context.Context) {
	pending := make(map[string]fsnotify.Op)
	timer := time.NewTimer(500 * time.Millisecond)
	timer.Stop()

	for {
		select {
		case event := <-fw.eventChan:
			pending[event.Name] = event.Op
			timer.Stop()
			timer.Reset(500 * time.Millisecond)
		case <-timer.C:
			fw.processPending(ctx, pending)
			pending = make(map[string]fsnotify.Op)
		case <-ctx.Done():
			return
		}
	}
}

func (fw *FileWatcher) processPending(ctx context.Context, pending map[string]fsnotify.Op) {
	fw.watcherMu.Lock()
	activeRoot := fw.currentRoot
	fw.watcherMu.Unlock()

	for name, op := range pending {
		if op.Has(fsnotify.Write) || op.Has(fsnotify.Create) {
			ext := filepath.Ext(name)
			if ext == ".go" || ext == ".ts" || ext == ".tsx" || ext == ".js" || ext == ".jsx" || ext == ".md" {
				store, err := fw.storeGetter(ctx)
				if err == nil {
					indexer.IndexSingleFile(ctx, name, fw.cfg, store, fw.embedder)
				}
			}
		}
		if op.Has(fsnotify.Remove) || op.Has(fsnotify.Rename) {
			relPath := config.GetRelativePath(name, activeRoot)
			store, err := fw.storeGetter(ctx)
			if err == nil {
				store.DeleteByPrefix(ctx, relPath, activeRoot)
				fw.logger.Info("Path removed from vector index (prefix delete)", "path", relPath, "project", activeRoot)
			}
		}
	}
}
