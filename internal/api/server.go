package api

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"time"

	"github.com/nilesh32236/vector-mcp-go/internal/config"
	"github.com/nilesh32236/vector-mcp-go/internal/db"
	"github.com/nilesh32236/vector-mcp-go/internal/indexer"
)

type StoreGetter func(ctx context.Context) (*db.Store, error)

type Server struct {
	cfg         *config.Config
	storeGetter StoreGetter
	embedder    indexer.Embedder
	srv         *http.Server
}

func NewServer(cfg *config.Config, storeGetter StoreGetter, embedder indexer.Embedder) *Server {
	mux := http.NewServeMux()

	server := &Server{
		cfg:         cfg,
		storeGetter: storeGetter,
		embedder:    embedder,
	}

	mux.HandleFunc("GET /api/health", server.handleHealth)
	mux.HandleFunc("POST /api/search", server.handleSearch)
	mux.HandleFunc("POST /api/context", server.handleContext)
	mux.HandleFunc("POST /api/todo", server.handleTodo)

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
