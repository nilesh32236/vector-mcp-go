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
	session, err := onnxruntime_go.NewAdvancedSession(modelPath,
		[]string{"input_ids", "attention_mask", "token_type_ids"},
		[]string{"last_hidden_state"},
		nil, nil, nil)
	
	if err != nil {
		fmt.Printf("Session failed (as expected): %v\n", err)
	} else {
		fmt.Println("Session succeeded with all 3 inputs")
		session.Destroy()
	}
}
