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
	"time"

	mcp_server "github.com/mark3labs/mcp-go/server"
	"github.com/nilesh32236/vector-mcp-go/internal/config"
	"github.com/nilesh32236/vector-mcp-go/internal/db"
	"github.com/nilesh32236/vector-mcp-go/internal/indexer"
	"github.com/nilesh32236/vector-mcp-go/internal/mcp"
	"github.com/nilesh32236/vector-mcp-go/internal/security/ratelimit"
)

// StoreGetter is a function type that retrieves the active vector database store.
type StoreGetter func(ctx context.Context) (*db.Store, error)

// Server represents the HTTP API server.
type Server struct {
	cfg         *config.Config        // System configuration
	storeGetter StoreGetter           // Logic to retrieve the database store
	embedder    indexer.Embedder      // Local embedding engine
	srv         *http.Server          // Underlying HTTP server
	mcpServer   *mcp.Server           // Linked MCP server instance for tool execution
	rateLimiter *ratelimit.Middleware // Rate limiting middleware
}

// NewServer initializes and returns a new API Server.
// It sets up routing for chat sessions, semantic search, and MCP tool proxies.
func NewServer(cfg *config.Config, storeGetter StoreGetter, embedder indexer.Embedder, mcpServer *mcp.Server) *Server {

	mux := http.NewServeMux()

	server := &Server{
		cfg:         cfg,
		storeGetter: storeGetter,
		embedder:    embedder,
		mcpServer:   mcpServer,
		// Initialize rate limiter: 30 requests/second per client, burst of 60
		rateLimiter: ratelimit.PerClientRateLimit(30, 60),
	}

	mux.HandleFunc("GET /api/health", server.handleHealth)
	mux.HandleFunc("GET /api/ready", server.handleReady)
	mux.HandleFunc("GET /api/live", server.handleLive)

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

	mux.HandleFunc("POST /api/search", server.handleSearch)
	mux.HandleFunc("POST /api/context", server.handleContext)
	mux.HandleFunc("POST /api/todo", server.handleTodo)

	// New Tool Management Endpoints
	mux.HandleFunc("GET /api/tools/repos", server.handleListRepos)
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

	// Wrap with rate limiting middleware
	rateLimitedHandler := server.rateLimiter.Handler(corsMux)

	server.srv = &http.Server{
		Addr:    addr,
		Handler: rateLimitedHandler,
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

	// Run health checks
	checks := make(map[string]interface{})
	allHealthy := true

	// Database check
	dbStart := time.Now()
	dbStatus := "ok"
	if s.storeGetter != nil {
		ctx, cancel := context.WithTimeout(r.Context(), 2*time.Second)
		defer cancel()
		_, err := s.storeGetter(ctx)
		if err != nil {
			dbStatus = "unhealthy"
			allHealthy = false
		}
	}
	checks["database"] = map[string]interface{}{
		"status":     dbStatus,
		"latency_ms": time.Since(dbStart).Milliseconds(),
	}

	// Embedder check (if available)
	if s.embedder != nil {
		checks["embedder"] = map[string]interface{}{
			"status": "ok",
		}
	}

	// Rate limiter stats
	if s.rateLimiter != nil {
		stats := s.rateLimiter.GetStats()
		checks["rate_limiter"] = map[string]interface{}{
			"status":        "ok",
			"total_buckets": stats.TotalBuckets,
		}
	}

	// Overall status
	status := "ok"
	if !allHealthy {
		status = "degraded"
	}

	response := map[string]interface{}{
		"status":  status,
		"version": "1.1.0",
		"checks":  checks,
	}

	if allHealthy {
		w.WriteHeader(http.StatusOK)
	} else {
		w.WriteHeader(http.StatusServiceUnavailable)
	}

	json.NewEncoder(w).Encode(response)
}

// handleReady reports whether the server is ready to accept traffic.
func (s *Server) handleReady(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Content-Type", "application/json")

	// Check if essential components are ready
	ready := true
	reasons := []string{}

	// Check database
	if s.storeGetter != nil {
		ctx, cancel := context.WithTimeout(r.Context(), 2*time.Second)
		defer cancel()
		_, err := s.storeGetter(ctx)
		if err != nil {
			ready = false
			reasons = append(reasons, "database not ready")
		}
	}

	response := map[string]interface{}{
		"ready": ready,
	}

	if !ready {
		response["reasons"] = reasons
		w.WriteHeader(http.StatusServiceUnavailable)
	} else {
		w.WriteHeader(http.StatusOK)
	}

	json.NewEncoder(w).Encode(response)
}

// handleLive reports whether the server process is alive.
func (s *Server) handleLive(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(http.StatusOK)
	json.NewEncoder(w).Encode(map[string]string{
		"status": "alive",
	})
}
