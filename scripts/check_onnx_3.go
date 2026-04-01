//go:build ignore
package main

import (
	"fmt"
	"os"
	"github.com/yalue/onnxruntime_go"
)

func main() {
	libPath := "lib/libonnxruntime.so"
	onnxruntime_go.SetSharedLibraryPath(libPath)
	err := onnxruntime_go.InitializeEnvironment()
	if err != nil {
		fmt.Printf("Init error: %v\n", err)
		os.Exit(1)
	}
	defer onnxruntime_go.DestroyEnvironment()

	modelPath := "models/bge-m3-q4.onnx"
	// AdvancedSession needs names to be created, so use NewSession first to inspect.
	session, err := onnxruntime_go.NewSession(modelPath, nil, nil)
	if err != nil {
		fmt.Printf("Session error: %v\n", err)
		os.Exit(1)
	}
	defer session.Destroy()

	inputNames, _ := session.GetInputNames()
	outputNames, _ := session.GetOutputNames()

	fmt.Printf("Model: %s\n", modelPath)
	fmt.Printf("Inputs: %v\n", inputNames)
	fmt.Printf("Outputs: %v\n", outputNames)

	// Also check reranker
	rerankerPath := "models/ms-marco-MiniLM-L-6-v2-q4.onnx"
	if _, err := os.Stat(rerankerPath); err == nil {
		rsess, err := onnxruntime_go.NewSession(rerankerPath, nil, nil)
		if err == nil {
			rin, _ := rsess.GetInputNames()
			rout, _ := rsess.GetOutputNames()
			fmt.Printf("Reranker: %s\n", rerankerPath)
			fmt.Printf("Inputs: %v\n", rin)
			fmt.Printf("Outputs: %v\n", rout)
			rsess.Destroy()
		}
	}
}
