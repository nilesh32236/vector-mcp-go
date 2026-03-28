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
	EmbedBatch(ctx context.Context, texts []string) ([][]float32, error)
}

// IndexerOptions groups parameters needed for indexing operations.
type IndexerOptions struct {
	Config      *config.Config
	Store       *db.Store
	Embedder    Embedder
	ProgressMap *sync.Map
	Logger      *slog.Logger
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

	hashMapping, _ := opts.Store.GetPathHashMapping(ctx, opts.Config.ProjectRoot)
	var toIndex []string
	for _, path := range files {
		relPath := config.GetRelativePath(path, opts.Config.ProjectRoot)
		currentHash, _ := GetHash(path)
		if existingHash, ok := hashMapping[relPath]; ok && existingHash == currentHash {
			summary.FilesSkipped++
			continue
		}
		toIndex = append(toIndex, path)
	}

	for dbPath := range hashMapping {
		found := false
		for _, absPath := range files {
			if config.GetRelativePath(absPath, opts.Config.ProjectRoot) == dbPath {
				found = true
				break
			}
		}
		if !found {
			opts.Store.DeleteByPath(ctx, dbPath, opts.Config.ProjectRoot)
		}
	}

	if len(toIndex) == 0 {
		opts.Store.SetStatus(ctx, opts.Config.ProjectRoot, fmt.Sprintf("Completed: %d files skipped (up to date)", summary.FilesSkipped))
		return summary, nil
	}

	var wg sync.WaitGroup
	results := make(chan Result, len(toIndex))
	tasks := make(chan string, len(toIndex))

	for i := 0; i < runtime.NumCPU(); i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for path := range tasks {
				results <- ProcessFile(ctx, path, opts)
			}
		}()
	}

	go func() {
		for _, path := range toIndex {
			tasks <- path
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
	}

	if len(batch) > 0 {
		opts.Store.Insert(ctx, batch)
	}

	return summary, nil
}

// ProcessFile indexes a single file if its hash has changed.
func ProcessFile(ctx context.Context, path string, opts IndexerOptions) Result {
	defer func() {
		if r := recover(); r != nil {
			slog.Error("Panic processing file", "path", path, "recover", r)
		}
	}()

	relPath := config.GetRelativePath(path, opts.Config.ProjectRoot)
	currentHash, err := GetHash(path)
	if err != nil {
		return Result{Err: err.Error(), RelPath: relPath}
	}

	// Capture modification time for recency boosting
	var updatedAt string
	if info, err := os.Stat(path); err == nil {
		updatedAt = strconv.FormatInt(info.ModTime().Unix(), 10)
	} else {
		updatedAt = strconv.FormatInt(time.Now().Unix(), 10)
	}

	existingHash, _ := opts.Store.GetFileHash(ctx, relPath, opts.Config.ProjectRoot)
	if existingHash == currentHash {
		return Result{Skipped: true, RelPath: relPath}
	}

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
		if err == nil {
			for i, chunk := range chunks {
				relJSON, _ := json.Marshal(chunk.Relationships)
				symJSON, _ := json.Marshal(chunk.Symbols)
				callsJSON, _ := json.Marshal(chunk.Calls)
				records = append(records, db.Record{
					ID:        fmt.Sprintf("%s-%s-%d-%d", opts.Config.ProjectRoot, relPath, time.Now().UnixNano(), i),
					Content:   chunk.Content,
					Embedding: embs[i],
					Metadata: map[string]string{
						"path":           relPath,
						"project_id":     opts.Config.ProjectRoot,
						"hash":           currentHash,
						"relationships":  string(relJSON),
						"symbols":        string(symJSON),
						"type":           chunk.Type,
						"calls":          string(callsJSON),
						"function_score": fmt.Sprintf("%.2f", chunk.FunctionScore),
						"category":       category,
						"updated_at":     updatedAt,
						"start_line":     strconv.Itoa(chunk.StartLine),
						"end_line":       strconv.Itoa(chunk.EndLine),
					},
				})
			}
		} else {
			// Fallback to single embedding if batch fails
			for _, chunk := range chunks {
				emb, err := opts.Embedder.Embed(ctx, chunk.ContextualString)
				if err != nil {
					continue
				}
				relJSON, _ := json.Marshal(chunk.Relationships)
				symJSON, _ := json.Marshal(chunk.Symbols)
				callsJSON, _ := json.Marshal(chunk.Calls)
				records = append(records, db.Record{
					ID:        fmt.Sprintf("%s-%s-%d", opts.Config.ProjectRoot, relPath, time.Now().UnixNano()),
					Content:   chunk.Content,
					Embedding: emb,
					Metadata: map[string]string{
						"path":           relPath,
						"project_id":     opts.Config.ProjectRoot,
						"hash":           currentHash,
						"relationships":  string(relJSON),
						"symbols":        string(symJSON),
						"type":           chunk.Type,
						"calls":          string(callsJSON),
						"function_score": fmt.Sprintf("%.2f", chunk.FunctionScore),
						"category":       category,
						"updated_at":     updatedAt,
						"start_line":     strconv.Itoa(chunk.StartLine),
						"end_line":       strconv.Itoa(chunk.EndLine),
					},
				})
			}
		}
	}

	if len(records) > 0 {
		opts.Store.DeleteByPath(ctx, relPath, opts.Config.ProjectRoot)
		return Result{Indexed: true, Records: records, RelPath: relPath}
	}
	return Result{RelPath: relPath}
}

// IndexSingleFile indexes a single file and updates the database.
func IndexSingleFile(ctx context.Context, path string, opts IndexerOptions) (IndexSummary, error) {
	res := ProcessFile(ctx, path, opts)
	if res.Err != "" {
		return IndexSummary{Status: "error"}, nil
	}
	if res.Indexed {
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
