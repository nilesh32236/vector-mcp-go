package api

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"time"

	"github.com/nilesh32236/vector-mcp-go/internal/chat"
	"github.com/nilesh32236/vector-mcp-go/internal/config"
	"github.com/nilesh32236/vector-mcp-go/internal/db"
	"github.com/nilesh32236/vector-mcp-go/internal/indexer"
	"github.com/nilesh32236/vector-mcp-go/internal/llm"
)

type StoreGetter func(ctx context.Context) (*db.Store, error)

type Server struct {
	cfg         *config.Config
	storeGetter StoreGetter
	embedder    indexer.Embedder
	srv         *http.Server
	chatStore   *chat.Store
}

func NewServer(cfg *config.Config, storeGetter StoreGetter, embedder indexer.Embedder) *Server {
	chatStore, err := chat.NewStore(cfg.DataDir)
	if err != nil && cfg.Logger != nil {
		cfg.Logger.Error("Failed to initialize chat store", "error", err)
	}

	mux := http.NewServeMux()

	server := &Server{
		cfg:         cfg,
		storeGetter: storeGetter,
		embedder:    embedder,
		chatStore:   chatStore,
	}

	mux.HandleFunc("GET /api/health", server.handleHealth)

	// Chat sessions CRUD
	mux.HandleFunc("GET /api/sessions", server.handleListSessions)
	mux.HandleFunc("POST /api/sessions", server.handleCreateSession)
	mux.HandleFunc("GET /api/sessions/{id}", server.handleGetSession)
	mux.HandleFunc("DELETE /api/sessions/{id}", server.handleDeleteSession)

	mux.HandleFunc("POST /api/search", server.handleSearch)
	mux.HandleFunc("POST /api/context", server.handleContext)
	mux.HandleFunc("POST /api/todo", server.handleTodo)
	mux.HandleFunc("POST /api/chat", server.handleChat)

	addr := fmt.Sprintf(":%s", cfg.ApiPort)
	server.srv = &http.Server{
		Addr:    addr,
		Handler: mux,
	}

	return server
}

func (s *Server) Start() error {
	if s.cfg.Logger != nil {
		s.cfg.Logger.Info("Starting HTTP API server", "port", s.cfg.ApiPort)
	}
	err := s.srv.ListenAndServe()
	if err != nil && err != http.ErrServerClosed {
		return err
	}
	return nil
}

func (s *Server) Shutdown(ctx context.Context) error {
	if s.cfg.Logger != nil {
		s.cfg.Logger.Info("Shutting down HTTP API server")
	}
	return s.srv.Shutdown(ctx)
}

func (s *Server) handleHealth(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(map[string]string{
		"status":  "ok",
		"version": "1.0",
	})
}

type SearchRequest struct {
	Query string `json:"query"`
	TopK  int    `json:"top_k"`
}

type SearchResponse struct {
	ID         string            `json:"id"`
	Text       string            `json:"text"`
	Similarity float32           `json:"similarity"`
	Metadata   map[string]string `json:"metadata"`
}

func (s *Server) handleSearch(w http.ResponseWriter, r *http.Request) {
	var req SearchRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	}

	if req.TopK <= 0 {
		req.TopK = 5 // default
	}

	emb, err := s.embedder.Embed(r.Context(), req.Query)
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}

	store, err := s.storeGetter(r.Context())
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}

	// For the global brain search, we don't filter by project ID unless requested,
	// but store.Search currently takes projectIDs. Passing an empty slice searches everything.
	records, err := store.Search(r.Context(), emb, req.TopK, []string{})
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}

	var resp []SearchResponse
	for _, rec := range records {
		resp = append(resp, SearchResponse{
			ID:         rec.ID,
			Text:       rec.Content,
			Similarity: rec.Similarity,
			Metadata:   rec.Metadata,
		})
	}

	if resp == nil {
		resp = []SearchResponse{}
	}

	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(resp)
}

type ContextRequest struct {
	Text     string            `json:"text"`
	Source   string            `json:"source"`
	Metadata map[string]string `json:"metadata"`
}

func (s *Server) handleContext(w http.ResponseWriter, r *http.Request) {
	var req ContextRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	}

	emb, err := s.embedder.Embed(r.Context(), req.Text)
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}

	store, err := s.storeGetter(r.Context())
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}

	meta := make(map[string]string)
	for k, v := range req.Metadata {
		meta[k] = v
	}
	meta["type"] = "manual_context"
	meta["source"] = req.Source

	id := fmt.Sprintf("manual_%d", time.Now().UnixNano())

	err = store.Insert(r.Context(), []db.Record{{
		ID:        id,
		Content:   req.Text,
		Embedding: emb,
		Metadata:  meta,
	}})

	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}

	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(map[string]string{
		"status":  "success",
		"message": "Context added to Global Brain",
	})
}

type TodoRequest struct {
	Title       string `json:"title"`
	Description string `json:"description"`
	Priority    string `json:"priority"`
}

func (s *Server) handleTodo(w http.ResponseWriter, r *http.Request) {
	var req TodoRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	}

	combinedText := req.Title + "\n" + req.Description
	emb, err := s.embedder.Embed(r.Context(), combinedText)
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}

	store, err := s.storeGetter(r.Context())
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}

	meta := map[string]string{
		"type":     "todo",
		"status":   "open",
		"priority": req.Priority,
	}

	id := fmt.Sprintf("todo_%d", time.Now().UnixNano())

	err = store.Insert(r.Context(), []db.Record{{
		ID:        id,
		Content:   combinedText,
		Embedding: emb,
		Metadata:  meta,
	}})

	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}

	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(map[string]string{
		"status":  "success",
		"message": "TODO stored in vector database",
	})
}

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

func (s *Server) handleCreateSession(w http.ResponseWriter, r *http.Request) {
	if s.chatStore == nil {
		http.Error(w, "Chat store not initialized", http.StatusInternalServerError)
		return
	}

	var req struct {
		Title string `json:"title"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
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

type ChatMessage struct {
	Role    string `json:"role"`
	Content string `json:"content"`
}

type ChatRequest struct {
	SessionID string `json:"session_id"`
	Model     string `json:"model"`
	Message   string `json:"message"`
}

type ChatResponse struct {
	ModelUsed string `json:"model_used"`
	Role      string `json:"role"`
	Content   string `json:"content"`
}

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

	records, err := store.Search(r.Context(), emb, 10, []string{})
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
	tools := []llm.Tool{
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

	endpointURL := ""
	if h := r.Header.Get("X-Test-Gemini-URL"); h != "" {
		endpointURL = h
	}

	// 5. Call Gemini
	resp, err := llm.GenerateGeminiCompletion(r.Context(), s.cfg.GeminiApiKey, model, systemPrompt, history.Messages, tools, endpointURL)
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}

	// 6. Handle Function Calling loop
	if resp.FunctionCall != nil && resp.FunctionCall.Name == "save_manual_context" {
		contentInter, ok := resp.FunctionCall.Args["content"]
		if ok {
			contentStr, _ := contentInter.(string)

			// Save to LanceDB
			emb, _ := s.embedder.Embed(r.Context(), contentStr)
			meta := map[string]string{
				"type":   "manual_context",
				"source": "agentic_memory",
			}
			id := fmt.Sprintf("manual_%d", time.Now().UnixNano())
			store.Insert(r.Context(), []db.Record{{
				ID:        id,
				Content:   contentStr,
				Embedding: emb,
				Metadata:  meta,
			}})

			// Append FunctionCall to history
			fnCallMsg := llm.Message{
				Role:         "assistant",
				FunctionCall: resp.FunctionCall,
			}
			s.chatStore.AppendMessage(req.SessionID, fnCallMsg)
			history.Messages = append(history.Messages, fnCallMsg)

			// Append FunctionResponse to history
			fnRespMsg := llm.Message{
				Role: "function",
				FunctionResponse: &llm.FunctionResponse{
					Name: "save_manual_context",
					Response: map[string]interface{}{
						"status": "success",
						"message": "Context saved successfully to Global Brain.",
					},
				},
			}
			s.chatStore.AppendMessage(req.SessionID, fnRespMsg)
			history.Messages = append(history.Messages, fnRespMsg)

			// Call Gemini again
			resp, err = llm.GenerateGeminiCompletion(r.Context(), s.cfg.GeminiApiKey, model, systemPrompt, history.Messages, tools, endpointURL)
			if err != nil {
				http.Error(w, err.Error(), http.StatusInternalServerError)
				return
			}
		}
	}

	// 7. Append Assistant final message
	assistantMsg := llm.Message{Role: "assistant", Content: resp.Text}
	s.chatStore.AppendMessage(req.SessionID, assistantMsg)

	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(ChatResponse{
		ModelUsed: model,
		Role:      "assistant",
		Content:   resp.Text,
	})
}
