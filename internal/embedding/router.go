// Package embedding provides tools for generating and managing vector embeddings.
package embedding

import (
	"context"
	"fmt"
	"path/filepath"
	"strings"
	"sync"
)

// ModelRouterConfig configures the model routing behavior.
type ModelRouterConfig struct {
	DefaultEmbeddingModel string // Primary embedding model
	CodeEmbeddingModel    string // Model for code content
	DocEmbeddingModel     string // Model for documentation
	RerankerModel         string // Reranker model
	EnableLanguageRouting bool   // Route by programming language
	EnableAdaptiveRouting bool   // Adapt based on query patterns
}

// DefaultRouterConfig returns sensible defaults.
func DefaultRouterConfig() ModelRouterConfig {
	return ModelRouterConfig{
		DefaultEmbeddingModel: "Xenova/bge-m3",
		CodeEmbeddingModel:    "Xenova/jina-embeddings-v2-base-code",
		DocEmbeddingModel:     "Xenova/bge-m3",
		RerankerModel:         "Xenova/bge-reranker-v2-m3",
		EnableLanguageRouting: true,
		EnableAdaptiveRouting: true,
	}
}

// ModelRouter intelligently selects models based on content type.
type ModelRouter struct {
	config    ModelRouterConfig
	modelsDir string
	poolSize  int

	mu         sync.RWMutex
	pools      map[string]*EmbedderPool // model name -> pool
	mainPool   *EmbedderPool            // default pool for backward compatibility
	rerankPool *EmbedderPool

	initialized bool
}

// NewModelRouter creates a new model router.
func NewModelRouter(config ModelRouterConfig, modelsDir string, poolSize int) *ModelRouter {
	return &ModelRouter{
		config:    config,
		modelsDir: modelsDir,
		poolSize:  poolSize,
		pools:     make(map[string]*EmbedderPool),
	}
}

// Initialize loads all configured models.
func (r *ModelRouter) Initialize(ctx context.Context) error {
	r.mu.Lock()
	defer r.mu.Unlock()

	if r.initialized {
		return nil
	}

	// Resolve model names
	defaultModel, err := ResolveModelName(r.config.DefaultEmbeddingModel)
	if err != nil {
		return fmt.Errorf("invalid default model: %w", err)
	}
	r.config.DefaultEmbeddingModel = defaultModel

	codeModel, err := ResolveModelName(r.config.CodeEmbeddingModel)
	if err != nil {
		// Fall back to default if code model is invalid
		codeModel = defaultModel
	}
	r.config.CodeEmbeddingModel = codeModel

	// Initialize default model pool
	defaultCfg, err := EnsureModel(r.modelsDir, defaultModel)
	if err != nil {
		return fmt.Errorf("failed to ensure default model: %w", err)
	}

	mainPool, err := NewEmbedderPool(ctx, r.modelsDir, r.poolSize, defaultCfg, nil)
	if err != nil {
		return fmt.Errorf("failed to create main pool: %w", err)
	}
	r.mainPool = mainPool
	r.pools[defaultModel] = mainPool

	// Initialize code model pool if different from default
	if codeModel != defaultModel {
		codeCfg, err := EnsureModel(r.modelsDir, codeModel)
		if err != nil {
			// Non-fatal: we'll fall back to default
			r.config.CodeEmbeddingModel = defaultModel
		} else {
			codePool, err := NewEmbedderPool(ctx, r.modelsDir, r.poolSize, codeCfg, nil)
			if err != nil {
				// Non-fatal: we'll fall back to default
				r.config.CodeEmbeddingModel = defaultModel
			} else {
				r.pools[codeModel] = codePool
			}
		}
	}

	// Initialize reranker if configured
	if r.config.RerankerModel != "" {
		rerankModel, err := ResolveModelName(r.config.RerankerModel)
		if err == nil {
			rerankCfg, err := EnsureModel(r.modelsDir, rerankModel)
			if err == nil {
				// Create a pool with just reranker
				rerankPool, err := NewEmbedderPool(ctx, r.modelsDir, r.poolSize, defaultCfg, &rerankCfg)
				if err == nil {
					r.rerankPool = rerankPool
				}
			}
		}
	}

	r.initialized = true
	return nil
}

// GetEmbedder returns an appropriate embedder for the given content.
func (r *ModelRouter) GetEmbedder(contentType ContentType, language string) (*Embedder, error) {
	r.mu.RLock()
	defer r.mu.RUnlock()

	if !r.initialized {
		return nil, fmt.Errorf("model router not initialized")
	}

	// Select model based on content type
	modelName := r.selectModel(contentType, language)

	pool, ok := r.pools[modelName]
	if !ok {
		// Fall back to main pool
		pool = r.mainPool
	}

	return pool.Get(context.Background())
}

// PutEmbedder returns an embedder to the appropriate pool.
func (r *ModelRouter) PutEmbedder(emb *Embedder) {
	// Determine which pool this embedder belongs to based on its model
	modelName := emb.embSess.modelName

	r.mu.RLock()
	pool, ok := r.pools[modelName]
	if !ok {
		pool = r.mainPool
	}
	r.mu.RUnlock()

	pool.Put(emb)
}

// selectModel chooses the best model for the content.
func (r *ModelRouter) selectModel(contentType ContentType, language string) string {
	switch contentType {
	case ContentTypeCode:
		if r.config.EnableLanguageRouting && language != "" {
			// Check if code model supports this language
			if cfg, err := GetMultiModelConfig(r.config.CodeEmbeddingModel); err == nil {
				for _, lang := range cfg.Capability.SupportedLangs {
					if strings.EqualFold(lang, language) {
						return r.config.CodeEmbeddingModel
					}
				}
			}
		}
		return r.config.CodeEmbeddingModel

	case ContentTypeDoc:
		return r.config.DocEmbeddingModel

	case ContentTypeQuery:
		// For queries, prefer the same model as code for better matching
		return r.config.CodeEmbeddingModel

	default:
		return r.config.DefaultEmbeddingModel
	}
}

// GetReranker returns an embedder with reranking capability.
func (r *ModelRouter) GetReranker() (*Embedder, error) {
	r.mu.RLock()
	defer r.mu.RUnlock()

	if r.rerankPool == nil {
		return nil, fmt.Errorf("reranker not configured")
	}

	return r.rerankPool.Get(context.Background())
}

// PutReranker returns a reranker embedder to the pool.
func (r *ModelRouter) PutReranker(emb *Embedder) {
	r.mu.RLock()
	defer r.mu.RUnlock()

	if r.rerankPool != nil {
		r.rerankPool.Put(emb)
	}
}

// Close shuts down all pools.
func (r *ModelRouter) Close() {
	r.mu.Lock()
	defer r.mu.Unlock()

	for _, pool := range r.pools {
		pool.Close()
	}

	if r.rerankPool != nil {
		r.rerankPool.Close()
	}

	r.pools = make(map[string]*EmbedderPool)
	r.mainPool = nil
	r.rerankPool = nil
	r.initialized = false
}

// DetectContentType analyzes content to determine its type.
func DetectContentType(content string, filePath string) ContentType {
	// Check file extension
	ext := strings.ToLower(filepath.Ext(filePath))

	// Code extensions
	codeExts := map[string]bool{
		".go": true, ".py": true, ".js": true, ".ts": true, ".jsx": true,
		".tsx": true, ".java": true, ".c": true, ".cpp": true, ".cc": true,
		".rs": true, ".rb": true, ".php": true, ".swift": true, ".kt": true,
		".scala": true, ".cs": true, ".sh": true, ".bash": true,
	}

	if codeExts[ext] {
		return ContentTypeCode
	}

	// Documentation extensions
	docExts := map[string]bool{
		".md": true, ".rst": true, ".txt": true, ".adoc": true,
	}

	if docExts[ext] {
		return ContentTypeDoc
	}

	// Config extensions
	configExts := map[string]bool{
		".json": true, ".yaml": true, ".yml": true, ".toml": true,
		".ini": true, ".cfg": true, ".conf": true, ".env": true,
	}

	if configExts[ext] {
		return ContentTypeConfig
	}

	// Try to detect from content
	if isCodeContent(content) {
		return ContentTypeCode
	}

	return ContentTypeGeneral
}

// DetectLanguageFromPath extracts the programming language from a file path.
func DetectLanguageFromPath(path string) string {
	ext := strings.ToLower(filepath.Ext(path))

	langMap := map[string]string{
		".go":    "go",
		".py":    "python",
		".js":    "javascript",
		".ts":    "typescript",
		".jsx":   "javascript",
		".tsx":   "typescript",
		".java":  "java",
		".c":     "c",
		".cpp":   "cpp",
		".cc":    "cpp",
		".cxx":   "cpp",
		".rs":    "rust",
		".rb":    "ruby",
		".php":   "php",
		".swift": "swift",
		".kt":    "kotlin",
		".scala": "scala",
		".cs":    "csharp",
		".sh":    "bash",
		".bash":  "bash",
	}

	if lang, ok := langMap[ext]; ok {
		return lang
	}

	return ""
}

// codeIndicators are simple heuristics to detect if content looks like code.
var codeIndicators = []string{
	"func ", "function ", "def ", "class ", "public ", "private ",
	"import ", "package ", "require(", "const ", "let ", "var ",
	"if (", "for (", "while (", "return ", "=>",
}

// isCodeContent performs a heuristic check if content looks like code.
func isCodeContent(content string) bool {
	lower := strings.ToLower(content)
	matches := 0
	for _, indicator := range codeIndicators {
		// codeIndicators are already lowercased statically above
		if strings.Contains(lower, indicator) {
			matches++
			if matches >= 2 {
				return true
			}
		}
	}

	// If multiple code indicators are present, likely code
	return false
}

// GetStats returns statistics about the model router.
func (r *ModelRouter) GetStats() map[string]any {
	r.mu.RLock()
	defer r.mu.RUnlock()

	stats := map[string]any{
		"initialized": r.initialized,
		"models":      make([]string, 0, len(r.pools)),
	}

	for name := range r.pools {
		stats["models"] = append(stats["models"].([]string), name)
	}

	if r.rerankPool != nil {
		stats["reranker"] = r.config.RerankerModel
	}

	return stats
}
