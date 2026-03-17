package embedding

import (
	"fmt"
	"io"
	"net/http"
	"os"
	"path/filepath"
)

const (
	OnnxURL      = "https://huggingface.co/Xenova/bge-m3/resolve/main/onnx/model_quantized.onnx"
	TokenizerURL = "https://huggingface.co/Xenova/bge-m3/resolve/main/tokenizer.json"
)

func EnsureModel(modelsDir string) (string, error) {
	modelPath := filepath.Join(modelsDir, "bge-m3-q4.onnx")
	tokenizerPath := filepath.Join(modelsDir, "tokenizer.json")

	if _, err := os.Stat(modelPath); os.IsNotExist(err) {
		fmt.Fprintf(os.Stderr, "Model not found at %s. Downloading BGE-M3 (quantized)...\n", modelPath)
		if err := downloadFile(OnnxURL, modelPath); err != nil {
			return "", fmt.Errorf("failed to download model: %w", err)
		}
		fmt.Fprintf(os.Stderr, "Model downloaded successfully!\n")
	}

	if _, err := os.Stat(tokenizerPath); os.IsNotExist(err) {
		fmt.Fprintf(os.Stderr, "Tokenizer not found at %s. Downloading...\n", tokenizerPath)
		if err := downloadFile(TokenizerURL, tokenizerPath); err != nil {
			return "", fmt.Errorf("failed to download tokenizer: %w", err)
		}
		fmt.Fprintf(os.Stderr, "Tokenizer downloaded successfully!\n")
	}

	return modelPath, nil
}

func downloadFile(url string, dest string) error {
	// Ensure directory exists
	if err := os.MkdirAll(filepath.Dir(dest), 0755); err != nil {
		return err
	}

	resp, err := http.Get(url)
	if err != nil {
		return err
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		return fmt.Errorf("bad status: %s", resp.Status)
	}

	out, err := os.Create(dest)
	if err != nil {
		return err
	}
	defer out.Close()

	_, err = io.Copy(out, resp.Body)
	return err
}
