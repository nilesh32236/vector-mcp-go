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

// MaxContextTokens is the maximum number of tokens considered for a single file context.
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

// Options groups parameters needed for indexing operations.
type Options struct {
	Config      *config.Config
	Store       *db.Store
	Embedder    Embedder
	ProgressMap *sync.Map
	Logger      *slog.Logger
}

// IndexFullCodebase performs a comprehensive index of the project directory.
func IndexFullCodebase(ctx context.Context, opts Options) (IndexSummary, error) {
	summary := IndexSummary{Status: "completed"}
	_ = opts.Store.SetStatus(ctx, opts.Config.ProjectRoot, "Scanning files and cleaning index...")

	files, err := ScanFiles(opts.Config.ProjectRoot)
	if err != nil {
		return summary, err
	}
	summary.FilesProcessed = len(files)

	hashMapping, _ := opts.Store.GetPathHashMapping(ctx, opts.Config.ProjectRoot)
	toIndex, discoverySkipped := opts.discoverFilesToIndex(files, hashMapping)
	summary.FilesSkipped += discoverySkipped

	opts.cleanupStaleFiles(ctx, files, hashMapping, &summary)

	if len(toIndex) == 0 {
		_ = opts.Store.SetStatus(ctx, opts.Config.ProjectRoot, fmt.Sprintf("Completed: %d files skipped (up to date)", summary.FilesSkipped))
		return summary, nil
	}

	return opts.processIndexing(ctx, toIndex, &summary)
}

func (opts Options) discoverFilesToIndex(files []string, hashMapping map[string]string) ([]struct{ Path, Hash string }, int) {
	var toIndex []struct{ Path, Hash string }
	skipped := 0
	for _, path := range files {
		relPath := config.GetRelativePath(path, opts.Config.ProjectRoot)
		currentHash, _ := GetHash(path)
		if existingHash, ok := hashMapping[relPath]; ok && existingHash == currentHash {
			skipped++
			continue
		}
		toIndex = append(toIndex, struct{ Path, Hash string }{path, currentHash})
	}
	return toIndex, skipped
}

func (opts Options) cleanupStaleFiles(ctx context.Context, files []string, hashMapping map[string]string, summary *IndexSummary) {
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
}

func (opts Options) processIndexing(ctx context.Context, toIndex []struct{ Path, Hash string }, summary *IndexSummary) (IndexSummary, error) {
	var wg sync.WaitGroup
	results := make(chan Result, len(toIndex))
	tasks := make(chan struct{ Path, Hash string }, len(toIndex))

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

	opts.collectResults(ctx, results, len(toIndex), summary)
	return *summary, nil
}

func (opts Options) collectResults(ctx context.Context, results chan Result, total int, summary *IndexSummary) {
	var batch []db.Record
	processed := 0
	for r := range results {
		processed++
		if r.Err != "" {
			summary.Errors = append(summary.Errors, r.Err)
		}
		if r.Indexed {
			summary.FilesIndexed++
			_ = opts.Store.DeleteByPath(ctx, r.RelPath, opts.Config.ProjectRoot)
			batch = append(batch, r.Records...)
		}
		if r.Skipped {
			summary.FilesSkipped++
		}

		if len(batch) >= 50 {
			_ = opts.Store.Insert(ctx, batch)
			batch = batch[:0]
		}

		progress := float64(processed) / float64(total) * 100
		status := fmt.Sprintf("Indexing: %.1f%% (%d/%d) - Current: %s", progress, processed, total, r.RelPath)
		if opts.ProgressMap != nil {
			opts.ProgressMap.Store(opts.Config.ProjectRoot, status)
		}
		_ = opts.Store.SetStatus(ctx, opts.Config.ProjectRoot, status)
	}

	if len(batch) > 0 {
		_ = opts.Store.Insert(ctx, batch)
	}
}

// ProcessFile indexes a single file if its hash has changed.
func ProcessFile(ctx context.Context, path string, opts Options, currentHash string) Result {
	defer func() {
		if r := recover(); r != nil {
			slog.Error("Panic processing file", "path", path, "recover", r)
		}
	}()

	relPath := config.GetRelativePath(path, opts.Config.ProjectRoot)
	content, err := readFileContent(path)
	if err != nil {
		return Result{Err: err.Error(), RelPath: relPath}
	}

	updatedAt := getFileUpdatedAt(path)
	priority := GetPriority(relPath)
	category := getFileCategory(path)

	chunks := CreateChunks(ctx, content, relPath)
	records, err := opts.prepareRecords(ctx, chunks, relPath, category, updatedAt, currentHash, priority)
	if err != nil {
		return Result{Err: err.Error(), RelPath: relPath}
	}

	return Result{Indexed: true, Records: records, RelPath: relPath}
}

func readFileContent(path string) (string, error) {
	ext := strings.ToLower(filepath.Ext(path))
	if ext == ".pdf" {
		return readPDFContent(path)
	}
	content, err := os.ReadFile(path)
	if err != nil {
		return "", err
	}
	return string(content), nil
}

func readPDFContent(path string) (string, error) {
	r, err := pdf.Open(path)
	if err != nil {
		return "", fmt.Errorf("failed to parse pdf: %w", err)
	}
	var b strings.Builder
	for i := 1; i <= r.NumPage(); i++ {
		p := r.Page(i)
		if p.V.IsNull() {
			continue
		}
		if text, err := p.GetPlainText(nil); err == nil {
			b.WriteString(text)
			b.WriteString("\n")
		}
	}
	return b.String(), nil
}

func getFileUpdatedAt(path string) string {
	if info, err := os.Stat(path); err == nil {
		return strconv.FormatInt(info.ModTime().Unix(), 10)
	}
	return strconv.FormatInt(time.Now().Unix(), 10)
}

func getFileCategory(path string) string {
	ext := strings.ToLower(filepath.Ext(path))
	if ext == ".pdf" || ext == ".md" || ext == ".txt" {
		return "document"
	}
	return "code"
}

func (opts Options) prepareRecords(ctx context.Context, chunks []Chunk, relPath, category, updatedAt, hash string, priority float32) ([]db.Record, error) {
	var texts []string
	for _, chunk := range chunks {
		texts = append(texts, chunk.ContextualString)
	}

	if len(texts) == 0 {
		return opts.addFileMetaOnly(relPath, updatedAt, hash), nil
	}

	embs, err := opts.Embedder.EmbedBatch(ctx, texts)
	if err != nil {
		opts.Logger.Warn("Batch embedding failed, falling back to sequential", "error", err, "path", relPath)
		embs = opts.embedSequentially(ctx, texts)
	}

	var records []db.Record
	for i, chunk := range chunks {
		if i >= len(embs) {
			break
		}
		records = append(records, opts.createChunkRecord(chunk, i, relPath, category, updatedAt, hash, priority, embs[i]))
	}

	records = append(records, opts.createFileMetaRecord(relPath, updatedAt, hash))
	return records, nil
}

func (opts Options) embedSequentially(ctx context.Context, texts []string) [][]float32 {
	embs := make([][]float32, 0, len(texts))
	for _, text := range texts {
		emb, err := opts.Embedder.Embed(ctx, text)
		if err != nil {
			opts.Logger.Error("Single embedding failed", "error", err)
			continue
		}
		embs = append(embs, emb)
	}
	return embs
}

func (opts Options) createChunkRecord(chunk Chunk, index int, relPath, category, updatedAt, hash string, priority float32, emb []float32) db.Record {
	relJSON, _ := json.Marshal(chunk.Relationships)
	symJSON, _ := json.Marshal(chunk.Symbols)
	callsJSON, _ := json.Marshal(chunk.Calls)
	structJSON, _ := json.Marshal(chunk.StructuralMetadata)

	name := ""
	if len(chunk.Symbols) > 0 {
		name = chunk.Symbols[0]
	}

	metadata := map[string]string{
		"path":                relPath,
		"project_id":          opts.Config.ProjectRoot,
		"category":            category,
		"updated_at":          updatedAt,
		"hash":                hash,
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

	return db.Record{
		ID:        fmt.Sprintf("chunk:%s:%s:%d", opts.Config.ProjectRoot, relPath, index),
		Content:   chunk.Content,
		Embedding: emb,
		Metadata:  metadata,
	}
}

func (opts Options) createFileMetaRecord(relPath, updatedAt, hash string) db.Record {
	dummyEmb := make([]float32, opts.Store.Dimension)
	return db.Record{
		ID:        fmt.Sprintf("filemeta:%s:%s", opts.Config.ProjectRoot, relPath),
		Content:   "",
		Embedding: dummyEmb,
		Metadata: map[string]string{
			"type":       "file_meta",
			"path":       relPath,
			"project_id": opts.Config.ProjectRoot,
			"hash":       hash,
			"updated_at": updatedAt,
		},
	}
}

func (opts Options) addFileMetaOnly(relPath, updatedAt, hash string) []db.Record {
	return []db.Record{opts.createFileMetaRecord(relPath, updatedAt, hash)}
}

// IndexSingleFile indexes a single file and updates the database.
func IndexSingleFile(ctx context.Context, path string, opts Options) (IndexSummary, error) {
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
		_ = opts.Store.DeleteByPath(ctx, relPath, opts.Config.ProjectRoot)
		_ = opts.Store.Insert(ctx, res.Records)
	}
	return IndexSummary{Status: "completed", FilesIndexed: 1}, nil
}

var (
	// AllowExts lists all file extensions that the scanner is permitted to process.
	AllowExts    = []string{".ts", ".tsx", ".js", ".jsx", ".prisma", ".json", ".css", ".html", ".md", ".env", ".yml", ".yaml", ".go", ".py", ".rs", ".sh", ".txt", ".pdf"}
	allowExtsMap = make(map[string]struct{})

	ignoredDirsMap = map[string]struct{}{
		"node_modules": {}, ".git": {}, ".next": {}, ".turbo": {}, "dist": {},
		"build": {}, "generated": {}, "coverage": {}, "out": {}, "vendor": {}, ".vector-db": {}, ".data": {},
	}

	ignoredExactFiles = []string{
		"package-lock.json", "pnpm-lock.yaml", "yarn.lock", "go.sum",
	}

	ignoredSuffixes = []string{
		".map", ".min.js", ".svg", ".png", ".jpg", ".jpeg", ".gif", ".ico", ".pdf",
	}
)

func init() {
	for _, ext := range AllowExts {
		allowExtsMap[ext] = struct{}{}
	}
}

// ScanFiles recursively discovers all allowed files within the specified root directory.
func ScanFiles(root string) ([]string, error) {
	ignorer := initIgnorer(root)
	return walkFiles(root, ignorer)
}

func initIgnorer(root string) *ignore.GitIgnore {
	if _, err := os.Stat(filepath.Join(root, ".vector-ignore")); err == nil {
		ignorer, _ := ignore.CompileIgnoreFile(filepath.Join(root, ".vector-ignore"))
		return ignorer
	}
	if _, err := os.Stat(filepath.Join(root, ".gitignore")); err == nil {
		ignorer, _ := ignore.CompileIgnoreFile(filepath.Join(root, ".gitignore"))
		return ignorer
	}
	return nil
}

func walkFiles(root string, ignorer *ignore.GitIgnore) ([]string, error) {
	var files []string
	err := filepath.Walk(root, func(path string, info os.FileInfo, err error) error {
		if err != nil {
			return err
		}
		relPath, _ := filepath.Rel(root, path)
		if relPath == "." {
			return nil
		}
		if info.IsDir() {
			if IsIgnoredDir(info.Name()) || (ignorer != nil && ignorer.MatchesPath(relPath)) {
				return filepath.SkipDir
			}
			return nil
		}
		if IsIgnoredFile(info.Name()) || (ignorer != nil && ignorer.MatchesPath(relPath)) {
			return nil
		}
		ext := filepath.Ext(path)
		if _, allowed := allowExtsMap[ext]; allowed {
			files = append(files, path)
		}
		return nil
	})
	return files, err
}

// IsIgnoredDir returns true if the specified directory should be skipped during scanning.
func IsIgnoredDir(name string) bool {
	_, ignored := ignoredDirsMap[name]
	return ignored
}

// IsIgnoredFile returns true if the specified file should be skipped during scanning.
func IsIgnoredFile(name string) bool {
	for _, f := range ignoredExactFiles {
		if strings.EqualFold(name, f) {
			return true
		}
	}

	for _, s := range ignoredSuffixes {
		if len(name) >= len(s) {
			if strings.EqualFold(name[len(name)-len(s):], s) {
				return true
			}
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

// GetHash computes the SHA256 content hash for a given file path.
func GetHash(path string) (string, error) {
	f, err := os.Open(path)
	if err != nil {
		return "", err
	}
	defer func() { _ = f.Close() }()

	h := sha256.New()
	if _, err := io.Copy(h, f); err != nil {
		return "", err
	}

	return hex.EncodeToString(h.Sum(nil)), nil
}
