// Package embedding provides tools for generating and managing vector embeddings.
package embedding

import (
	"fmt"
	"io"
	"net/http"
	"os"
	"path/filepath"
)

// Models contains all embedding and reranker model presets supported by the runtime downloader.
var Models = map[string]ModelConfig{
	"Xenova/bge-m3": {
		OnnxURL:      "https://huggingface.co/Xenova/bge-m3/resolve/main/onnx/model_quantized.onnx",
		TokenizerURL: "https://huggingface.co/Xenova/bge-m3/resolve/main/tokenizer.json",
		Filename:     "bge-m3-q4.onnx",
		Dimension:    1024,
	},
	"BAAI/bge-small-en-v1.5": {
		OnnxURL:      "https://huggingface.co/Xenova/bge-small-en-v1.5/resolve/main/onnx/model_quantized.onnx",
		TokenizerURL: "https://huggingface.co/Xenova/bge-small-en-v1.5/resolve/main/tokenizer.json",
		Filename:     "bge-small-en-v1.5-q4.onnx",
		Dimension:    384,
	},
	"BAAI/bge-base-en-v1.5": {
		OnnxURL:      "https://huggingface.co/Xenova/bge-base-en-v1.5/resolve/main/onnx/model_quantized.onnx",
		TokenizerURL: "https://huggingface.co/Xenova/bge-base-en-v1.5/resolve/main/tokenizer.json",
		Filename:     "bge-base-en-v1.5-q4.onnx",
		Dimension:    768,
	},
	"cross-encoder/ms-marco-MiniLM-L-6-v2": {
		OnnxURL:      "https://huggingface.co/Xenova/ms-marco-MiniLM-L-6-v2/resolve/main/onnx/model_quantized.onnx",
		TokenizerURL: "https://huggingface.co/Xenova/ms-marco-MiniLM-L-6-v2/resolve/main/tokenizer.json",
		Filename:     "ms-marco-MiniLM-L-6-v2-q4.onnx",
		Dimension:    1,
		IsReranker:   true,
	},
	"Xenova/bge-reranker-base": {
		OnnxURL:      "https://huggingface.co/Xenova/bge-reranker-base/resolve/main/onnx/model_quantized.onnx",
		TokenizerURL: "https://huggingface.co/Xenova/bge-reranker-base/resolve/main/tokenizer.json",
		Filename:     "bge-reranker-base-q4.onnx",
		Dimension:    1,
		IsReranker:   true,
	},
	"Xenova/bge-reranker-v2-m3": {
		OnnxURL:      "https://huggingface.co/onnx-community/bge-reranker-v2-m3-ONNX/resolve/main/onnx/model_quantized.onnx",
		TokenizerURL: "https://huggingface.co/onnx-community/bge-reranker-v2-m3-ONNX/resolve/main/tokenizer.json",
		Filename:     "bge_reranker_v2_m3/model_quantized.onnx",
		Dimension:    1,
		IsReranker:   true,
	},
	"Xenova/bge-reranker-v2-gemma": {
		OnnxURL:      "https://huggingface.co/onnx-community/bge-reranker-v2-gemma-ONNX/resolve/main/onnx/model_quantized.onnx",
		TokenizerURL: "https://huggingface.co/onnx-community/bge-reranker-v2-gemma-ONNX/resolve/main/tokenizer.json",
		Filename:     "bge_reranker_v2_gemma/model_quantized.onnx",
		Dimension:    1,
		IsReranker:   true,
	},
	"Xenova/jina-reranker-v2-base-multilingual": {
		OnnxURL:      "https://huggingface.co/jinaai/jina-reranker-v2-base-multilingual/resolve/main/onnx/model_int8.onnx",
		TokenizerURL: "https://huggingface.co/jinaai/jina-reranker-v2-base-multilingual/resolve/main/tokenizer.json",
		Filename:     "jina_reranker_v2/model_int8.onnx",
		Dimension:    1,
		IsReranker:   true,
	},
	"Xenova/jina-embeddings-v2-base-code": {
		OnnxURL:      "https://huggingface.co/Xenova/jina-embeddings-v2-base-code/resolve/main/onnx/model_quantized.onnx",
		TokenizerURL: "https://huggingface.co/Xina-embeddings-v2-base-code/resolve/main/tokenizer.json",
		Filename:     "jina_code_v2/model_quantized.onnx",
		Dimension:    768,
	},
	"IBM/granite-embedding-english-r2": {
		OnnxURL:      "https://huggingface.co/onnx-community/granite-embedding-english-r2-ONNX/resolve/main/onnx/model_quantized.onnx",
		TokenizerURL: "https://huggingface.co/onnx-community/granite-embedding-english-r2-ONNX/resolve/main/tokenizer.json",
		Filename:     "IBM_granite_r2/model_quantized.onnx",
		Dimension:    768,
	},
}

// GetModelConfig resolves a named model preset.
func GetModelConfig(modelName string) (ModelConfig, error) {
	mc, ok := Models[modelName]
	if !ok {
		return ModelConfig{}, fmt.Errorf("unsupported model %q, choose from: Xenova/bge-m3, BAAI/bge-small-en-v1.5, BAAI/bge-base-en-v1.5, cross-encoder/ms-marco-MiniLM-L-6-v2, Xenova/bge-reranker-base, IBM/granite-embedding-english-r2, Xenova/jina-embeddings-v2-base-code, Xenova/bge-reranker-v2-m3", modelName)
	}
	return mc, nil
}

// EnsureModel ensures the selected model and tokenizer are present locally.
func EnsureModel(modelsDir, modelName string) (ModelConfig, error) {
	mc, err := GetModelConfig(modelName)
	if err != nil {
		return mc, err
	}

	modelPath := filepath.Join(modelsDir, mc.Filename)
	// For models in subdirectories, place tokenizer in the same subdir
	tokenizerPath := filepath.Join(filepath.Dir(modelPath), "tokenizer.json")

	if _, err := os.Stat(modelPath); os.IsNotExist(err) {
		fmt.Fprintf(os.Stderr, "Downloading %s model...\n", modelName)
		if err := downloadFile(mc.OnnxURL, modelPath); err != nil {
			return mc, fmt.Errorf("failed to download model: %w", err)
		}
	}

	if _, err := os.Stat(tokenizerPath); os.IsNotExist(err) {
		fmt.Fprintf(os.Stderr, "Downloading %s tokenizer...\n", modelName)
		if err := downloadFile(mc.TokenizerURL, tokenizerPath); err != nil {
			return mc, fmt.Errorf("failed to download tokenizer: %w", err)
		}
	}

	mc.TokenizerURL = tokenizerPath // reuse field to pass resolved path
	return mc, nil
}

// downloadFile performs an atomic download by writing to a temporary file first.
func downloadFile(url string, dest string) error {
	// Ensure directory exists
	if err := os.MkdirAll(filepath.Dir(dest), 0755); err != nil {
		return fmt.Errorf("failed to create directory: %w", err)
	}

	tempDest := dest + ".tmp"
	resp, err := http.Get(url)
	if err != nil {
		return fmt.Errorf("failed to download file: %w", err)
	}
	defer func() { _ = resp.Body.Close() }()

	if resp.StatusCode != http.StatusOK {
		return fmt.Errorf("bad status: %s", resp.Status)
	}

	out, err := os.Create(tempDest)
	if err != nil {
		return fmt.Errorf("failed to create temp file: %w", err)
	}

	_, err = io.Copy(out, resp.Body)
	_ = out.Close()
	if err != nil {
		_ = os.Remove(tempDest)
		return fmt.Errorf("failed to write temp file: %w", err)
	}

	return os.Rename(tempDest, dest)
}
