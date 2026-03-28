package llm

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
)

type OllamaRequest struct {
	Model  string    `json:"model"`
	Prompt string    `json:"prompt,omitempty"`
	System string    `json:"system,omitempty"`
	Stream bool      `json:"stream"`
	Messages []Message `json:"messages,omitempty"`
}

type OllamaResponse struct {
	Message Message `json:"message"`
	Done    bool    `json:"done"`
}

func GenerateOllamaCompletion(ctx context.Context, model string, systemPrompt string, messages []Message) (CompletionResponse, error) {
	url := "http://localhost:11434/api/chat"
	
	reqBody := OllamaRequest{
		Model:  model,
		System: systemPrompt,
		Stream: false,
		Messages: messages,
	}

	jsonData, err := json.Marshal(reqBody)
	if err != nil {
		return CompletionResponse{}, err
	}

	req, err := http.NewRequestWithContext(ctx, "POST", url, bytes.NewBuffer(jsonData))
	if err != nil {
		return CompletionResponse{}, err
	}

	req.Header.Set("Content-Type", "application/json")

	client := &http.Client{}
	resp, err := client.Do(req)
	if err != nil {
		return CompletionResponse{}, err
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		bodyBytes, _ := io.ReadAll(resp.Body)
		return CompletionResponse{}, fmt.Errorf("ollama api returned status %d: %s", resp.StatusCode, string(bodyBytes))
	}

	var ollamaResp OllamaResponse
	if err := json.NewDecoder(resp.Body).Decode(&ollamaResp); err != nil {
		return CompletionResponse{}, err
	}

	return CompletionResponse{
		Text: ollamaResp.Message.Content,
	}, nil
}
