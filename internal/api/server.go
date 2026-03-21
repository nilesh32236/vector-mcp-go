/*
 * Package api provides the HTTP REST interface for the Vector MCP server.
 * It enables chat interactions, session management, and programmatic access to MCP tools.
 */
package api

import (
	"context"
	"encoding/json"
	"fmt"
	"log"
	"net/http"

	mcp_server "github.com/mark3labs/mcp-go/server"
	"github.com/nilesh32236/vector-mcp-go/internal/chat"
	"github.com/nilesh32236/vector-mcp-go/internal/config"
	"github.com/nilesh32236/vector-mcp-go/internal/db"
	"github.com/nilesh32236/vector-mcp-go/internal/indexer"
	"github.com/nilesh32236/vector-mcp-go/internal/mcp"
)

// StoreGetter is a function type that retrieves the active vector database store.
type StoreGetter func(ctx context.Context) (*db.Store, error)

// Server represents the HTTP API server.
type Server struct {
	cfg         *config.Config   // System configuration
	storeGetter StoreGetter      // Logic to retrieve the database store
	embedder    indexer.Embedder // Local embedding engine
	srv         *http.Server     // Underlying HTTP server
	chatStore   *chat.Store      // Persistent storage for chat history
	mcpServer   *mcp.Server      // Linked MCP server instance for tool execution
}

// NewServer initializes and returns a new API Server.
// It sets up routing for chat sessions, semantic search, and MCP tool proxies.
func NewServer(cfg *config.Config, storeGetter StoreGetter, embedder indexer.Embedder, mcpServer *mcp.Server) *Server {
	chatStore, err := chat.NewStore(cfg.DataDir)
	if err != nil && cfg.Logger != nil {
		cfg.Logger.Error("Failed to initialize chat store", "error", err)
		// We continue but some chat features might be disabled or return errors
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

// Start launches the HTTP API server on the configured port.
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

// Shutdown gracefully stops the HTTP API server.
func (s *Server) Shutdown(ctx context.Context) error {
	if s.cfg.Logger != nil {
		s.cfg.Logger.Info("Shutting down HTTP API server")
	}
	return s.srv.Shutdown(ctx)
}

// handleHealth reports the current status and version of the API server.
func (s *Server) handleHealth(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(map[string]string{
		"status":  "ok",
		"version": "1.1.0",
	})
}
