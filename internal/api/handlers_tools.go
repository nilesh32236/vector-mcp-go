package api

import (
	"encoding/json"
	"fmt"
	"net/http"
	"time"

	"github.com/nilesh32236/vector-mcp-go/internal/db"
	"github.com/nilesh32236/vector-mcp-go/internal/util"
)

// SearchRequest defines the criteria for a semantic and lexical search.
type SearchRequest struct {
	Query    string `json:"query"`     // The natural language search term
	TopK     int    `json:"top_k"`     // Maximum number of results to return
	DocsOnly bool   `json:"docs_only"` // If true, only searches documentation files
}

// SearchResponse represents a single relevant match from the vector database.
type SearchResponse struct {
	ID         string            `json:"id"`         // Unique identifier for the chunk
	Text       string            `json:"text"`       // The content of the code or document chunk
	Similarity float32           `json:"similarity"` // Ranking score (Higher is better)
	Metadata   map[string]string `json:"metadata"`   // Additional context (path, lines, etc.)
}

// handleSearch performs a hybrid search across the global brain (all projects).
func (s *Server) handleSearch(w http.ResponseWriter, r *http.Request) {
	var req SearchRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	}

	req.TopK = util.ClampInt(req.TopK, 1, 100)

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
		text := util.TruncateRuneSafe(rec.Content, 4000)
		if text != rec.Content {
			text += "\n... [Truncated for length]"
		}
		resp = append(resp, SearchResponse{
			ID:         rec.ID,
			Text:       text,
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

// ContextRequest defines the payload for manually adding context to the database.
type ContextRequest struct {
	Text     string            `json:"text"`     // The content to index
	Source   string            `json:"source"`   // Where this information came from
	Metadata map[string]string `json:"metadata"` // Optional additional metadata
}

// handleContext adds manual rules or knowledge to the vector database.
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

// TodoRequest defines the payload for creating a new TODO item in the vector database.
type TodoRequest struct {
	Title       string `json:"title"`       // Short summary of the task
	Description string `json:"description"` // Detailed explanation
	Priority    string `json:"priority"`    // Task priority level
}

// handleTodo stores a task or reminder in the vector database for semantic retrieval.
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

// handleListTools returns a list of all tools available via the linked MCP server.
func (s *Server) handleListTools(w http.ResponseWriter, r *http.Request) {
	if s.mcpServer == nil {
		http.Error(w, "MCP server not available", http.StatusInternalServerError)
		return
	}

	tools := s.mcpServer.ListTools()
	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(tools)
}

// handleCallTool proxy a tool execution request to the linked MCP server.
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

	result, err := s.mcpServer.CallTool(r.Context(), req.Name, req.Arguments)
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}

	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(result)
}

// handleIndexStatus proxy the 'index_status' tool call.
func (s *Server) handleIndexStatus(w http.ResponseWriter, r *http.Request) {
	if s.mcpServer == nil {
		http.Error(w, "MCP server not available", http.StatusInternalServerError)
		return
	}
	result, err := s.mcpServer.CallTool(r.Context(), "index_status", nil)
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(result)
}

// handleTriggerIndex proxy the 'trigger_project_index' tool call.
func (s *Server) handleTriggerIndex(w http.ResponseWriter, r *http.Request) {
	if s.mcpServer == nil {
		http.Error(w, "MCP server not available", http.StatusInternalServerError)
		return
	}
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

// Repo represents an indexed project/repository.
type Repo struct {
	Path   string `json:"path"`
	Status string `json:"status"`
}

// handleListRepos returns all projects that have been indexed (as found in statuses).
func (s *Server) handleListRepos(w http.ResponseWriter, r *http.Request) {
	store, err := s.storeGetter(r.Context())
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}

	statuses, err := store.GetAllStatuses(r.Context())
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}

	var repos []Repo
	for path, status := range statuses {
		repos = append(repos, Repo{
			Path:   path,
			Status: status,
		})
	}

	if repos == nil {
		repos = []Repo{}
	}

	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(repos)
}

// handleGetSkeleton proxy the 'get_codebase_skeleton' tool call.
func (s *Server) handleGetSkeleton(w http.ResponseWriter, r *http.Request) {
	if s.mcpServer == nil {
		http.Error(w, "MCP server not available", http.StatusInternalServerError)
		return
	}
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
