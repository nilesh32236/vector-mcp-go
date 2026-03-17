package embedding

import (
	"context"
	"fmt"
	"os"
	"path/filepath"

	"github.com/sugarme/tokenizer"
	"github.com/sugarme/tokenizer/pretrained"
	"github.com/yalue/onnxruntime_go"
)

const MaxSeqLength = 512

type Embedder struct {
	session             *onnxruntime_go.AdvancedSession
	tokenizer           *tokenizer.Tokenizer
	inputIdsTensor      *onnxruntime_go.Tensor[int64]
	attentionMaskTensor *onnxruntime_go.Tensor[int64]
	outputTensor        *onnxruntime_go.Tensor[float32]
}

func NewEmbedder(modelsDir string) (*Embedder, error) {
	modelPath := filepath.Join(modelsDir, "bge-m3-q4.onnx")
	tokenizerPath := filepath.Join(modelsDir, "tokenizer.json")

	// Load tokenizer using the pretrained sub-package which has FromFile implemented
	tk, err := pretrained.FromFile(tokenizerPath)
	if err != nil {
		return nil, fmt.Errorf("failed to load tokenizer from %s: %w", tokenizerPath, err)
	}

	// We'll handle truncation and padding manually in Embed()
	// because sugarme/tokenizer high-level methods are buggy with this model.

	// Safety: BGE-M3 from Xenova might have nil components in sugarme
	if tk.GetNormalizer() == nil {
		fmt.Fprintf(os.Stderr, "⚠️ Normalizer is nil...\n")
	}

	shape := onnxruntime_go.NewShape(1, MaxSeqLength)

	// Pre-allocate input tensors
	inputIds := make([]int64, MaxSeqLength)
	inputIdsTensor, err := onnxruntime_go.NewTensor(shape, inputIds)
	if err != nil {
		return nil, err
	}

	attentionMask := make([]int64, MaxSeqLength)
	attentionMaskTensor, err := onnxruntime_go.NewTensor(shape, attentionMask)
	if err != nil {
		inputIdsTensor.Destroy()
		return nil, err
	}

	// Pre-allocate output tensor
	outputShape := onnxruntime_go.NewShape(1, MaxSeqLength, 1024)
	outputTensor, err := onnxruntime_go.NewEmptyTensor[float32](outputShape)
	if err != nil {
		inputIdsTensor.Destroy()
		attentionMaskTensor.Destroy()
		return nil, err
	}

	inputs := []onnxruntime_go.Value{inputIdsTensor, attentionMaskTensor}
	outputs := []onnxruntime_go.Value{outputTensor}

	session, err := onnxruntime_go.NewAdvancedSession(modelPath,
		[]string{"input_ids", "attention_mask"},
		[]string{"last_hidden_state"},
		inputs, outputs, nil)
	if err != nil {
		inputIdsTensor.Destroy()
		attentionMaskTensor.Destroy()
		outputTensor.Destroy()
		return nil, fmt.Errorf("failed to create ONNX session: %w", err)
	}

	return &Embedder{
		session:             session,
		tokenizer:           tk,
		inputIdsTensor:      inputIdsTensor,
		attentionMaskTensor: attentionMaskTensor,
		outputTensor:        outputTensor,
	}, nil
}

func (e *Embedder) Embed(ctx context.Context, text string) (emb []float32, err error) {
	// Panic recovery for buggy tokenizer internal states
	defer func() {
		if r := recover(); r != nil {
			err = fmt.Errorf("tokenizer panic: %v", r)
		}
	}()

	// 1. Tokenize
	en, err := e.tokenizer.EncodeSingle(text, true)
	if err != nil {
		return nil, fmt.Errorf("tokenization failed: %w", err)
	}

	ids := en.GetIds()
	mask := en.GetAttentionMask()

	// 2. Update existing tensor data
	inputIdsData := e.inputIdsTensor.GetData()
	attentionMaskData := e.attentionMaskTensor.GetData()

	for i := 0; i < MaxSeqLength; i++ {
		if i < len(ids) {
			inputIdsData[i] = int64(ids[i])
			attentionMaskData[i] = int64(mask[i])
		} else {
			inputIdsData[i] = 0 // Padding
			attentionMaskData[i] = 0
		}
	}

	// 3. Run Inference
	err = e.session.Run()
	if err != nil {
		return nil, fmt.Errorf("ONNX run failed: %w", err)
	}

	// 4. Mean Pooling: Take CLS
	fullOutput := e.outputTensor.GetData()
	embedding := make([]float32, 1024)
	copy(embedding, fullOutput[:1024])

	return embedding, nil
}

func (e *Embedder) Close() {
	if e.session != nil {
		e.session.Destroy()
	}
	if e.inputIdsTensor != nil {
		e.inputIdsTensor.Destroy()
	}
	if e.attentionMaskTensor != nil {
		e.attentionMaskTensor.Destroy()
	}
	if e.outputTensor != nil {
		e.outputTensor.Destroy()
	}
}
