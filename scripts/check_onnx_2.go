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
	session, err := onnxruntime_go.NewAdvancedSession(modelPath,
		nil, nil, nil, nil, nil)
	
	if err != nil {
		fmt.Printf("Session error: %v\n", err)
	} else {
		// Try to inspect inputs/outputs if possible? 
		// Actually onnxruntime_go doesn't seem to expose them easily from session?
		fmt.Println("Session created")
		session.Destroy()
	}
}
