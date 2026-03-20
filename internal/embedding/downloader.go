package embedding

import (
	"fmt"
	"io"
	"net/http"
	"os"
	"path/filepath"
)

type ModelConfig struct {
	OnnxURL      string
	TokenizerURL string
	Filename     string
	Dimension    int
}

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
}

func GetModelConfig(modelName string) (ModelConfig, error) {
	mc, ok := Models[modelName]
	if !ok {
		return ModelConfig{}, fmt.Errorf("unsupported model %q, choose from: Xenova/bge-m3, BAAI/bge-small-en-v1.5, BAAI/bge-base-en-v1.5", modelName)
	}
	return mc, nil
}

func EnsureModel(modelsDir, modelName string) (ModelConfig, error) {
	mc, err := GetModelConfig(modelName)
	if err != nil {
		return mc, err
	}

	modelPath := filepath.Join(modelsDir, mc.Filename)
	tokenizerPath := filepath.Join(modelsDir, mc.Filename+".tokenizer.json")

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

func downloadFile(url string, dest string) error {
	// Ensure directory exists
	if err := os.MkdirAll(filepath.Dir(dest), 0755); err != nil {
		return err
	}

	tempDest := dest + ".tmp"
	resp, err := http.Get(url)
	if err != nil {
		return err
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		return fmt.Errorf("bad status: %s", resp.Status)
	}

	out, err := os.Create(tempDest)
	if err != nil {
		return err
	}

	_, err = io.Copy(out, resp.Body)
	out.Close()
	if err != nil {
		os.Remove(tempDest)
		return err
	}

	return os.Rename(tempDest, dest)
}
