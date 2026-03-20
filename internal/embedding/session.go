package embedding

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"unicode/utf8"

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
	dimension           int
}

type EmbedderPool struct {
	pool chan *Embedder
}

func NewEmbedderPool(ctx context.Context, modelsDir string, size int, mc ModelConfig) (*EmbedderPool, error) {
	pool := make(chan *Embedder, size)
	for i := 0; i < size; i++ {
		emb, err := NewEmbedder(modelsDir, mc)
		if err != nil {
			close(pool)
			for e := range pool {
				e.Close()
			}
			return nil, fmt.Errorf("failed to initialize embedder pool (index %d): %w", i, err)
		}
		pool <- emb
	}
	return &EmbedderPool{pool: pool}, nil
}

func (p *EmbedderPool) Get(ctx context.Context) (*Embedder, error) {
	select {
	case e := <-p.pool:
		return e, nil
	case <-ctx.Done():
		return nil, ctx.Err()
	}
}

func (p *EmbedderPool) Put(e *Embedder) {
	p.pool <- e
}

func (p *EmbedderPool) Close() {
	close(p.pool)
	for e := range p.pool {
		e.Close()
	}
}

func NewEmbedder(modelsDir string, mc ModelConfig) (*Embedder, error) {
	modelPath := filepath.Join(modelsDir, mc.Filename)
	tokenizerPath := mc.TokenizerURL // resolved path stored here by EnsureModel
	dim := mc.Dimension

	if _, err := os.Stat(modelPath); os.IsNotExist(err) {
		return nil, fmt.Errorf("model file not found: %s", modelPath)
	}
	if _, err := os.Stat(tokenizerPath); os.IsNotExist(err) {
		return nil, fmt.Errorf("tokenizer file not found: %s", tokenizerPath)
	}

	tk, err := pretrained.FromFile(tokenizerPath)
	if err != nil {
		return nil, fmt.Errorf("failed to load tokenizer from %s: %w", tokenizerPath, err)
	}

	shape := onnxruntime_go.NewShape(1, MaxSeqLength)

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

	outputShape := onnxruntime_go.NewShape(1, MaxSeqLength, int64(dim))
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
		dimension:           dim,
	}, nil
}

func (e *Embedder) Embed(ctx context.Context, text string) (emb []float32, err error) {
	defer func() {
		if r := recover(); r != nil {
			err = fmt.Errorf("tokenizer panic: %v", r)
		}
	}()

	if !utf8.ValidString(text) {
		return nil, fmt.Errorf("invalid UTF-8 sequence")
	}
	if len(text) == 0 {
		return nil, fmt.Errorf("empty input text")
	}

	runes := []rune(text)
	if len(runes) > 16000 {
		text = string(runes[:16000])
	}

	en, err := e.tokenizer.EncodeSingle(text, true)
	if err != nil {
		return nil, fmt.Errorf("tokenization failed: %w", err)
	}

	ids := en.GetIds()
	mask := en.GetAttentionMask()

	inputIdsData := e.inputIdsTensor.GetData()
	attentionMaskData := e.attentionMaskTensor.GetData()

	for i := 0; i < MaxSeqLength; i++ {
		if i < len(ids) {
			inputIdsData[i] = int64(ids[i])
			attentionMaskData[i] = int64(mask[i])
		} else {
			inputIdsData[i] = 0
			attentionMaskData[i] = 0
		}
	}

	err = e.session.Run()
	if err != nil {
		return nil, fmt.Errorf("ONNX run failed: %w", err)
	}

	fullOutput := e.outputTensor.GetData()
	embedding := make([]float32, e.dimension)
	copy(embedding, fullOutput[:e.dimension])

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
