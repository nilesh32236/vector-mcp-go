package embedding

import (
	"context"
	"fmt"
	"math"
	"os"
	"path/filepath"
	"unicode/utf8"

	"github.com/sugarme/tokenizer"
	"github.com/sugarme/tokenizer/pretrained"
	"github.com/yalue/onnxruntime_go"
)

// MaxSeqLength is the maximum sequence length supported by the embedding model.
const MaxSeqLength = 512

// SessionData stores model context and session information for an embedding task.
type SessionData struct {
	session             *onnxruntime_go.AdvancedSession
	tokenizer           *tokenizer.Tokenizer
	inputIDsTensor      *onnxruntime_go.Tensor[int64]
	attentionMaskTensor *onnxruntime_go.Tensor[int64]
	tokenTypeIDsTensor  *onnxruntime_go.Tensor[int64]
	outputTensor        *onnxruntime_go.Tensor[float32]
	dimension           int
	modelName           string
	matryoshkaDim       int // 0 = disabled; >0 = truncate to this many dims (MRL)
}

// NewEmbedder initializes and returns a new embedder with optional reranking capability.
func NewEmbedder(_ context.Context, modelsDir string, embCfg ModelConfig, rerankCfg *ModelConfig) (*Embedder, error) {
	embSess, err := newSessionData(modelsDir, embCfg)
	if err != nil {
		return nil, fmt.Errorf("failed to load embedding model: %w", err)
	}

	var rerankSess *SessionData
	if rerankCfg != nil {
		rerankSess, err = newSessionData(modelsDir, *rerankCfg)
		if err != nil {
			fmt.Fprintf(os.Stderr, "WARNING: Failed to load reranker: %v\n", err)
		}
	}

	return &Embedder{
		embSess:    embSess,
		rerankSess: rerankSess,
	}, nil
}

// NewEmbedderPool creates a pool of embedders for concurrent processing.
func NewEmbedderPool(ctx context.Context, modelsDir string, size int, embCfg ModelConfig, rerankerCfg *ModelConfig) (*EmbedderPool, error) {
	pool := make(chan *Embedder, size)
	for i := 0; i < size; i++ {
		emb, err := NewEmbedder(ctx, modelsDir, embCfg, rerankerCfg)
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

	inputIDs := make([]int64, MaxSeqLength)
	inputIDsTensor, err := onnxruntime_go.NewTensor(shape, inputIDs)
	if err != nil {
		return nil, err
	}

	attentionMask := make([]int64, MaxSeqLength)
	attentionMaskTensor, err := onnxruntime_go.NewTensor(shape, attentionMask)
	if err != nil {
		_ = inputIDsTensor.Destroy()
		return nil, err
	}

	tokenTypeIDs := make([]int64, MaxSeqLength)
	tokenTypeIDsTensor, err := onnxruntime_go.NewTensor(shape, tokenTypeIDs)
	if err != nil {
		_ = inputIDsTensor.Destroy()
		_ = attentionMaskTensor.Destroy()
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
		_ = inputIDsTensor.Destroy()
		_ = attentionMaskTensor.Destroy()
		_ = tokenTypeIDsTensor.Destroy()
		return nil, err
	}

	inputNodeNames := []string{"input_ids", "attention_mask", "token_type_ids"}
	inputs := []onnxruntime_go.Value{inputIDsTensor, attentionMaskTensor, tokenTypeIDsTensor}
	outputs := []onnxruntime_go.Value{outputTensor}

	// BGE-M3 and some other models don't have token_type_ids
	if mc.Filename == "bge-m3-q4.onnx" {
		inputNodeNames = []string{"input_ids", "attention_mask"}
		inputs = []onnxruntime_go.Value{inputIDsTensor, attentionMaskTensor}
	}

	session, err := onnxruntime_go.NewAdvancedSession(modelPath,
		inputNodeNames,
		outputNodeNames,
		inputs, outputs, nil)
	if err != nil {
		_ = inputIDsTensor.Destroy()
		_ = attentionMaskTensor.Destroy()
		_ = tokenTypeIDsTensor.Destroy()
		_ = outputTensor.Destroy()
		return nil, fmt.Errorf("failed to create ONNX session: %w", err)
	}

	return &SessionData{
		session:             session,
		tokenizer:           tk,
		inputIDsTensor:      inputIDsTensor,
		attentionMaskTensor: attentionMaskTensor,
		tokenTypeIDsTensor:  tokenTypeIDsTensor,
		outputTensor:        outputTensor,
		dimension:           dim,
		modelName:           mc.Filename,
		matryoshkaDim:       mc.MatryoshkaDim,
	}, nil
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

	inputIDsData := s.inputIDsTensor.GetData()
	attentionMaskData := s.attentionMaskTensor.GetData()
	tokenTypeIDsData := s.tokenTypeIDsTensor.GetData()

	typeIDs := en.GetTypeIds()

	for i := 0; i < MaxSeqLength; i++ {
		if i < len(ids) {
			inputIDsData[i] = int64(ids[i])
			attentionMaskData[i] = int64(mask[i])
			if i < len(typeIDs) {
				tokenTypeIDsData[i] = int64(typeIDs[i])
			} else {
				tokenTypeIDsData[i] = 0
			}
		} else {
			inputIDsData[i] = 0
			attentionMaskData[i] = 0
			tokenTypeIDsData[i] = 0
		}
	}

	err = s.session.Run()
	if err != nil {
		return nil, fmt.Errorf("ONNX run failed: %w", err)
	}

	fullOutput := s.outputTensor.GetData()
	embedding := make([]float32, s.dimension)

	// Xenova ports of BGE models usually support CLS pooling (token 0)
	// Some models like BGE-M3 might benefit from Mean Pooling.
	// We'll stick with CLS but add normalization which is CRITICAL for Cosine Similarity.
	copy(embedding, fullOutput[:s.dimension])

	// Matryoshka truncation: nomic-embed-text-v1.5 supports MRL — truncating to a
	// smaller dimension (e.g. 256 of 768) retains most quality at lower memory cost.
	// The model signals this via a "matryoshka_dim" field in ModelConfig (set via
	// MATRYOSHKA_DIM env var). We truncate then re-normalise.
	if s.matryoshkaDim > 0 && s.matryoshkaDim < s.dimension {
		embedding = embedding[:s.matryoshkaDim]
	}

	normalize(embedding)

	return embedding, nil
}

func normalize(v []float32) {
	var sum float32
	for _, x := range v {
		sum += x * x
	}
	norm := float32(math.Sqrt(float64(sum)))
	if norm > 1e-9 {
		for i := range v {
			v[i] /= norm
		}
	}
}

// Close releases resources and terminates the model session for the associated data.
func (s *SessionData) Close() {
	if s.session != nil {
		_ = s.session.Destroy()
	}
	if s.inputIDsTensor != nil {
		_ = s.inputIDsTensor.Destroy()
	}
	if s.attentionMaskTensor != nil {
		_ = s.attentionMaskTensor.Destroy()
	}
	if s.tokenTypeIDsTensor != nil {
		_ = s.tokenTypeIDsTensor.Destroy()
	}
	if s.outputTensor != nil {
		_ = s.outputTensor.Destroy()
	}
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

	inputIDsData := s.inputIDsTensor.GetData()
	attentionMaskData := s.attentionMaskTensor.GetData()
	tokenTypeIDsData := s.tokenTypeIDsTensor.GetData()

	typeIDs := en.GetTypeIds()

	for i := 0; i < MaxSeqLength; i++ {
		if i < len(ids) {
			inputIDsData[i] = int64(ids[i])
			attentionMaskData[i] = int64(mask[i])
			if i < len(typeIDs) {
				tokenTypeIDsData[i] = int64(typeIDs[i])
			} else {
				tokenTypeIDsData[i] = 0
			}
		} else {
			inputIDsData[i] = 0
			attentionMaskData[i] = 0
			tokenTypeIDsData[i] = 0
		}
	}

	err = s.session.Run()
	if err != nil {
		return 0, fmt.Errorf("ONNX run failed: %w", err)
	}

	fullOutput := s.outputTensor.GetData()
	return fullOutput[0], nil
}
