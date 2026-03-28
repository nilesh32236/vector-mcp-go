package llm

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
)

type FunctionCall struct {
	Name string                 `json:"name"`
	Args map[string]interface{} `json:"args"`
}

type FunctionResponse struct {
	Name     string                 `json:"name"`
	Response map[string]interface{} `json:"response"`
}

type Message struct {
	Role             string            `json:"role"`
	Content          string            `json:"content,omitempty"`
	FunctionCall     *FunctionCall     `json:"function_call,omitempty"`
	FunctionResponse *FunctionResponse `json:"function_response,omitempty"`
}

type CompletionResponse struct {
	Text         string
	FunctionCall *FunctionCall
}

type geminiFunctionCall struct {
	FunctionName string                 `json:"name"`
	Args         map[string]interface{} `json:"args"`
}

type geminiFunctionResponseContent struct {
	Name     string                 `json:"name"`
	Response map[string]interface{} `json:"response"`
}

type geminiPart struct {
	Text             string                         `json:"text,omitempty"`
	FunctionCall     *geminiFunctionCall            `json:"functionCall,omitempty"`
	FunctionResponse *geminiFunctionResponseContent `json:"functionResponse,omitempty"`
}

type Tool struct {
	FunctionDeclarations []FunctionDeclaration `json:"function_declarations"`
}

type FunctionDeclaration struct {
	Name        string     `json:"name"`
	Description string     `json:"description"`
	Parameters  Parameters `json:"parameters"`
}

type Parameters struct {
	Type       string              `json:"type"`
	Properties map[string]Property `json:"properties"`
	Required   []string            `json:"required"`
}

type Property struct {
	Type        string `json:"type"`
	Description string `json:"description"`
}

type geminiContent struct {
	Role  string       `json:"role"`
	Parts []geminiPart `json:"parts"`
}

type geminiSystemInstruction struct {
	Parts []geminiPart `json:"parts"`
}

type geminiRequest struct {
	SystemInstruction *geminiSystemInstruction `json:"system_instruction,omitempty"`
	Contents          []geminiContent          `json:"contents"`
	Tools             []Tool                   `json:"tools,omitempty"`
}

type geminiResponse struct {
	Candidates []struct {
		Content struct {
			Parts []geminiPart `json:"parts"`
		} `json:"content"`
	} `json:"candidates"`
}

// GeminiConfig holds the configuration for a Gemini completion request.
type GeminiConfig struct {
	APIKey       string
	Model        string
	SystemPrompt string
	Messages     []Message
	Tools        []Tool
	EndpointURL  string
}

// GenerateGeminiCompletion calls the Google Gemini REST API to generate a response.
func GenerateGeminiCompletion(ctx context.Context, cfg GeminiConfig) (CompletionResponse, error) {
	endpointURL := cfg.EndpointURL
	if endpointURL == "" {
		endpointURL = fmt.Sprintf("https://generativelanguage.googleapis.com/v1beta/models/%s:generateContent", cfg.Model)
	}

	reqBody := geminiRequest{}

	if cfg.SystemPrompt != "" {
		reqBody.SystemInstruction = &geminiSystemInstruction{
			Parts: []geminiPart{
				{Text: cfg.SystemPrompt},
			},
		}
	}

	if len(cfg.Tools) > 0 {
		reqBody.Tools = cfg.Tools
	}

	for _, msg := range cfg.Messages {
		// Gemini uses "user" and "model" roles
		role := msg.Role
		if role == "assistant" || role == "function" {
			role = "model"
		}

		var parts []geminiPart

		if msg.FunctionCall != nil {
			parts = append(parts, geminiPart{
				FunctionCall: &geminiFunctionCall{
					FunctionName: msg.FunctionCall.Name,
					Args:         msg.FunctionCall.Args,
				},
			})
		} else if msg.FunctionResponse != nil {
			role = "user" // Function responses are sent back as the user role in Gemini
			parts = append(parts, geminiPart{
				FunctionResponse: &geminiFunctionResponseContent{
					Name:     msg.FunctionResponse.Name,
					Response: msg.FunctionResponse.Response,
				},
			})
		} else {
			parts = append(parts, geminiPart{
				Text: msg.Content,
			})
		}

		reqBody.Contents = append(reqBody.Contents, geminiContent{
			Role:  role,
			Parts: parts,
		})
	}

	jsonData, err := json.Marshal(reqBody)
	if err != nil {
		return CompletionResponse{}, fmt.Errorf("failed to marshal gemini request: %w", err)
	}

	reqURL := fmt.Sprintf("%s?key=%s", endpointURL, cfg.APIKey)
	req, err := http.NewRequestWithContext(ctx, "POST", reqURL, bytes.NewBuffer(jsonData))
	if err != nil {
		return CompletionResponse{}, fmt.Errorf("failed to create http request: %w", err)
	}

	req.Header.Set("Content-Type", "application/json")

	client := &http.Client{}
	resp, err := client.Do(req)
	if err != nil {
		return CompletionResponse{}, fmt.Errorf("failed to execute http request: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		bodyBytes, _ := io.ReadAll(resp.Body)
		return CompletionResponse{}, fmt.Errorf("gemini api returned status %d: %s", resp.StatusCode, string(bodyBytes))
	}

	var geminiResp geminiResponse
	if err := json.NewDecoder(resp.Body).Decode(&geminiResp); err != nil {
		return CompletionResponse{}, fmt.Errorf("failed to decode gemini response: %w", err)
	}

	if len(geminiResp.Candidates) == 0 || len(geminiResp.Candidates[0].Content.Parts) == 0 {
		return CompletionResponse{}, fmt.Errorf("gemini api returned no content")
	}

	part := geminiResp.Candidates[0].Content.Parts[0]

	comp := CompletionResponse{}

	if part.FunctionCall != nil {
		comp.FunctionCall = &FunctionCall{
			Name: part.FunctionCall.FunctionName,
			Args: part.FunctionCall.Args,
		}
	} else {
		comp.Text = part.Text
	}

	return comp, nil
}
// ListGeminiModels retrieves the list of available models from the Google Gemini API.
func ListGeminiModels(ctx context.Context, apiKey string) ([]string, error) {
	url := fmt.Sprintf("https://generativelanguage.googleapis.com/v1beta/models?key=%s", apiKey)
	req, err := http.NewRequestWithContext(ctx, "GET", url, nil)
	if err != nil {
		return nil, fmt.Errorf("failed to create http request: %w", err)
	}

	client := &http.Client{}
	resp, err := client.Do(req)
	if err != nil {
		return nil, fmt.Errorf("failed to execute http request: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		bodyBytes, _ := io.ReadAll(resp.Body)
		return nil, fmt.Errorf("gemini api returned status %d: %s", resp.StatusCode, string(bodyBytes))
	}

	var data struct {
		Models []struct {
			Name string `json:"name"`
		} `json:"models"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&data); err != nil {
		return nil, fmt.Errorf("failed to decode gemini response: %w", err)
	}

	var modelNames []string
	for _, m := range data.Models {
		modelNames = append(modelNames, m.Name)
	}
	return modelNames, nil
}
