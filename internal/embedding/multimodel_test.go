package embedding

import (
	"testing"
)

func TestResolveModelName(t *testing.T) {
	tests := []struct {
		input    string
		expected string
		hasError bool
	}{
		{"Xenova/bge-m3", "Xenova/bge-m3", false},
		{"bge-m3", "Xenova/bge-m3", false},
		{"BGE-M3", "Xenova/bge-m3", false},
		{"jina-code", "Xenova/jina-embeddings-v2-base-code", false},
		{"codebert", "microsoft/codebert-base", false},
		{"unknown-model", "", true},
	}

	for _, tt := range tests {
		t.Run(tt.input, func(t *testing.T) {
			result, err := ResolveModelName(tt.input)

			if tt.hasError {
				if err == nil {
					t.Error("Expected error, got none")
				}
				return
			}

			if err != nil {
				t.Errorf("Unexpected error: %v", err)
				return
			}

			if result != tt.expected {
				t.Errorf("Expected %q, got %q", tt.expected, result)
			}
		})
	}
}

func TestGetMultiModelConfig(t *testing.T) {
	// Test with canonical name
	cfg, err := GetMultiModelConfig("Xenova/bge-m3")
	if err != nil {
		t.Fatalf("Unexpected error: %v", err)
	}

	if cfg.Dimension != 1024 {
		t.Errorf("Expected dimension 1024, got %d", cfg.Dimension)
	}

	if cfg.Capability.Type != ModelTypeGeneral {
		t.Errorf("Expected type general, got %s", cfg.Capability.Type)
	}

	// Test with alias
	cfg2, err := GetMultiModelConfig("jina-code")
	if err != nil {
		t.Fatalf("Unexpected error for alias: %v", err)
	}

	if cfg2.Capability.Type != ModelTypeCode {
		t.Errorf("Expected type code, got %s", cfg2.Capability.Type)
	}

	if len(cfg2.Capability.SupportedLangs) == 0 {
		t.Error("Code model should have supported languages")
	}
}

func TestListModelsByType(t *testing.T) {
	codeModels := ListCodeModels()
	if len(codeModels) == 0 {
		t.Error("Expected at least one code model")
	}

	rerankModels := ListRerankerModels()
	if len(rerankModels) == 0 {
		t.Error("Expected at least one reranker model")
	}

	generalModels := ListGeneralModels()
	if len(generalModels) == 0 {
		t.Error("Expected at least one general model")
	}
}

func TestRecommendModelForLanguage(t *testing.T) {
	tests := []struct {
		lang         string
		expectCode   bool
	}{
		{"go", true},
		{"python", true},
		{"javascript", true},
		{"typescript", true},
		{"rust", true},
		{"unknown-lang", true}, // Should fall back to jina-code
	}

	for _, tt := range tests {
		t.Run(tt.lang, func(t *testing.T) {
			model := RecommendModelForLanguage(tt.lang)

			cfg, err := GetMultiModelConfig(model)
			if err != nil {
				t.Errorf("Recommended model %s not found: %v", model, err)
				return
			}

			if tt.expectCode && cfg.Capability.Type != ModelTypeCode {
				t.Errorf("Expected code model for %s, got %s", tt.lang, cfg.Capability.Type)
			}
		})
	}
}

func TestRecommendModelForUseCase(t *testing.T) {
	tests := []struct {
		useCase string
	}{
		{"code-search"},
		{"semantic-search"},
		{"fast-search"},
		{"rerank"},
		{"unknown-usecase"},
	}

	for _, tt := range tests {
		t.Run(tt.useCase, func(t *testing.T) {
			model := RecommendModelForUseCase(tt.useCase)

			// Verify the model exists
			_, err := GetMultiModelConfig(model)
			if err != nil {
				t.Errorf("Recommended model %s not found: %v", model, err)
			}
		})
	}
}

func TestDetectContentType(t *testing.T) {
	tests := []struct {
		path     string
		content  string
		expected ContentType
	}{
		{"main.go", "package main\nfunc main() {}", ContentTypeCode},
		{"README.md", "# Documentation", ContentTypeDoc},
		{"config.json", `{"key": "value"}`, ContentTypeConfig},
		{"main.py", "def hello():\n    pass", ContentTypeCode},
		{"app.js", "function test() { return 1; }", ContentTypeCode},
	}

	for _, tt := range tests {
		t.Run(tt.path, func(t *testing.T) {
			result := DetectContentType(tt.content, tt.path)

			if result != tt.expected {
				t.Errorf("Expected %s for %s, got %s", tt.expected, tt.path, result)
			}
		})
	}
}

func TestDetectLanguageFromPath(t *testing.T) {
	tests := []struct {
		path     string
		expected string
	}{
		{"main.go", "go"},
		{"app.py", "python"},
		{"index.js", "javascript"},
		{"app.ts", "typescript"},
		{"main.rs", "rust"},
		{"App.java", "java"},
		{"unknown.xyz", ""},
	}

	for _, tt := range tests {
		t.Run(tt.path, func(t *testing.T) {
			result := DetectLanguageFromPath(tt.path)

			if result != tt.expected {
				t.Errorf("Expected %q, got %q", tt.expected, result)
			}
		})
	}
}

func TestDefaultRouterConfig(t *testing.T) {
	cfg := DefaultRouterConfig()

	if cfg.DefaultEmbeddingModel == "" {
		t.Error("Default model should be set")
	}

	if cfg.CodeEmbeddingModel == "" {
		t.Error("Code model should be set")
	}

	if cfg.RerankerModel == "" {
		t.Error("Reranker model should be set")
	}
}

func TestDefaultRerankConfig(t *testing.T) {
	cfg := DefaultRerankConfig()

	if cfg.Strategy != RerankStrategyTopK {
		t.Errorf("Expected topk strategy, got %s", cfg.Strategy)
	}

	if cfg.TopK == 0 {
		t.Error("TopK should be > 0")
	}
}

func TestDefaultRerankWeights(t *testing.T) {
	weights := DefaultRerankWeights()

	// Verify weights sum approximately to 1.0
	total := weights.SemanticScore + weights.RerankerScore + weights.RecencyScore + weights.FileScore
	if total < 0.9 || total > 1.1 {
		t.Errorf("Weights should sum to ~1.0, got %f", total)
	}

	if weights.RerankerScore < weights.SemanticScore {
		t.Error("Reranker score should typically be weighted higher than semantic")
	}
}

func TestModelRouterConfig(t *testing.T) {
	router := NewModelRouter(DefaultRouterConfig(), "/tmp/models", 2)

	if router == nil {
		t.Fatal("Expected non-nil router")
	}

	if router.poolSize != 2 {
		t.Errorf("Expected pool size 2, got %d", router.poolSize)
	}

	stats := router.GetStats()
	if stats["initialized"] == true {
		t.Error("Router should not be initialized before Initialize()")
	}
}

func TestTokenize(t *testing.T) {
	tests := []struct {
		input    string
		expected int
	}{
		{"hello world", 2},
		{"one two three four", 4},
		{"", 0},
		{"single", 1},
		{"multiple   spaces", 2},
	}

	for _, tt := range tests {
		t.Run(tt.input, func(t *testing.T) {
			result := tokenize(tt.input)

			if len(result) != tt.expected {
				t.Errorf("Expected %d tokens, got %d", tt.expected, len(result))
			}
		})
	}
}

func TestCodeModelCapabilities(t *testing.T) {
	// Verify jina-code model has expected capabilities
	cfg, err := GetMultiModelConfig("Xenova/jina-embeddings-v2-base-code")
	if err != nil {
		t.Fatalf("Failed to get config: %v", err)
	}

	foundGo := false
	for _, lang := range cfg.Capability.SupportedLangs {
		if lang == "go" {
			foundGo = true
			break
		}
	}

	if !foundGo {
		t.Error("Jina code model should support Go")
	}

	if cfg.Capability.MaxSeqLength != 8192 {
		t.Errorf("Expected max seq length 8192, got %d", cfg.Capability.MaxSeqLength)
	}
}

func TestRerankerModels(t *testing.T) {
	rerankers := ListRerankerModels()

	// Verify all reranker models have IsReranker = true
	for _, name := range rerankers {
		cfg, err := GetMultiModelConfig(name)
		if err != nil {
			t.Errorf("Failed to get config for %s: %v", name, err)
			continue
		}

		if !cfg.IsReranker {
			t.Errorf("Reranker model %s should have IsReranker=true", name)
		}

		if cfg.Capability.Type != ModelTypeRerank {
			t.Errorf("Reranker model %s should have type rerank", name)
		}
	}
}
