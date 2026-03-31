package mcp

import (
	"context"
	"encoding/json"
	"fmt"
	"log/slog"
	"path/filepath"
	"sync"

	"github.com/modelcontextprotocol/go-sdk/mcp"
	"github.com/nilesh32236/vector-mcp-go/internal/config"
	"github.com/nilesh32236/vector-mcp-go/internal/daemon"
	"github.com/nilesh32236/vector-mcp-go/internal/db"
	"github.com/nilesh32236/vector-mcp-go/internal/indexer"
	"github.com/nilesh32236/vector-mcp-go/internal/lsp"
	"github.com/nilesh32236/vector-mcp-go/internal/mutation"
	"github.com/nilesh32236/vector-mcp-go/internal/system"
	"github.com/nilesh32236/vector-mcp-go/internal/util"
)

// Searcher defines the interface for searching the vector database.
type Searcher interface {
	Search(ctx context.Context, embedding []float32, topK int, projectIDs []string, category string) ([]db.Record, error)
	SearchWithScore(ctx context.Context, queryEmbedding []float32, topK int, projectIDs []string, category string) ([]db.Record, []float32, error)
	HybridSearch(ctx context.Context, query string, queryEmbedding []float32, topK int, projectIDs []string, category string) ([]db.Record, error)
	LexicalSearch(ctx context.Context, query string, topK int, projectIDs []string, category string) ([]db.Record, error)
}

// StatusProvider defines the interface for project status monitoring.
type StatusProvider interface {
	GetStatus(ctx context.Context, projectID string) (string, error)
	GetAllStatuses(ctx context.Context) (map[string]string, error)
}

// StoreManager defines the interface for managing project data and record lifecycle.
type StoreManager interface {
	Insert(ctx context.Context, records []db.Record) error
	DeleteByPrefix(ctx context.Context, prefix string, projectID string) error
	ClearProject(ctx context.Context, projectID string) error
	GetPathHashMapping(ctx context.Context, projectID string) (map[string]string, error)
	GetAllRecords(ctx context.Context) ([]db.Record, error)
	GetAllMetadata(ctx context.Context) ([]db.Record, error)
	GetByPath(ctx context.Context, path string, projectID string) ([]db.Record, error)
	GetByPrefix(ctx context.Context, prefix string, projectID string) ([]db.Record, error)
	Count() int64
}

// IndexerStore defines the composite interface for database operations,
// allowing both local and remote implementations to be used interchangeably.
type IndexerStore interface {
	Searcher
	StatusProvider
	StoreManager
}

// Server is the core MCP server implementation. It manages the lifecycle of
// the MCP server, registers available tools, and routes incoming tool calls
// to their respective handlers.
type Server struct {
	cfg              *config.Config                                                                                  // Server configuration
	logger           *slog.Logger                                                                                    // Structured logger
	MCPServer        *mcp.Server                                                                                     // Underlying MCP server instance
	localStoreGetter func(ctx context.Context) (*db.Store, error)                                                    // Function to get local store
	remoteStore      IndexerStore                                                                                    // Optional remote store implementation
	embedder         indexer.Embedder                                                                                // Embedding engine for semantic operations
	indexQueue       chan string                                                                                     // Queue for background indexing tasks
	daemonClient     *daemon.Client                                                                                  // Client for master daemon communication
	progressMap      *sync.Map                                                                                       // Thread-safe map for tracking indexing progress
	watcherResetChan chan string                                                                                     // Channel to signal file watcher resets
	monorepoResolver *indexer.WorkspaceResolver                                                                      // Resolver for monorepo package structures
	lspSessions      map[string]*lsp.LSPManager                                                                      // Map of root paths to LSP managers
	lspMu            sync.Mutex                                                                                      // Mutex for lspSessions map
	throttler        *system.MemThrottler                                                                            // Shared system memory throttler
	safety           *mutation.SafetyChecker                                                                         // Safety checker for mutation integrity
	graph            *db.KnowledgeGraph                                                                              // Code relationship graph for reasoning
	toolHandlers     map[string]func(ctx context.Context, request *mcp.CallToolRequest) (*mcp.CallToolResult, error) // Map of tool names to handlers
}

// NewServer initializes and returns a new Server instance.
// It sets up the internal MCP server and registers all supported tools, resources, and prompts.
func NewServer(cfg *config.Config, logger *slog.Logger, storeGetter func(ctx context.Context) (*db.Store, error), embedder indexer.Embedder, queue chan string, daemonClient *daemon.Client, progress *sync.Map, resetChan chan string, resolver *indexer.WorkspaceResolver, throttler *system.MemThrottler) *Server {
	s := mcp.NewServer(
		&mcp.Implementation{
			Name:    "vector-mcp-go",
			Version: "1.0.0",
		},
		&mcp.ServerOptions{
			Logger: logger,
		},
	)
	srv := &Server{
		cfg:              cfg,
		logger:           logger,
		MCPServer:        s,
		localStoreGetter: storeGetter,
		embedder:         embedder,
		indexQueue:       queue,
		daemonClient:     daemonClient,
		progressMap:      progress,
		watcherResetChan: resetChan,
		monorepoResolver: resolver,
		lspSessions:      make(map[string]*lsp.LSPManager),
		throttler:        throttler,
		graph:            db.NewKnowledgeGraph(),
		toolHandlers:     make(map[string]func(ctx context.Context, request *mcp.CallToolRequest) (*mcp.CallToolResult, error)),
	}

	// Initialize SafetyChecker with a provider that uses the server's session management
	srv.safety = mutation.NewSafetyChecker(func(path string) (*lsp.LSPManager, error) {
		manager, _, err := srv.getLSPManagerForFile(path)
		return manager, err
	})

	srv.registerResources()
	srv.registerPrompts()
	srv.registerTools()
	return srv
}

// getLSPManagerForFile resolves the workspace root for a given file and returns
// the appropriate LSPManager session, starting it if necessary.
func (s *Server) getLSPManagerForFile(filePath string) (*lsp.LSPManager, string, error) {
	// 1. Resolve workspace root
	root, err := util.FindWorkspaceRoot(filePath)
	if err != nil {
		s.logger.Warn("Failed to find workspace root, falling back to project root", "path", filePath, "error", err)
		root = s.cfg.ProjectRoot
	}

	// 2. Determine LSP command for file extension
	ext := filepath.Ext(filePath)
	cmd, ok := lsp.GetServerCommand(ext)
	if !ok {
		return nil, "", fmt.Errorf("no language server configured for extension %s", ext)
	}

	// 3. Get or create session
	s.lspMu.Lock()
	defer s.lspMu.Unlock()

	sessionKey := fmt.Sprintf("%s:%s", root, cmd[0])
	if manager, ok := s.lspSessions[sessionKey]; ok {
		return manager, root, nil
	}

	manager := lsp.NewLSPManager(cmd, root, s.logger, s.throttler)
	s.lspSessions[sessionKey] = manager
	return manager, root, nil
}

// WithRemoteStore sets a remote store for the server, enabling it to act as a slave
// and delegate database operations to a master instance.
func (s *Server) WithRemoteStore(rs IndexerStore) {
	s.remoteStore = rs
}

// getStore returns the active store implementation. It prefers the remote store
// if configured; otherwise, it uses the local store getter.
func (s *Server) getStore(ctx context.Context) (IndexerStore, error) {
	if s.remoteStore != nil {
		return s.remoteStore, nil
	}
	return s.localStoreGetter(ctx)
}

// PopulateGraph builds the structural knowledge graph from all records in the store.
func (s *Server) PopulateGraph(ctx context.Context) error {
	s.logger.Info("Populating structural knowledge graph")

	store, err := s.getStore(ctx)
	if err != nil {
		return fmt.Errorf("failed to get store for graph population: %w", err)
	}

	records, err := store.GetAllRecords(ctx)
	if err != nil {
		return fmt.Errorf("failed to fetch records for graph: %w", err)
	}

	s.graph.Populate(records)
	s.logger.Info("Knowledge graph populated", "node_count", len(records))
	return nil
}

// Serve starts the MCP server on stdio.
func (s *Server) Serve() error {
	s.logger.Info("MCP Server listening on stdio...")
	return s.MCPServer.Run(context.Background(), &mcp.StdioTransport{})
}

// registerResources defines and registers all available MCP resources.
func (s *Server) registerResources() {
	s.MCPServer.AddResource(&mcp.Resource{
		URI:         "index://status",
		Name:        "Indexing Status",
		Description: "Current indexing status and background progress diagnostics.",
		MIMEType:    "application/json",
	}, func(ctx context.Context, request *mcp.ReadResourceRequest) (*mcp.ReadResourceResult, error) {
		status := "Idle"
		if s.progressMap != nil {
			if val, ok := s.progressMap.Load(s.cfg.ProjectRoot); ok {
				status = val.(string)
			}
		}

		store, _ := s.getStore(ctx)
		count := int64(0)
		if store != nil {
			count = store.Count()
		}

		data := map[string]interface{}{
			"project_root": s.cfg.ProjectRoot,
			"status":       status,
			"record_count": count,
			"is_master":    s.remoteStore == nil,
			"model":        s.cfg.ModelName,
		}
		jsonBytes, _ := json.MarshalIndent(data, "", "  ")

		return &mcp.ReadResourceResult{
			Contents: []mcp.ResourceContents{
				&mcp.TextResourceContents{
					URI:      "index://status",
					MIMEType: "application/json",
					Text:     string(jsonBytes),
				},
			},
		}, nil
	})

	s.MCPServer.AddResource(&mcp.Resource{
		URI:         "config://project",
		Name:        "Project Configuration",
		Description: "Active configuration for the vector-mcp-go server.",
		MIMEType:    "application/json",
	}, func(ctx context.Context, request *mcp.ReadResourceRequest) (*mcp.ReadResourceResult, error) {
		jsonBytes, _ := json.MarshalIndent(s.cfg, "", "  ")
		return &mcp.ReadResourceResult{
			Contents: []mcp.ResourceContents{
				&mcp.TextResourceContents{
					URI:      "config://project",
					MIMEType: "application/json",
					Text:     string(jsonBytes),
				},
			},
		}, nil
	})

	s.MCPServer.AddResource(&mcp.Resource{
		URI:         "docs://guide",
		Name:        "Usage Guide",
		Description: "Quick guide on how to use vector-mcp-go effectively.",
		MIMEType:    "text/markdown",
	}, func(ctx context.Context, request *mcp.ReadResourceRequest) (*mcp.ReadResourceResult, error) {
		guide := `
# Vector MCP Go Usage Guide

This server provides semantic search and code analysis for your project.

## Core Resources
- **index://status**: Check if indexing is complete.
- **config://project**: View active server settings.

## Recommended Prompts
- **generate-docstring**: Use this to write high-quality documentation for functions or classes.
- **analyze-architecture**: Get a visual overview of your project structure.

## Key Tools
- **search_workspace** (action="vector"): Your primary tool for semantic search.
- **get_related_context**: Best for understanding a specific file's dependencies.
- **trigger_project_index**: Run this if you've made major changes to ensure the index is fresh.
`
		return &mcp.ReadResourceResult{
			Contents: []mcp.ResourceContents{
				&mcp.TextResourceContents{
					URI:      "docs://guide",
					MIMEType: "text/markdown",
					Text:     guide,
				},
			},
		}, nil
	})
}

// registerPrompts defines and registers all available MCP prompts.
func (s *Server) registerPrompts() {
	s.MCPServer.AddPrompt(&mcp.Prompt{
		Name:        "generate-docstring",
		Description: "Generates a highly contextual prompt to write professional documentation.",
		Arguments: []*mcp.PromptArgument{
			{Name: "file_path", Description: "The relative path of the file", Required: true},
			{Name: "entity_name", Description: "The name of the function or class to document", Required: true},
		},
	}, func(ctx context.Context, request *mcp.GetPromptRequest) (*mcp.GetPromptResult, error) {
		filePath := request.Params.Arguments["file_path"]
		entityName := request.Params.Arguments["entity_name"]

		prompt := fmt.Sprintf("Please generate a professional docstring for the entity '%s' in file '%s'. "+
			"Include parameter descriptions, return values, and any relevant implementation details based on the context.",
			entityName, filePath)

		return &mcp.GetPromptResult{
			Description: "Prompt for generating documentation",
			Messages: []*mcp.PromptMessage{
				{
					Role: mcp.RoleUser,
					Content: &mcp.TextContent{
						Text: prompt,
					},
				},
			},
		}, nil
	})

	s.MCPServer.AddPrompt(&mcp.Prompt{
		Name:        "analyze-architecture",
		Description: "Analyzes the project architecture and generates a summary prompt.",
	}, func(ctx context.Context, request *mcp.GetPromptRequest) (*mcp.GetPromptResult, error) {
		prompt := "Analyze the current project's architecture. Focus on package boundaries, dependency flow, and key design patterns used. If this is a monorepo, identify the core packages and their interactions."

		return &mcp.GetPromptResult{
			Description: "Architectural analysis prompt",
			Messages: []*mcp.PromptMessage{
				{
					Role: mcp.RoleUser,
					Content: &mcp.TextContent{
						Text: prompt,
					},
				},
			},
		}, nil
	})
}

// registerTools defines and registers all available MCP tools and their handlers.
func (s *Server) registerTools() {
	// 1. search_workspace: Unified search engine (Fat Tool)
	s.MCPServer.AddTool(&mcp.Tool{
		Name:        "search_workspace",
		Description: "Unified search engine for deep codebase exploration. Use this for semantic search (vector), exact text/regex matching (ripgrep), following code relationship graphs (calls/imports), or checking indexing progress.",
		InputSchema: map[string]interface{}{
			"type": "object",
			"properties": map[string]interface{}{
				"action": map[string]interface{}{"type": "string", "description": "The type of search: 'vector', 'regex', 'graph', or 'index_status'."},
				"query":  map[string]interface{}{"type": "string", "description": "The search query, symbol name, or regex pattern."},
				"limit":  map[string]interface{}{"type": "number", "description": "Max number of results to return (default 10)."},
				"path":   map[string]interface{}{"type": "string", "description": "Optional file or directory path to restrict the search scope."},
			},
		},
	}, s.handleSearchWorkspace)

	// 2. workspace_manager: Project lifecycle commands (Fat Tool)
	s.MCPServer.AddTool(&mcp.Tool{
		Name:        "workspace_manager",
		Description: "Core project management tools. Use this to switch active project roots, trigger specialized indexing runs, or retrieve detailed system diagnostics and state reports.",
		InputSchema: map[string]interface{}{
			"type": "object",
			"properties": map[string]interface{}{
				"action": map[string]interface{}{"type": "string", "description": "Management action: 'set_project_root', 'trigger_index', or 'get_indexing_diagnostics'."},
				"path":   map[string]interface{}{"type": "string", "description": "The absolute path to the project root or a specific directory to act upon."},
			},
		},
	}, s.handleWorkspaceManager)

	// 3. lsp_query: Deep Language Server Protocol integration (Fat Tool)
	s.MCPServer.AddTool(&mcp.Tool{
		Name:        "lsp_query",
		Description: "High-precision symbol analysis via the Language Server Protocol (LSP). Use this for jumping to definitions, finding all references across the workspace, exploring large type hierarchies, or analyzing the impact of cross-file changes.",
		InputSchema: map[string]interface{}{
			"type": "object",
			"properties": map[string]interface{}{
				"action":    map[string]interface{}{"type": "string", "description": "LSP capability: 'definition', 'references', 'type_hierarchy', or 'impact_analysis'."},
				"path":      map[string]interface{}{"type": "string", "description": "Absolute path to the file containing the symbol."},
				"line":      map[string]interface{}{"type": "number", "description": "0-indexed line number of the symbol."},
				"character": map[string]interface{}{"type": "number", "description": "0-indexed character offset of the symbol."},
			},
		},
	}, s.handleLspQuery)

	// 4. analyze_code: Codebase diagnostics (Fat Tool)
	s.MCPServer.AddTool(&mcp.Tool{
		Name:        "analyze_code",
		Description: "Advanced codebase diagnostic suite. Use this to generate AST-based structural skeletons, detect dead (unused) exported symbols, find semantically duplicated code blocks, or validate dependency health against manifest files.",
		InputSchema: map[string]interface{}{
			"type": "object",
			"properties": map[string]interface{}{
				"action": map[string]interface{}{"type": "string", "description": "Analysis type: 'ast_skeleton', 'dead_code', 'duplicate_code', or 'dependencies'."},
				"path":   map[string]interface{}{"type": "string", "description": "Subdirectory or file path to analyze."},
			},
		},
	}, s.handleAnalyzeCode)

	// 5. modify_workspace: Safe file mutation (Fat Tool)
	s.MCPServer.AddTool(&mcp.Tool{
		Name:        "modify_workspace",
		Description: "Safe and structured codebase mutation tools. Use this for applying small search-and-replace patches, creating new files with content, or running formatters/linters (like go fmt) to ensure code quality.",
		InputSchema: map[string]interface{}{
			"type": "object",
			"properties": map[string]interface{}{
				"action":  map[string]interface{}{"type": "string", "description": "Mutation action: 'apply_patch', 'create_file', 'run_linter', 'verify_patch', or 'auto_fix'."},
				"path":    map[string]interface{}{"type": "string", "description": "Target file path for the mutation."},
				"content": map[string]interface{}{"type": "string", "description": "Complete file content or patch context."},
				"search":  map[string]interface{}{"type": "string", "description": "Exact text block to find and replace."},
				"replace": map[string]interface{}{"type": "string", "description": "New text block to insert."},
				"tool":    map[string]interface{}{"type": "string", "description": "Linter or formatter tool name (e.g., 'go fmt')."},
			},
		},
	}, s.handleModifyWorkspace)

	// Individual Utility Tools
	s.MCPServer.AddTool(&mcp.Tool{
		Name:        "index_status",
		Description: "Check current indexing status and background progress.",
		InputSchema: map[string]interface{}{"type": "object"},
	}, s.handleIndexStatus)

	s.MCPServer.AddTool(&mcp.Tool{
		Name:        "trigger_project_index",
		Description: "Manually trigger a full re-index of the project.",
		InputSchema: map[string]interface{}{
			"type": "object",
			"properties": map[string]interface{}{
				"project_path": map[string]interface{}{"type": "string", "description": "Absolute path to the project root."},
			},
		},
	}, s.handleTriggerProjectIndex)

	s.MCPServer.AddTool(&mcp.Tool{
		Name:        "get_related_context",
		Description: "Retrieve semantically related code and symbols for a specific file.",
		InputSchema: map[string]interface{}{
			"type": "object",
			"properties": map[string]interface{}{
				"filePath": map[string]interface{}{"type": "string", "description": "Path to the source file."},
			},
		},
	}, s.handleGetRelatedContext)

	s.MCPServer.AddTool(&mcp.Tool{
		Name:        "store_context",
		Description: "Store general project rules or architectural decisions.",
		InputSchema: map[string]interface{}{
			"type": "object",
			"properties": map[string]interface{}{
				"text": map[string]interface{}{"type": "string", "description": "The text context to store."},
			},
		},
	}, s.handleStoreContext)

	s.MCPServer.AddTool(&mcp.Tool{
		Name:        "delete_context",
		Description: "Delete specific shared memory context, or completely wipe a project's vector index.",
		InputSchema: map[string]interface{}{
			"type": "object",
			"properties": map[string]interface{}{
				"target_path": map[string]interface{}{"type": "string", "description": "The exact file path, context ID, or 'ALL' to clear the whole project."},
			},
		},
	}, s.handleDeleteContext)

	s.MCPServer.AddTool(&mcp.Tool{
		Name:        "distill_package_purpose",
		Description: "Generates a high-level semantic summary of a package's primary purpose and key entities.",
		InputSchema: map[string]interface{}{
			"type": "object",
			"properties": map[string]interface{}{
				"path": map[string]interface{}{"type": "string", "description": "Relative or absolute path of the package directory to distill."},
			},
		},
	}, s.handleDistillPackagePurpose)

	s.MCPServer.AddTool(&mcp.Tool{
		Name:        "trace_data_flow",
		Description: "Traces the usage of a specific field or symbol across the project.",
		InputSchema: map[string]interface{}{
			"type": "object",
			"properties": map[string]interface{}{
				"field_name": map[string]interface{}{"type": "string", "description": "The name of the field or symbol to trace"},
			},
		},
	}, s.handleTraceDataFlow)
}

// SendNotification sends a logging message notification to all connected clients.
func (s *Server) SendNotification(level mcp.LoggingLevel, data any, logger string) {
	params := &mcp.LoggingMessageParams{
		Level: level,
		Data:  data,
	}
	if logger != "" {
		params.Logger = logger
	}

	for session := range s.MCPServer.Sessions() {
		// Use background context for notifications
		_ = session.LoggingMessage(context.Background(), params)
	}
}

// Notify is a helper to send info-level notifications.
func (s *Server) Notify(message string) {
	s.SendNotification(mcp.LoggingLevelInfo, message, "vector-mcp")
}

// Log is a helper to send debug/log-level notifications.
func (s *Server) Log(level mcp.LoggingLevel, message string) {
	s.SendNotification(level, message, "vector-mcp")
}

// CallTool invokes a registered tool handler by name with the provided arguments.
func (s *Server) CallTool(ctx context.Context, name string, args map[string]interface{}) (*mcp.CallToolResult, error) {
	// Re-populating toolHandlers is done during registerTools
	// This is temporarily placeholder until we decide how to handle internal calls
	return nil, fmt.Errorf("internal CallTool not fully implemented in refactor yet")
}

// GetEmbedder returns the embedding engine used by the server.
func (s *Server) GetEmbedder() indexer.Embedder {
	return s.embedder
}
