package embedding

import (
	"fmt"
	"strings"
)

// ModelType categorizes embedding models by their specialization.
type ModelType string

const (
	// ModelTypeGeneral is a model used for general-purpose text embeddings.
	ModelTypeGeneral ModelType = "general"  // General-purpose embeddings
	ModelTypeCode    ModelType = "code"     // Code-specific embeddings
	ModelTypeDomain  ModelType = "domain"   // Domain-specific (e.g., legal, medical)
	ModelTypeRerank  ModelType = "reranker" // Reranking models
)

// ModelCapability describes what a model can do.
type ModelCapability struct {
	Type             ModelType
	SupportedLangs   []string // Programming languages (for code models)
	MaxSeqLength     int
	RecommendedFor   []string // Use case recommendations
	AverageLatencyMs int      // Approximate latency on CPU
}

// MultiModelConfig extends ModelConfig with additional metadata.
type MultiModelConfig struct {
	ModelConfig
	Capability ModelCapability
	Aliases    []string // Alternative names for the model
	Deprecated bool     // Whether this model is deprecated
}

// MultiModelRegistry maintains all available models with their capabilities.
var MultiModelRegistry = map[string]MultiModelConfig{
	// General-purpose embedding models
	"Xenova/bge-m3": {
		ModelConfig: ModelConfig{
			OnnxURL:      "https://huggingface.co/Xenova/bge-m3/resolve/main/onnx/model_quantized.onnx",
			TokenizerURL: "https://huggingface.co/Xenova/bge-m3/resolve/main/tokenizer.json",
			Filename:     "bge-m3-q4.onnx",
			Dimension:    1024,
		},
		Capability: ModelCapability{
			Type:             ModelTypeGeneral,
			MaxSeqLength:     8192,
			RecommendedFor:   []string{"semantic-search", "retrieval", "general"},
			AverageLatencyMs: 50,
		},
		Aliases: []string{"bge-m3", "BAAI/bge-m3"},
	},
	"BAAI/bge-small-en-v1.5": {
		ModelConfig: ModelConfig{
			OnnxURL:      "https://huggingface.co/Xenova/bge-small-en-v1.5/resolve/main/onnx/model_quantized.onnx",
			TokenizerURL: "https://huggingface.co/Xenova/bge-small-en-v1.5/resolve/main/tokenizer.json",
			Filename:     "bge-small-en-v1.5-q4.onnx",
			Dimension:    384,
		},
		Capability: ModelCapability{
			Type:             ModelTypeGeneral,
			MaxSeqLength:     512,
			RecommendedFor:   []string{"fast-search", "low-latency"},
			AverageLatencyMs: 15,
		},
		Aliases: []string{"bge-small", "BAAI/bge-small"},
	},
	"BAAI/bge-base-en-v1.5": {
		ModelConfig: ModelConfig{
			OnnxURL:      "https://huggingface.co/Xenova/bge-base-en-v1.5/resolve/main/onnx/model_quantized.onnx",
			TokenizerURL: "https://huggingface.co/Xenova/bge-base-en-v1.5/resolve/main/tokenizer.json",
			Filename:     "bge-base-en-v1.5-q4.onnx",
			Dimension:    768,
		},
		Capability: ModelCapability{
			Type:             ModelTypeGeneral,
			MaxSeqLength:     512,
			RecommendedFor:   []string{"balanced", "semantic-search"},
			AverageLatencyMs: 30,
		},
		Aliases: []string{"bge-base", "BAAI/bge-base"},
	},

	// Code-specific embedding models
	"Xenova/jina-embeddings-v2-base-code": {
		ModelConfig: ModelConfig{
			OnnxURL:      "https://huggingface.co/Xenova/jina-embeddings-v2-base-code/resolve/main/onnx/model_quantized.onnx",
			TokenizerURL: "https://huggingface.co/Xenova/jina-embeddings-v2-base-code/resolve/main/tokenizer.json",
			Filename:     "jina_code_v2/model_quantized.onnx",
			Dimension:    768,
		},
		Capability: ModelCapability{
			Type: ModelTypeCode,
			SupportedLangs: []string{
				"go", "python", "javascript", "typescript", "java",
				"c", "cpp", "rust", "ruby", "php", "swift", "kotlin",
			},
			MaxSeqLength:     8192,
			RecommendedFor:   []string{"code-search", "code-comprehension", "snippet-retrieval"},
			AverageLatencyMs: 40,
		},
		Aliases: []string{"jina-code", "jina-embeddings-v2-base-code"},
	},
	"microsoft/codebert-base": {
		ModelConfig: ModelConfig{
			OnnxURL:      "https://huggingface.co/onnx-community/codebert-base-ONNX/resolve/main/onnx/model_quantized.onnx",
			TokenizerURL: "https://huggingface.co/onnx-community/codebert-base-ONNX/resolve/main/tokenizer.json",
			Filename:     "codebert_base/model_quantized.onnx",
			Dimension:    768,
		},
		Capability: ModelCapability{
			Type: ModelTypeCode,
			SupportedLangs: []string{
				"go", "python", "javascript", "typescript", "java",
				"c", "cpp", "csharp", "ruby", "php",
			},
			MaxSeqLength:     512,
			RecommendedFor:   []string{"code-search", "code-similarity"},
			AverageLatencyMs: 25,
		},
		Aliases: []string{"codebert", "CodeBERT"},
	},
	"bigcode/starencoder": {
		ModelConfig: ModelConfig{
			OnnxURL:      "https://huggingface.co/onnx-community/starencoder-ONNX/resolve/main/onnx/model_quantized.onnx",
			TokenizerURL: "https://huggingface.co/onnx-community/starencoder-ONNX/resolve/main/tokenizer.json",
			Filename:     "starencoder/model_quantized.onnx",
			Dimension:    768,
		},
		Capability: ModelCapability{
			Type: ModelTypeCode,
			SupportedLangs: []string{
				"go", "python", "javascript", "typescript", "java",
				"c", "cpp", "csharp", "rust", "ruby", "php", "scala",
			},
			MaxSeqLength:     1024,
			RecommendedFor:   []string{"code-completion", "code-search", "large-codebase"},
			AverageLatencyMs: 35,
		},
		Aliases: []string{"starencoder", "StarCoder-Embed"},
	},
	"IBM/granite-embedding-english-r2": {
		ModelConfig: ModelConfig{
			OnnxURL:      "https://huggingface.co/onnx-community/granite-embedding-english-r2-ONNX/resolve/main/onnx/model_quantized.onnx",
			TokenizerURL: "https://huggingface.co/onnx-community/granite-embedding-english-r2-ONNX/resolve/main/tokenizer.json",
			Filename:     "IBM_granite_r2/model_quantized.onnx",
			Dimension:    768,
		},
		Capability: ModelCapability{
			Type: ModelTypeCode,
			SupportedLangs: []string{
				"go", "python", "javascript", "typescript", "java",
				"c", "cpp", "rust", "ruby",
			},
			MaxSeqLength:     512,
			RecommendedFor:   []string{"enterprise-code", "code-search"},
			AverageLatencyMs: 28,
		},
		Aliases: []string{"granite-embedding", "granite-r2"},
	},

	// Reranker models
	"cross-encoder/ms-marco-MiniLM-L-6-v2": {
		ModelConfig: ModelConfig{
			OnnxURL:      "https://huggingface.co/Xenova/ms-marco-MiniLM-L-6-v2/resolve/main/onnx/model_quantized.onnx",
			TokenizerURL: "https://huggingface.co/Xenova/ms-marco-MiniLM-L-6-v2/resolve/main/tokenizer.json",
			Filename:     "ms-marco-MiniLM-L-6-v2-q4.onnx",
			Dimension:    1,
			IsReranker:   true,
		},
		Capability: ModelCapability{
			Type:             ModelTypeRerank,
			MaxSeqLength:     512,
			RecommendedFor:   []string{"general-reranking", "fast-rerank"},
			AverageLatencyMs: 10,
		},
		Aliases: []string{"ms-marco", "MiniLM-rerank"},
	},
	"Xenova/bge-reranker-base": {
		ModelConfig: ModelConfig{
			OnnxURL:      "https://huggingface.co/Xenova/bge-reranker-base/resolve/main/onnx/model_quantized.onnx",
			TokenizerURL: "https://huggingface.co/Xenova/bge-reranker-base/resolve/main/tokenizer.json",
			Filename:     "bge-reranker-base-q4.onnx",
			Dimension:    1,
			IsReranker:   true,
		},
		Capability: ModelCapability{
			Type:             ModelTypeRerank,
			MaxSeqLength:     512,
			RecommendedFor:   []string{"semantic-reranking", "high-accuracy"},
			AverageLatencyMs: 12,
		},
		Aliases: []string{"bge-reranker"},
	},
	"Xenova/bge-reranker-v2-m3": {
		ModelConfig: ModelConfig{
			OnnxURL:      "https://huggingface.co/onnx-community/bge-reranker-v2-m3-ONNX/resolve/main/onnx/model_quantized.onnx",
			TokenizerURL: "https://huggingface.co/onnx-community/bge-reranker-v2-m3-ONNX/resolve/main/tokenizer.json",
			Filename:     "bge_reranker_v2_m3/model_quantized.onnx",
			Dimension:    1,
			IsReranker:   true,
		},
		Capability: ModelCapability{
			Type:             ModelTypeRerank,
			MaxSeqLength:     8192,
			RecommendedFor:   []string{"multilingual-reranking", "long-context"},
			AverageLatencyMs: 25,
		},
		Aliases: []string{"bge-reranker-v2", "bge-reranker-m3"},
	},
	"Xenova/bge-reranker-v2-gemma": {
		ModelConfig: ModelConfig{
			OnnxURL:      "https://huggingface.co/onnx-community/bge-reranker-v2-gemma-ONNX/resolve/main/onnx/model_quantized.onnx",
			TokenizerURL: "https://huggingface.co/onnx-community/bge-reranker-v2-gemma-ONNX/resolve/main/tokenizer.json",
			Filename:     "bge_reranker_v2_gemma/model_quantized.onnx",
			Dimension:    1,
			IsReranker:   true,
		},
		Capability: ModelCapability{
			Type:             ModelTypeRerank,
			MaxSeqLength:     8192,
			RecommendedFor:   []string{"high-accuracy", "complex-queries"},
			AverageLatencyMs: 45,
		},
		Aliases: []string{"bge-reranker-gemma"},
	},
	"Xenova/jina-reranker-v2-base-multilingual": {
		ModelConfig: ModelConfig{
			OnnxURL:      "https://huggingface.co/jinaai/jina-reranker-v2-base-multilingual/resolve/main/onnx/model_int8.onnx",
			TokenizerURL: "https://huggingface.co/jinaai/jina-reranker-v2-base-multilingual/resolve/main/tokenizer.json",
			Filename:     "jina_reranker_v2/model_int8.onnx",
			Dimension:    1,
			IsReranker:   true,
		},
		Capability: ModelCapability{
			Type:             ModelTypeRerank,
			MaxSeqLength:     1024,
			RecommendedFor:   []string{"multilingual", "code-reranking", "fast-rerank"},
			AverageLatencyMs: 15,
		},
		Aliases: []string{"jina-reranker", "jina-reranker-v2"},
	},
}

// ResolveModelName resolves model aliases to canonical names.
func ResolveModelName(name string) (string, error) {
	// Check canonical name first
	if _, ok := MultiModelRegistry[name]; ok {
		return name, nil
	}

	// Check aliases
	for canonical, cfg := range MultiModelRegistry {
		for _, alias := range cfg.Aliases {
			if strings.EqualFold(alias, name) {
				return canonical, nil
			}
		}
	}

	return "", fmt.Errorf("unknown model: %s", name)
}

// GetMultiModelConfig retrieves a model configuration by name or alias.
func GetMultiModelConfig(name string) (MultiModelConfig, error) {
	canonicalName, err := ResolveModelName(name)
	if err != nil {
		return MultiModelConfig{}, err
	}

	cfg, ok := MultiModelRegistry[canonicalName]
	if !ok {
		return MultiModelConfig{}, fmt.Errorf("model not found in registry: %s", canonicalName)
	}

	return cfg, nil
}

// ListModelsByType returns all models of a specific type.
func ListModelsByType(modelType ModelType) []string {
	var models []string
	for name, cfg := range MultiModelRegistry {
		if cfg.Capability.Type == modelType {
			models = append(models, name)
		}
	}
	return models
}

// ListCodeModels returns all code-specific embedding models.
func ListCodeModels() []string {
	return ListModelsByType(ModelTypeCode)
}

// ListRerankerModels returns all reranker models.
func ListRerankerModels() []string {
	return ListModelsByType(ModelTypeRerank)
}

// ListGeneralModels returns all general-purpose embedding models.
func ListGeneralModels() []string {
	return ListModelsByType(ModelTypeGeneral)
}

// RecommendModelForLanguage recommends the best embedding model for a programming language.
func RecommendModelForLanguage(lang string) string {
	lang = strings.ToLower(lang)

	// Prefer code-specific models that support the language
	for name, cfg := range MultiModelRegistry {
		if cfg.Capability.Type == ModelTypeCode {
			for _, supportedLang := range cfg.Capability.SupportedLangs {
				if strings.EqualFold(supportedLang, lang) {
					return name
				}
			}
		}
	}

	// Fall back to jina-code as it has the broadest language support
	return "Xenova/jina-embeddings-v2-base-code"
}

// RecommendModelForUseCase recommends the best model for a specific use case.
func RecommendModelForUseCase(useCase string) string {
	useCase = strings.ToLower(useCase)

	// Map use cases to recommended models
	useCaseMap := map[string]string{
		"code-search":     "Xenova/jina-embeddings-v2-base-code",
		"semantic-search": "Xenova/bge-m3",
		"fast-search":     "BAAI/bge-small-en-v1.5",
		"code-completion": "bigcode/starencoder",
		"enterprise-code": "IBM/granite-embedding-english-r2",
		"rerank":          "Xenova/bge-reranker-v2-m3",
		"code-rerank":     "Xenova/jina-reranker-v2-base-multilingual",
	}

	if model, ok := useCaseMap[useCase]; ok {
		return model
	}

	// Default to BGE-M3 for general use
	return "Xenova/bge-m3"
}
