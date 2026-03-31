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

type SessionData struct {
	session             *onnxruntime_go.AdvancedSession
	tokenizer           *tokenizer.Tokenizer
	inputIdsTensor      *onnxruntime_go.Tensor[int64]
	attentionMaskTensor *onnxruntime_go.Tensor[int64]
	tokenTypeIdsTensor  *onnxruntime_go.Tensor[int64]
	outputTensor        *onnxruntime_go.Tensor[float32]
	dimension           int
}

type Embedder struct {
	embSess    *SessionData
	rerankSess *SessionData
}

type EmbedderPool struct {
	pool chan *Embedder
}

func NewEmbedderPool(ctx context.Context, modelsDir string, size int, embCfg ModelConfig, rerankerCfg *ModelConfig) (*EmbedderPool, error) {
	pool := make(chan *Embedder, size)
	for i := 0; i < size; i++ {
		embSess, err := newSessionData(modelsDir, embCfg)
		if err != nil {
			close(pool)
			for e := range pool {
				e.Close()
			}
			return nil, fmt.Errorf("failed to initialize embedder pool (index %d): %w", i, err)
		}

		var rerankSess *SessionData
		if rerankerCfg != nil {
			rerankSess, err = newSessionData(modelsDir, *rerankerCfg)
			if err != nil {
				fmt.Fprintf(os.Stderr, "WARNING: Failed to load reranker: %v\n", err)
			}
		}

		emb := &Embedder{
			embSess:    embSess,
			rerankSess: rerankSess,
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

func newSessionData(modelsDir string, mc ModelConfig) (*SessionData, error) {
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

	tokenTypeIds := make([]int64, MaxSeqLength)
	tokenTypeIdsTensor, err := onnxruntime_go.NewTensor(shape, tokenTypeIds)
	if err != nil {
		inputIdsTensor.Destroy()
		attentionMaskTensor.Destroy()
		return nil, err
	}

	outputShape := onnxruntime_go.NewShape(1, MaxSeqLength, int64(dim))
	outputNodeNames := []string{"last_hidden_state"}
	if mc.IsReranker {
		outputShape = onnxruntime_go.NewShape(1, 1)
		outputNodeNames = []string{"logits"}
	}

	outputTensor, err := onnxruntime_go.NewEmptyTensor[float32](outputShape)
	if err != nil {
		inputIdsTensor.Destroy()
		attentionMaskTensor.Destroy()
		tokenTypeIdsTensor.Destroy()
		return nil, err
	}

	inputNodeNames := []string{"input_ids", "attention_mask", "token_type_ids"}
	inputs := []onnxruntime_go.Value{inputIdsTensor, attentionMaskTensor, tokenTypeIdsTensor}
	outputs := []onnxruntime_go.Value{outputTensor}

	// BGE-M3 and some other models don't have token_type_ids
	if mc.Filename == "bge-m3-q4.onnx" {
		inputNodeNames = []string{"input_ids", "attention_mask"}
		inputs = []onnxruntime_go.Value{inputIdsTensor, attentionMaskTensor}
	}

	session, err := onnxruntime_go.NewAdvancedSession(modelPath,
		inputNodeNames,
		outputNodeNames,
		inputs, outputs, nil)
	if err != nil {
		inputIdsTensor.Destroy()
		attentionMaskTensor.Destroy()
		tokenTypeIdsTensor.Destroy()
		outputTensor.Destroy()
		return nil, fmt.Errorf("failed to create ONNX session: %w", err)
	}

	return &SessionData{
		session:             session,
		tokenizer:           tk,
		inputIdsTensor:      inputIdsTensor,
		attentionMaskTensor: attentionMaskTensor,
		tokenTypeIdsTensor:  tokenTypeIdsTensor,
		outputTensor:        outputTensor,
		dimension:           dim,
	}, nil
}

func (e *Embedder) Embed(ctx context.Context, text string) (emb []float32, err error) {
	return e.embSess.embedSingle(text)
}

func (s *SessionData) embedSingle(text string) (emb []float32, err error) {
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

	en, err := s.tokenizer.EncodeSingle(text, true)
	if err != nil {
		return nil, fmt.Errorf("tokenization failed: %w", err)
	}

	ids := en.GetIds()
	mask := en.GetAttentionMask()

	inputIdsData := s.inputIdsTensor.GetData()
	attentionMaskData := s.attentionMaskTensor.GetData()
	tokenTypeIdsData := s.tokenTypeIdsTensor.GetData()

	typeIds := en.GetTypeIds()

	for i := 0; i < MaxSeqLength; i++ {
		if i < len(ids) {
			inputIdsData[i] = int64(ids[i])
			attentionMaskData[i] = int64(mask[i])
			if i < len(typeIds) {
				tokenTypeIdsData[i] = int64(typeIds[i])
			} else {
				tokenTypeIdsData[i] = 0
			}
		} else {
			inputIdsData[i] = 0
			attentionMaskData[i] = 0
			tokenTypeIdsData[i] = 0
		}
	}

	err = s.session.Run()
	if err != nil {
		return nil, fmt.Errorf("ONNX run failed: %w", err)
	}

	fullOutput := s.outputTensor.GetData()
	embedding := make([]float32, s.dimension)
	copy(embedding, fullOutput[:s.dimension])

	return embedding, nil
}

func (e *Embedder) EmbedBatch(ctx context.Context, texts []string) ([][]float32, error) {
	results := make([][]float32, len(texts))
	for i, text := range texts {
		emb, err := e.Embed(ctx, text)
		if err != nil {
			return nil, err
		}
		results[i] = emb
	}
	return results, nil
}

func (e *Embedder) Close() {
	if e.embSess != nil {
		e.embSess.Close()
	}
	if e.rerankSess != nil {
		e.rerankSess.Close()
	}
}

func (s *SessionData) Close() {
	if s.session != nil {
		s.session.Destroy()
	}
	if s.inputIdsTensor != nil {
		s.inputIdsTensor.Destroy()
	}
	if s.attentionMaskTensor != nil {
		s.attentionMaskTensor.Destroy()
	}
	if s.tokenTypeIdsTensor != nil {
		s.tokenTypeIdsTensor.Destroy()
	}
	if s.outputTensor != nil {
		s.outputTensor.Destroy()
	}
}

func (e *Embedder) RerankBatch(ctx context.Context, query string, texts []string) ([]float32, error) {
	if e.rerankSess == nil {
		return nil, fmt.Errorf("reranker model not loaded")
	}

	scores := make([]float32, len(texts))
	for i, text := range texts {
		score, err := e.rerankSess.rerankSingle(query, text)
		if err != nil {
			return nil, err
		}
		scores[i] = score
	}
	return scores, nil
}

func (s *SessionData) rerankSingle(query, text string) (score float32, err error) {
	defer func() {
		if r := recover(); r != nil {
			err = fmt.Errorf("tokenizer panic: %v", r)
		}
	}()

	if !utf8.ValidString(query) || !utf8.ValidString(text) {
		return 0, fmt.Errorf("invalid UTF-8 sequence")
	}

	combinedText := query + " </s> " + text

	en, err := s.tokenizer.EncodeSingle(combinedText, true)
	if err != nil {
		return 0, fmt.Errorf("tokenization failed: %w", err)
	}

	ids := en.GetIds()
	mask := en.GetAttentionMask()

	inputIdsData := s.inputIdsTensor.GetData()
	attentionMaskData := s.attentionMaskTensor.GetData()
	tokenTypeIdsData := s.tokenTypeIdsTensor.GetData()

	typeIds := en.GetTypeIds()

	for i := 0; i < MaxSeqLength; i++ {
		if i < len(ids) {
			inputIdsData[i] = int64(ids[i])
			attentionMaskData[i] = int64(mask[i])
			if i < len(typeIds) {
				tokenTypeIdsData[i] = int64(typeIds[i])
			} else {
				tokenTypeIdsData[i] = 0
			}
		} else {
			inputIdsData[i] = 0
			attentionMaskData[i] = 0
			tokenTypeIdsData[i] = 0
		}
	}

	err = s.session.Run()
	if err != nil {
		return 0, fmt.Errorf("ONNX run failed: %w", err)
	}

	fullOutput := s.outputTensor.GetData()
	return fullOutput[0], nil
}
