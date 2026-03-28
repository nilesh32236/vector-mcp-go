package api

import (
	"encoding/json"
	"fmt"
	"net/http"
	"strings"
	"time"

	mcp_lib "github.com/mark3labs/mcp-go/mcp"
	"github.com/nilesh32236/vector-mcp-go/internal/chat"
	"github.com/nilesh32236/vector-mcp-go/internal/db"
	"github.com/nilesh32236/vector-mcp-go/internal/llm"
)

// handleListSessions returns a list of all existing chat sessions.
func (s *Server) handleListSessions(w http.ResponseWriter, r *http.Request) {
	if s.chatStore == nil {
		http.Error(w, "Chat store not initialized", http.StatusInternalServerError)
		return
	}

	sessions, err := s.chatStore.ListSessions()
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}

	if sessions == nil {
		sessions = []chat.Session{}
	}

	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(sessions)
}

// handleCreateSession initializes a new chat session with an optional title.
func (s *Server) handleCreateSession(w http.ResponseWriter, r *http.Request) {
	if s.chatStore == nil {
		http.Error(w, "Chat store not initialized", http.StatusInternalServerError)
		return
	}

	var req struct {
		Title string `json:"title"`
	}
	if r.ContentLength > 0 {
		if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
			http.Error(w, err.Error(), http.StatusBadRequest)
			return
		}
	}

	if req.Title == "" {
		req.Title = "New Chat"
	}

	session, err := s.chatStore.CreateSession(req.Title)
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}

	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(session)
}

// handleGetSession retrieves the full message history for a specific chat session.
func (s *Server) handleGetSession(w http.ResponseWriter, r *http.Request) {
	if s.chatStore == nil {
		http.Error(w, "Chat store not initialized", http.StatusInternalServerError)
		return
	}

	id := r.PathValue("id")
	if id == "" {
		http.Error(w, "Session ID is required", http.StatusBadRequest)
		return
	}

	history, err := s.chatStore.GetSession(id)
	if err != nil {
		if err.Error() == "session not found" {
			http.Error(w, "Session not found", http.StatusNotFound)
		} else {
			http.Error(w, err.Error(), http.StatusInternalServerError)
		}
		return
	}

	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(history)
}

// handleDeleteSession removes a chat session and all its message history.
func (s *Server) handleDeleteSession(w http.ResponseWriter, r *http.Request) {
	if s.chatStore == nil {
		http.Error(w, "Chat store not initialized", http.StatusInternalServerError)
		return
	}

	id := r.PathValue("id")
	if id == "" {
		http.Error(w, "Session ID is required", http.StatusBadRequest)
		return
	}

	if err := s.chatStore.DeleteSession(id); err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}

	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(map[string]string{"status": "success"})
}

// ChatMessage represents a single interaction in a chat thread.
type ChatMessage struct {
	Role    string `json:"role"`    // "user", "assistant", or "function"
	Content string `json:"content"` // Message text or function response
}

// ChatRequest defines the payload for sending a message to the AI assistant.
type ChatRequest struct {
	SessionID string `json:"session_id"` // ID of the session to append to
	Model     string `json:"model"`      // Optional override for the LLM model
	Message   string `json:"message"`    // The user's prompt
}

// ChatResponse contains the assistant's reply and metadata about the interaction.
type ChatResponse struct {
	ModelUsed string `json:"model_used"` // The specific model that generated the response
	Role      string `json:"role"`       // Always "assistant"
	Content   string `json:"content"`    // The generated text reply
	ToolCalls int    `json:"tool_calls"` // Number of tools invoked during the generation
}

// handleChat processes a user message using RAG and optional MCP tool calls.
func (s *Server) handleChat(w http.ResponseWriter, r *http.Request) {
	if s.cfg.GeminiApiKey == "" {
		http.Error(w, `{"error": "Gemini API key is not configured"}`, http.StatusNotImplemented)
		return
	}

	if s.chatStore == nil {
		http.Error(w, "Chat store not initialized", http.StatusInternalServerError)
		return
	}

	var req ChatRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	}

	if req.SessionID == "" {
		http.Error(w, "session_id is required", http.StatusBadRequest)
		return
	}

	if req.Message == "" {
		http.Error(w, "message cannot be empty", http.StatusBadRequest)
		return
	}

	model := req.Model
	if model == "" {
		model = s.cfg.DefaultGeminiModel
	}

	// 1. Fetch History
	history, err := s.chatStore.GetSession(req.SessionID)
	if err != nil {
		http.Error(w, "Session not found", http.StatusNotFound)
		return
	}

	// 2. Append User Message
	userMsg := llm.Message{Role: "user", Content: req.Message}
	if err := s.chatStore.AppendMessage(req.SessionID, userMsg); err != nil {
		http.Error(w, "Failed to append user message", http.StatusInternalServerError)
		return
	}
	history.Messages = append(history.Messages, userMsg)

	// 3. RAG Search on new message
	emb, err := s.embedder.Embed(r.Context(), req.Message)
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}

	store, err := s.storeGetter(r.Context())
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}

	records, err := store.HybridSearch(r.Context(), req.Message, emb, 10, []string{}, "")
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}

	var contextBuilder string
	for _, rec := range records {
		path := ""
		if p, ok := rec.Metadata["path"]; ok {
			path = p
		} else if p, ok := rec.Metadata["file"]; ok {
			path = p
		}

		if path != "" {
			contextBuilder += fmt.Sprintf("File: %s\nCode:\n%s\n\n", path, rec.Content)
		} else {
			contextBuilder += fmt.Sprintf("Code:\n%s\n\n", rec.Content)
		}
	}

	systemPrompt := "You are an expert AI coding assistant. Use the provided codebase context to answer the user's question. If the answer is not in the context, say so. \n\nContext:\n" + contextBuilder
	// 4. Tools config
	var allTools []mcp_lib.Tool
	if s.mcpServer != nil {
		allTools = s.mcpServer.ListTools()
	}

	// Add default manual context tool
	geminiTools := []llm.Tool{
		{
			FunctionDeclarations: []llm.FunctionDeclaration{
				{
					Name:        "save_manual_context",
					Description: "Saves client requirements, architectural rules, or manual notes to the vector database for future memory.",
					Parameters: llm.Parameters{
						Type: "OBJECT",
						Properties: map[string]llm.Property{
							"content": {
								Type:        "STRING",
								Description: "The formatted text to save",
							},
						},
						Required: []string{"content"},
					},
				},
			},
		},
	}

	for _, t := range allTools {
		// Convert mcp.Tool to llm.FunctionDeclaration
		fd := llm.FunctionDeclaration{
			Name:        t.Name,
			Description: t.Description,
			Parameters: llm.Parameters{
				Type:       "OBJECT",
				Properties: make(map[string]llm.Property),
				Required:   t.InputSchema.Required,
			},
		}

		// Convert Schema Properties to llm Properties
		for k, vInterface := range t.InputSchema.Properties {
			v, ok := vInterface.(map[string]interface{})
			if !ok {
				continue
			}
			propType := "STRING"
			if tStr, ok := v["type"].(string); ok {
				propType = strings.ToUpper(tStr)
			}
			desc := ""
			if dStr, ok := v["description"].(string); ok {
				desc = dStr
			}
			fd.Parameters.Properties[k] = llm.Property{
				Type:        propType,
				Description: desc,
			}
		}
		geminiTools[0].FunctionDeclarations = append(geminiTools[0].FunctionDeclarations, fd)
	}

	endpointURL := ""
	if h := r.Header.Get("X-Test-Gemini-URL"); h != "" {
		endpointURL = h
	}

	// 5. Chat & Tool Loop
	var finalContent string
	var i int
	maxTurns := 10
	for i = 0; i < maxTurns; i++ {
		resp, err := llm.GenerateGeminiCompletion(r.Context(), s.cfg.GeminiApiKey, model, systemPrompt, history.Messages, geminiTools, endpointURL)
		if err != nil {
			http.Error(w, err.Error(), http.StatusInternalServerError)
			return
		}

		if resp.FunctionCall != nil {
			// 1. Handle Built-in Tools
			if resp.FunctionCall.Name == "save_manual_context" {
				contentInter, ok := resp.FunctionCall.Args["content"]
				if ok {
					contentStr, _ := contentInter.(string)
					emb, _ := s.embedder.Embed(r.Context(), contentStr)
					meta := map[string]string{"type": "manual_context", "source": "agentic_memory"}
					id := fmt.Sprintf("manual_%d", time.Now().UnixNano())
					store.Insert(r.Context(), []db.Record{{ID: id, Content: contentStr, Embedding: emb, Metadata: meta}})

					fnCallMsg := llm.Message{Role: "assistant", FunctionCall: resp.FunctionCall}
					s.chatStore.AppendMessage(req.SessionID, fnCallMsg)
					history.Messages = append(history.Messages, fnCallMsg)
					fnRespMsg := llm.Message{Role: "function", FunctionResponse: &llm.FunctionResponse{Name: "save_manual_context", Response: map[string]interface{}{"status": "success"}}}
					s.chatStore.AppendMessage(req.SessionID, fnRespMsg)
					history.Messages = append(history.Messages, fnRespMsg)
					continue
				}
			}

			// 2. Executing MCP Tool
			if s.mcpServer == nil {
				http.Error(w, "MCP server not available for tool calling", http.StatusInternalServerError)
				return
			}
			toolResult, err := s.mcpServer.CallTool(r.Context(), resp.FunctionCall.Name, resp.FunctionCall.Args)

			// Append FunctionCall to history
			fnCallMsg := llm.Message{
				Role:         "assistant",
				FunctionCall: resp.FunctionCall,
			}
			s.chatStore.AppendMessage(req.SessionID, fnCallMsg)
			history.Messages = append(history.Messages, fnCallMsg)

			// Prepare Response
			var resContent map[string]interface{}
			if err != nil {
				resContent = map[string]interface{}{"error": err.Error()}
			} else {
				// We expect toolResult to have Text or other content
				resContent = map[string]interface{}{"result": toolResult.Content}
			}

			// Append FunctionResponse to history
			fnRespMsg := llm.Message{
				Role: "function",
				FunctionResponse: &llm.FunctionResponse{
					Name:     resp.FunctionCall.Name,
					Response: resContent,
				},
			}
			s.chatStore.AppendMessage(req.SessionID, fnRespMsg)
			history.Messages = append(history.Messages, fnRespMsg)

			// Continue loop to get final answer from Gemini
			continue
		}

		// If no more function calls, we have our final text
		finalContent = resp.Text
		break
	}

	// 7. Append Assistant final message
	assistantMsg := llm.Message{Role: "assistant", Content: finalContent}
	s.chatStore.AppendMessage(req.SessionID, assistantMsg)

	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(ChatResponse{
		ModelUsed: model,
		Role:      "assistant",
		Content:   finalContent,
		ToolCalls: i,
	})
}
