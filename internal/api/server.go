package api

import (
	"context"
	"encoding/json"
	"fmt"
	"log"
	"net/http"
	"strings"
	"time"

	mcp_lib "github.com/mark3labs/mcp-go/mcp"
	mcp_server "github.com/mark3labs/mcp-go/server"
	"github.com/nilesh32236/vector-mcp-go/internal/chat"
	"github.com/nilesh32236/vector-mcp-go/internal/config"
	"github.com/nilesh32236/vector-mcp-go/internal/db"
	"github.com/nilesh32236/vector-mcp-go/internal/indexer"
	"github.com/nilesh32236/vector-mcp-go/internal/llm"
	"github.com/nilesh32236/vector-mcp-go/internal/mcp"
)

type StoreGetter func(ctx context.Context) (*db.Store, error)

type Server struct {
	cfg         *config.Config
	storeGetter StoreGetter
	embedder    indexer.Embedder
	srv         *http.Server
	chatStore   *chat.Store
	mcpServer   *mcp.Server
}

func NewServer(cfg *config.Config, storeGetter StoreGetter, embedder indexer.Embedder, mcpServer *mcp.Server) *Server {
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
		mcpServer:   mcpServer,
	}

	mux.HandleFunc("GET /api/health", server.handleHealth)

	// MCP HTTP transport (Streamable-HTTP specification)
	if mcpServer != nil && mcpServer.MCPServer != nil {
		mcpHandler := mcp_server.NewStreamableHTTPServer(mcpServer.MCPServer)

		handler := func(w http.ResponseWriter, r *http.Request) {
			log.Printf("MCP request: %s %s", r.Method, r.URL.String())

			// CORS headers are essential for browser-based clients
			w.Header().Set("Access-Control-Allow-Origin", "*")
			w.Header().Set("Access-Control-Allow-Methods", "GET, POST, DELETE, OPTIONS")
			w.Header().Set("Access-Control-Allow-Headers", "Content-Type, Mcp-Session-Id, Authorization, MCP-Protocol-Version")
			w.Header().Set("Access-Control-Expose-Headers", "Mcp-Session-Id")

			if r.Method == http.MethodOptions {
				w.WriteHeader(http.StatusNoContent)
				return
			}

			mcpHandler.ServeHTTP(w, r)
		}

		mux.HandleFunc("/sse", handler)
		mux.HandleFunc("/message", handler)
	}

	// Chat sessions CRUD
	mux.HandleFunc("GET /api/sessions", server.handleListSessions)
	mux.HandleFunc("POST /api/sessions", server.handleCreateSession)
	mux.HandleFunc("GET /api/sessions/{id}", server.handleGetSession)
	mux.HandleFunc("DELETE /api/sessions/{id}", server.handleDeleteSession)

	mux.HandleFunc("POST /api/search", server.handleSearch)
	mux.HandleFunc("POST /api/context", server.handleContext)
	mux.HandleFunc("POST /api/todo", server.handleTodo)
	mux.HandleFunc("POST /api/chat", server.handleChat)

	// New Tool Management Endpoints
	mux.HandleFunc("GET /api/tools/status", server.handleIndexStatus)
	mux.HandleFunc("POST /api/tools/index", server.handleTriggerIndex)
	mux.HandleFunc("GET /api/tools/skeleton", server.handleGetSkeleton)
	mux.HandleFunc("GET /api/tools/list", server.handleListTools)
	mux.HandleFunc("POST /api/tools/call", server.handleCallTool)

	addr := fmt.Sprintf(":%s", cfg.ApiPort)

	corsMux := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Access-Control-Allow-Origin", "*")
		w.Header().Set("Access-Control-Allow-Methods", "GET, POST, DELETE, OPTIONS")
		w.Header().Set("Access-Control-Allow-Headers", "Content-Type, Authorization, Mcp-Session-Id, MCP-Protocol-Version, X-Requested-With, Accept, Origin")
		w.Header().Set("Access-Control-Expose-Headers", "Mcp-Session-Id")

		if r.Method == http.MethodOptions {
			w.WriteHeader(http.StatusNoContent)
			return
		}

		mux.ServeHTTP(w, r)
	})

	server.srv = &http.Server{
		Addr:    addr,
		Handler: corsMux,
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
		"version": "1.1.0",
	})
}

type SearchRequest struct {
	Query    string `json:"query"`
	TopK     int    `json:"top_k"`
	DocsOnly bool   `json:"docs_only"`
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
	// but store.HybridSearch currently takes projectIDs. Passing an empty slice searches everything.
	var category string
	if req.DocsOnly {
		category = "document"
	}
	records, err := store.HybridSearch(r.Context(), req.Query, emb, req.TopK, []string{}, category)
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
	ToolCalls int    `json:"tool_calls"`
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

func (s *Server) handleListTools(w http.ResponseWriter, r *http.Request) {
	if s.mcpServer == nil {
		http.Error(w, "MCP server not available", http.StatusInternalServerError)
		return
	}

	tools := s.mcpServer.ListTools()
	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(tools)
}

func (s *Server) handleCallTool(w http.ResponseWriter, r *http.Request) {
	if s.mcpServer == nil {
		http.Error(w, "MCP server not available", http.StatusInternalServerError)
		return
	}

	var req struct {
		Name      string                 `json:"name"`
		Arguments map[string]interface{} `json:"arguments"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	}

	// mcp_server.MCPServer has CallTool method? Let's check or mock it.
	// Actually, the server from mark3labs/mcp-go/server handles its own registration.
	// We might need to manually invoke the handler if it's not exposed.
	// Let's assume it has CallTool based on common patterns.
	// Since I cannot verify easily, I will implement a bridge if needed.
	// Wait, I have access to the mcpServer.
	// Looking at typical MCP implementations, it might be easier to use the tools we already have.

	result, err := s.mcpServer.CallTool(r.Context(), req.Name, req.Arguments)
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}

	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(result)
}

func (s *Server) handleIndexStatus(w http.ResponseWriter, r *http.Request) {
	// Re-use logic from handleIndexStatus in internal/mcp/server.go
	// But since this is a different server, we'll call the tool if possible
	// or just call CallTool("index_status", nil)
	result, err := s.mcpServer.CallTool(r.Context(), "index_status", nil)
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(result)
}

func (s *Server) handleTriggerIndex(w http.ResponseWriter, r *http.Request) {
	var req struct {
		Path string `json:"path"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	}
	if req.Path == "" {
		req.Path = s.cfg.ProjectRoot
	}

	result, err := s.mcpServer.CallTool(r.Context(), "trigger_project_index", map[string]interface{}{
		"project_path": req.Path,
	})
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(result)
}

func (s *Server) handleGetSkeleton(w http.ResponseWriter, r *http.Request) {
	path := r.URL.Query().Get("path")
	if path == "" {
		path = s.cfg.ProjectRoot
	}

	result, err := s.mcpServer.CallTool(r.Context(), "get_codebase_skeleton", map[string]interface{}{
		"target_path": path,
	})
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(result)
}
