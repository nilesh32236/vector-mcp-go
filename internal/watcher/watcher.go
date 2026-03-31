package watcher

import (
	"context"
	"encoding/json"
	"fmt"
	"log/slog"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"

	"github.com/fsnotify/fsnotify"
	"github.com/mark3labs/mcp-go/mcp"
	"github.com/nilesh32236/vector-mcp-go/internal/analysis"
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
	analyzer         analysis.Analyzer
	distiller        *analysis.Distiller
	notifyFunc       func(level mcp.LoggingLevel, data any, logger string)
}

// NewFileWatcher creates a new FileWatcher.
func NewFileWatcher(cfg *config.Config, logger *slog.Logger, resetChan chan string, storeGetter func(ctx context.Context) (*db.Store, error), embedder indexer.Embedder, analyzer analysis.Analyzer, distiller *analysis.Distiller, notify func(level mcp.LoggingLevel, data any, logger string)) (*FileWatcher, error) {
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
		analyzer:         analyzer,
		distiller:        distiller,
		notifyFunc:       notify,
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
		if d != nil && d.IsDir() {
			if indexer.IsIgnoredDir(d.Name()) {
				return filepath.SkipDir
			}
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
				if indexer.IsIgnoredDir(d.Name()) {
					return filepath.SkipDir
				}
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
					opts := indexer.IndexerOptions{
						Config:   fw.cfg,
						Store:    store,
						Embedder: fw.embedder,
					}
					indexer.IndexSingleFile(ctx, name, opts)
					fw.logger.Info("File re-indexed (proactive)", "path", name)

					// --- Architectural Guardrails ---
					relPath := config.GetRelativePath(name, activeRoot)
					fw.CheckArchitecturalCompliance(ctx, relPath, activeRoot)

					// --- Autonomous Re-Distillation ---
					fw.RedistillDependents(ctx, relPath, activeRoot)

					// --- Proactive Analysis ---
					if fw.analyzer != nil {
						issues, err := fw.analyzer.Analyze(ctx, name)
						if err == nil && len(issues) > 0 {
							for _, issue := range issues {
								msg := fmt.Sprintf("⚠️ %s at %s:%d: %s", issue.Source, filepath.Base(issue.Path), issue.Line, issue.Message)
								fw.logger.Info("Proactive Analysis found issue", "issue", msg)
								if fw.notifyFunc != nil {
									level := mcp.LoggingLevelInfo
									if issue.Severity == "Error" || issue.Severity == "Warning" {
										level = mcp.LoggingLevelWarning
									}
									fw.notifyFunc(level, msg, "proactive-analysis")
								}
							}
						}
					}
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

func (fw *FileWatcher) CheckArchitecturalCompliance(ctx context.Context, relPath string, projectRoot string) {
	store, err := fw.storeGetter(ctx)
	if err != nil {
		return
	}

	// 1. Fetch the records for the file we just indexed
	records, err := store.GetByPrefix(ctx, relPath, projectRoot)
	if err != nil || len(records) == 0 {
		return
	}

	// 2. Search for relevant ADRs and Distilled Summaries
	relevantRules, err := store.HybridSearch(ctx, "architecture dependency rules constraints ADR", nil, 5, []string{projectRoot}, "")
	if err != nil {
		return
	}

	for _, r := range records {
		var currentDeps []string
		if deps, ok := r.Metadata["relationships"]; ok {
			_ = json.Unmarshal([]byte(deps), &currentDeps)
		}

		for _, rule := range relevantRules {
			cat := rule.Metadata["category"]
			if cat != "adr" && cat != "distilled" {
				continue
			}

			// Simple heuristic: look for "Forbidden: [pkg]" or "No [pkg] in [this pkg]"
			content := strings.ToLower(rule.Content)
			for _, dep := range currentDeps {
				depLower := strings.ToLower(dep)
				// Example rule: "No database in internal/api"
				if strings.Contains(content, "no "+depLower) || strings.Contains(content, "forbidden: "+depLower) {
					msg := fmt.Sprintf("🛡️ Architectural Alert: File `%s` might violate rule in `%s`. Found forbidden dependency: `%s`",
						relPath, rule.Metadata["path"], dep)
					fw.logger.Warn("Architectural violation detected", "msg", msg)
					if fw.notifyFunc != nil {
						fw.notifyFunc(mcp.LoggingLevelWarning, msg, "architectural-guardrail")
					}
				}
			}
		}
	}
}

func (fw *FileWatcher) RedistillDependents(ctx context.Context, relPath string, projectRoot string) {
	if fw.distiller == nil {
		return
	}

	pkg := filepath.Dir(relPath)
	store, err := fw.storeGetter(ctx)
	if err != nil {
		return
	}

	// 1. Find packages that depend on this package
	// Simple heuristic: search for usages of this package path in 'relationships' metadata
	query := fmt.Sprintf("pkg:%s", pkg)
	records, err := store.HybridSearch(ctx, query, nil, 20, []string{projectRoot}, "")
	if err != nil {
		return
	}

	dependentPkgs := make(map[string]bool)
	for _, r := range records {
		if dPkg, ok := r.Metadata["path"]; ok {
			dir := filepath.Dir(dPkg)
			if dir != pkg {
				dependentPkgs[dir] = true
			}
		}
	}

	// 2. Trigger re-distillation for each dependent package
	for dPkg := range dependentPkgs {
		fw.logger.Info("Triggering autonomous re-distillation for dependent package", "package", dPkg, "reason", relPath)
		_, _ = fw.distiller.DistillPackagePurpose(ctx, projectRoot, dPkg)
	}
}
