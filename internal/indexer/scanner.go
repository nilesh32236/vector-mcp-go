package indexer

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"log/slog"
	"os"
	"path/filepath"
	"runtime"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/dslipak/pdf"
	"github.com/nilesh32236/vector-mcp-go/internal/config"
	"github.com/nilesh32236/vector-mcp-go/internal/db"
	ignore "github.com/sabhiram/go-gitignore"
)

// IndexSummary provides a summary of the indexing operation.
type IndexSummary struct {
	Status         string   `json:"status"`
	FilesProcessed int      `json:"files_processed"`
	FilesIndexed   int      `json:"files_indexed"`
	FilesSkipped   int      `json:"files_skipped"`
	Errors         []string `json:"errors"`
	DurationMs     int64    `json:"duration_ms"`
}

// Result represents the outcome of processing a single file.
type Result struct {
	Indexed bool
	Skipped bool
	Err     string
	RelPath string
	Records []db.Record
}

const MaxContextTokens = 10000

// EstimateTokens provides a rough estimate of the number of tokens in a string.
func EstimateTokens(text string) int {
	return (len(strings.Fields(text)) * 4) / 3
}

// Embedder is an interface for generating vector embeddings from text.
type Embedder interface {
	RerankBatch(ctx context.Context, query string, texts []string) ([]float32, error)
	Embed(ctx context.Context, text string) ([]float32, error)
	EmbedQuery(ctx context.Context, text string) ([]float32, error)
	EmbedBatch(ctx context.Context, texts []string) ([][]float32, error)
}

// Progress represents the real-time state of an indexing operation.
type Progress struct {
	Total      int
	Current    int
	File       string
	Percentage float64
}

// IndexerOptions groups parameters needed for indexing operations.
type IndexerOptions struct {
	Config      *config.Config
	Store       *db.Store
	Embedder    Embedder
	ProgressMap *sync.Map
	Logger      *slog.Logger
	ProgressCh  chan<- Progress // Optional channel for streaming updates
}

// IndexFullCodebase performs a comprehensive index of the project directory.
func IndexFullCodebase(ctx context.Context, opts IndexerOptions) (IndexSummary, error) {
	summary := IndexSummary{Status: "completed"}

	opts.Store.SetStatus(ctx, opts.Config.ProjectRoot, "Scanning files and cleaning index...")

	files, err := ScanFiles(opts.Config.ProjectRoot)
	if err != nil {
		return summary, err
	}
	summary.FilesProcessed = len(files)

	// Phase 1: Discovery (Efficient Hash Comparison)
	hashMapping, _ := opts.Store.GetPathHashMapping(ctx, opts.Config.ProjectRoot)
	var toIndex []struct {
		Path string
		Hash string
	}
	for _, path := range files {
		relPath := config.GetRelativePath(path, opts.Config.ProjectRoot)
		currentHash, _ := GetHash(path)
		if existingHash, ok := hashMapping[relPath]; ok && existingHash == currentHash {
			summary.FilesSkipped++
			continue
		}
		toIndex = append(toIndex, struct {
			Path string
			Hash string
		}{path, currentHash})
	}

	// Cleanup stale files that no longer exist on disk
	filePathsSet := make(map[string]struct{}, len(files))
	for _, absPath := range files {
		relPath := config.GetRelativePath(absPath, opts.Config.ProjectRoot)
		filePathsSet[relPath] = struct{}{}
	}

	for dbPath := range hashMapping {
		if _, found := filePathsSet[dbPath]; !found {
			if err := opts.Store.DeleteByPath(ctx, dbPath, opts.Config.ProjectRoot); err != nil {
				opts.Logger.Error("Failed to delete stale path", "path", dbPath, "error", err)
				summary.Errors = append(summary.Errors, fmt.Sprintf("failed to delete stale path %s: %v", dbPath, err))
				summary.Status = "partially_completed"
			}
		}
	}

	if len(toIndex) == 0 {
		opts.Store.SetStatus(ctx, opts.Config.ProjectRoot, fmt.Sprintf("Completed: %d files skipped (up to date)", summary.FilesSkipped))
		return summary, nil
	}

	// Phase 2: Processing and Atomic Update
	var wg sync.WaitGroup
	results := make(chan Result, len(toIndex))
	tasks := make(chan struct {
		Path string
		Hash string
	}, len(toIndex))

	for i := 0; i < runtime.NumCPU(); i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for task := range tasks {
				results <- ProcessFile(ctx, task.Path, opts, task.Hash)
			}
		}()
	}

	go func() {
		for _, task := range toIndex {
			tasks <- task
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
		if r.Err != "" {
			summary.Errors = append(summary.Errors, r.Err)
		}
		if r.Indexed {
			summary.FilesIndexed++
			// Atomic Update: Delete old chunks before inserting new ones
			// This prevents the "ghost-chunk" bug
			if err := opts.Store.DeleteByPath(ctx, r.RelPath, opts.Config.ProjectRoot); err != nil {
				opts.Logger.Error("Failed to delete old chunks for path", "path", r.RelPath, "error", err)
			}
			batch = append(batch, r.Records...)
		}
		if r.Skipped {
			summary.FilesSkipped++
		}

		if len(batch) >= 50 {
			opts.Logger.Info("Inserting batch of records", "count", len(batch))
			opts.Store.Insert(ctx, batch)
			batch = batch[:0]
		}

		// Real-time progress update
		progress := float64(processed) / float64(totalToIndex) * 100
		status := fmt.Sprintf("Indexing: %.1f%% (%d/%d) - Current: %s", progress, processed, totalToIndex, r.RelPath)
		if opts.ProgressMap != nil {
			opts.ProgressMap.Store(opts.Config.ProjectRoot, status)
		}
		opts.Store.SetStatus(ctx, opts.Config.ProjectRoot, status)

		if opts.ProgressCh != nil {
			select {
			case opts.ProgressCh <- Progress{
				Total:      totalToIndex,
				Current:    processed,
				File:       r.RelPath,
				Percentage: progress,
			}:
			default:
				// Don't block if the channel is full or nobody is listening
			}
		}
	}

	if len(batch) > 0 {
		opts.Store.Insert(ctx, batch)
	}

	return summary, nil
}

// ProcessFile indexes a single file if its hash has changed.
func ProcessFile(ctx context.Context, path string, opts IndexerOptions, currentHash string) Result {
	defer func() {
		if r := recover(); r != nil {
			slog.Error("Panic processing file", "path", path, "recover", r)
		}
	}()

	relPath := config.GetRelativePath(path, opts.Config.ProjectRoot)

	// Capture modification time for recency boosting
	var updatedAt string
	if info, err := os.Stat(path); err == nil {
		updatedAt = strconv.FormatInt(info.ModTime().Unix(), 10)
	} else {
		updatedAt = strconv.FormatInt(time.Now().Unix(), 10)
	}

	priority := GetPriority(relPath)

	var contentStr string
	ext := strings.ToLower(filepath.Ext(path))
	category := "code"
	if ext == ".pdf" || ext == ".md" || ext == ".txt" {
		category = "document"
	}

	if ext == ".pdf" {
		r, err := pdf.Open(path)
		if err != nil {
			return Result{Err: fmt.Sprintf("failed to parse pdf: %v", err), RelPath: relPath}
		}
		var b strings.Builder
		for i := 1; i <= r.NumPage(); i++ {
			p := r.Page(i)
			if p.V.IsNull() {
				continue
			}
			text, err := p.GetPlainText(nil)
			if err == nil {
				b.WriteString(text)
				b.WriteString("\n")
			}
		}
		contentStr = b.String()
	} else {
		content, err := os.ReadFile(path)
		if err != nil {
			return Result{Err: err.Error(), RelPath: relPath}
		}
		contentStr = string(content)
	}

	chunks := CreateChunks(contentStr, relPath)
	var records []db.Record

	// Prepare texts for batch embedding
	var texts []string
	for _, chunk := range chunks {
		texts = append(texts, chunk.ContextualString)
	}

	if len(texts) > 0 {
		embs, err := opts.Embedder.EmbedBatch(ctx, texts)
		if err != nil {
			// Fallback to single embedding if batch fails
			opts.Logger.Warn("Batch embedding failed, falling back to sequential", "error", err, "path", relPath)
			embs = make([][]float32, 0, len(texts))
			for _, text := range texts {
				emb, err := opts.Embedder.Embed(ctx, text)
				if err != nil {
					opts.Logger.Error("Single embedding failed", "error", err)
					continue
				}
				embs = append(embs, emb)
			}
		}

		for i, chunk := range chunks {
			if i >= len(embs) {
				break
			}
			relJSON, _ := json.Marshal(chunk.Relationships)
			symJSON, _ := json.Marshal(chunk.Symbols)
			callsJSON, _ := json.Marshal(chunk.Calls)

			name := ""
			if len(chunk.Symbols) > 0 {
				name = chunk.Symbols[0]
			}

			structJSON, _ := json.Marshal(chunk.StructuralMetadata)

			metadata := map[string]string{
				"path":                relPath,
				"project_id":          opts.Config.ProjectRoot,
				"category":            category,
				"updated_at":          updatedAt,
				"hash":                currentHash,
				"relationships":       string(relJSON),
				"symbols":             string(symJSON),
				"parent_symbol":       chunk.ParentSymbol,
				"type":                "chunk",
				"name":                name,
				"calls":               string(callsJSON),
				"priority":            fmt.Sprintf("%.1f", priority),
				"function_score":      fmt.Sprintf("%.2f", chunk.FunctionScore),
				"docstring":           chunk.Docstring,
				"structural_metadata": string(structJSON),
				"start_line":          strconv.Itoa(chunk.StartLine),
				"end_line":            strconv.Itoa(chunk.EndLine),
			}

			// Deterministic ID for easier debugging
			id := fmt.Sprintf("chunk:%s:%s:%d", opts.Config.ProjectRoot, relPath, i)
			records = append(records, db.Record{
				ID:        id,
				Content:   chunk.Content,
				Embedding: embs[i],
				Metadata:  metadata,
			})
		}
	}

	// Add the file metadata record
	// Ensure dummyEmb has a consistent dimension from the store
	dummyEmb := make([]float32, opts.Store.Dimension)

	records = append(records, db.Record{
		ID:        fmt.Sprintf("filemeta:%s:%s", opts.Config.ProjectRoot, relPath),
		Content:   "", // No content for metadata
		Embedding: dummyEmb,
		Metadata: map[string]string{
			"type":       "file_meta",
			"path":       relPath,
			"project_id": opts.Config.ProjectRoot,
			"hash":       currentHash,
			"updated_at": updatedAt,
		},
	})

	return Result{Indexed: true, Records: records, RelPath: relPath}
}

// IndexSingleFile indexes a single file and updates the database.
func IndexSingleFile(ctx context.Context, path string, opts IndexerOptions) (IndexSummary, error) {
	currentHash, err := GetHash(path)
	if err != nil {
		return IndexSummary{Status: "error", Errors: []string{err.Error()}}, err
	}

	relPath := config.GetRelativePath(path, opts.Config.ProjectRoot)
	res := ProcessFile(ctx, path, opts, currentHash)
	if res.Err != "" {
		return IndexSummary{Status: "error", Errors: []string{res.Err}}, nil
	}
	if res.Indexed {
		// Delete old chunks before inserting new ones
		opts.Store.DeleteByPath(ctx, relPath, opts.Config.ProjectRoot)
		opts.Store.Insert(ctx, res.Records)
	}
	return IndexSummary{Status: "completed", FilesIndexed: 1}, nil
}

var (
	AllowExts = []string{".ts", ".tsx", ".js", ".jsx", ".prisma", ".json", ".css", ".html", ".md", ".env", ".yml", ".yaml", ".go", ".py", ".rs", ".sh", ".txt", ".pdf"}
)

func ScanFiles(root string) ([]string, error) {
	var files []string

	// Try to load .vector-ignore first, then .gitignore
	var ignorer *ignore.GitIgnore
	if _, err := os.Stat(filepath.Join(root, ".vector-ignore")); err == nil {
		ignorer, _ = ignore.CompileIgnoreFile(filepath.Join(root, ".vector-ignore"))
	} else if _, err := os.Stat(filepath.Join(root, ".gitignore")); err == nil {
		ignorer, _ = ignore.CompileIgnoreFile(filepath.Join(root, ".gitignore"))
	}

	err := filepath.Walk(root, func(path string, info os.FileInfo, err error) error {
		if err != nil {
			return err
		}

		relPath, _ := filepath.Rel(root, path)
		if relPath == "." {
			return nil
		}

		// Always exclude default dirs
		if info.IsDir() {
			if IsIgnoredDir(info.Name()) {
				return filepath.SkipDir
			}
		}

		// Always exclude heavy files and lockfiles
		if !info.IsDir() {
			if IsIgnoredFile(info.Name()) {
				return nil
			}
		}

		// Check against ignore rules
		if ignorer != nil && ignorer.MatchesPath(relPath) {
			if info.IsDir() {
				return filepath.SkipDir
			}
			return nil
		}

		if info.IsDir() {
			return nil
		}

		ext := filepath.Ext(path)
		allowed := false
		for _, a := range AllowExts {
			if ext == a {
				allowed = true
				break
			}
		}

		if allowed {
			files = append(files, path)
		}
		return nil
	})
	return files, err
}

func IsIgnoredDir(name string) bool {
	ignored := []string{
		"node_modules", ".git", ".next", ".turbo", "dist",
		"build", "generated", "coverage", "out", "vendor", ".vector-db", ".data",
	}
	for _, d := range ignored {
		if name == d {
			return true
		}
	}
	return false
}

func IsIgnoredFile(name string) bool {
	lowerName := strings.ToLower(name)
	ignoredExact := []string{
		"package-lock.json", "pnpm-lock.yaml", "yarn.lock", "go.sum",
	}
	for _, f := range ignoredExact {
		if lowerName == f {
			return true
		}
	}

	ignoredSuffixes := []string{
		".map", ".min.js", ".svg", ".png", ".jpg", ".jpeg", ".gif", ".ico", ".pdf",
	}
	for _, s := range ignoredSuffixes {
		if len(lowerName) >= len(s) && lowerName[len(lowerName)-len(s):] == s {
			return true
		}
	}

	return false
}

// GetPriority returns a search boost factor based on the file path.
// High-priority files include ADRs and architectural documentation.
func GetPriority(relPath string) float32 {
	lower := strings.ToLower(relPath)
	if strings.Contains(lower, "adr/") || strings.Contains(lower, "architecture") || filepath.Base(lower) == "architecture.md" {
		return 1.5
	}
	return 1.0
}

func GetHash(path string) (string, error) {
	f, err := os.Open(path)
	if err != nil {
		return "", err
	}
	defer f.Close()

	h := sha256.New()
	if _, err := io.Copy(h, f); err != nil {
		return "", err
	}

	return hex.EncodeToString(h.Sum(nil)), nil
}
