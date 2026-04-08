// Package onnx provides an interface for interacting with ONNX models for embedding and reranking.
package onnx

import (
	"fmt"
	"os"
	"path/filepath"
	"runtime"

	"github.com/yalue/onnxruntime_go"
)

// Init initializes the ONNX runtime environment and discovered the shared library path.
func Init() error {
	if runtime.GOOS == "linux" {
		libPath := os.Getenv("ONNX_LIB_PATH")
		if libPath == "" {
			// 1. Try relative to CWD (for go run)
			cwd, _ := os.Getwd()
			libPath = filepath.Join(cwd, "lib", "libonnxruntime.so")

			if _, err := os.Stat(libPath); os.IsNotExist(err) {
				// 2. Try relative to executable
				execPath, _ := os.Executable()
				execDir := filepath.Dir(execPath)
				libPath = filepath.Join(execDir, "lib", "libonnxruntime.so")

				if _, err := os.Stat(libPath); os.IsNotExist(err) {
					// 3. Try standard local path
					home, _ := os.UserHomeDir()
					libPath = filepath.Join(home, ".local", "share", "vector-mcp-go", "lib", "libonnxruntime.so")
				}
			}
		}
		fmt.Fprintf(os.Stderr, "DEBUG: Using ONNX shared library path: %s\n", libPath)
		onnxruntime_go.SetSharedLibraryPath(libPath)
	}

	err := onnxruntime_go.InitializeEnvironment()
	if err != nil {
		return fmt.Errorf("error initializing ONNX runtime: %w", err)
	}
	return nil
}
