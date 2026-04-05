/*
 * Package mcp provides the core implementation of the Model Context Protocol (MCP) server.
 * It manages tool registration, request handling, and integrates with the vector database
 * for semantic search and project context management.
 */
package mcp

import (
	"context"
	"encoding/json"
	"fmt"
	"log/slog"
	"path/filepath"
	"sync"

	"github.com/mark3labs/mcp-go/mcp"
	"github.com/mark3labs/mcp-go/server"
	"github.com/nilesh32236/vector-mcp-go/internal/config"
	"github.com/nilesh32236/vector-mcp-go/internal/daemon"
	"github.com/nilesh32236/vector-mcp-go/internal/db"
	"github.com/nilesh32236/vector-mcp-go/internal/indexer"
	"github.com/nilesh32236/vector-mcp-go/internal/lsp"
	"github.com/nilesh32236/vector-mcp-go/internal/mutation"
	"github.com/nilesh32236/vector-mcp-go/internal/observability/metrics"
	"github.com/nilesh32236/vector-mcp-go/internal/observability/tracing"
	"github.com/nilesh32236/vector-mcp-go/internal/security/pathguard"
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
	cfg              *config.Config                                                                                 // Server configuration
	logger           *slog.Logger                                                                                   // Structured logger
	MCPServer        *server.MCPServer                                                                              // Underlying MCP server instance
	localStoreGetter func(ctx context.Context) (*db.Store, error)                                                   // Function to get local store
	remoteStore      IndexerStore                                                                                   // Optional remote store implementation
	embedder         indexer.Embedder                                                                               // Embedding engine for semantic operations
	indexQueue       chan string                                                                                    // Queue for background indexing tasks
	daemonClient     *daemon.Client                                                                                 // Client for master daemon communication
	progressMap      *sync.Map                                                                                      // Thread-safe map for tracking indexing progress
	watcherResetChan chan string                                                                                    // Channel to signal file watcher resets
	monorepoResolver *indexer.WorkspaceResolver                                                                     // Resolver for monorepo package structures
	lspSessions      map[string]*lsp.LSPManager                                                                     // Map of root paths to LSP managers
	lspMu            sync.Mutex                                                                                     // Mutex for lspSessions map
	throttler        *system.MemThrottler                                                                           // Shared system memory throttler
	safety           *mutation.SafetyChecker                                                                        // Safety checker for mutation integrity
	graph            *db.KnowledgeGraph                                                                             // Code relationship graph for reasoning
	pathValidator    *pathguard.Validator                                                                           // Path validator for security
	toolHandlers     map[string]func(ctx context.Context, request mcp.CallToolRequest) (*mcp.CallToolResult, error) // Map of tool names to handlers
}

// NewServer initializes and returns a new Server instance.
// It sets up the internal MCP server and registers all supported tools, resources, and prompts.
func NewServer(cfg *config.Config, logger *slog.Logger, storeGetter func(ctx context.Context) (*db.Store, error), embedder indexer.Embedder, queue chan string, daemonClient *daemon.Client, progress *sync.Map, resetChan chan string, resolver *indexer.WorkspaceResolver, throttler *system.MemThrottler) *Server {
	metrics.InitializeDefaultMetrics()
	s := server.NewMCPServer("vector-mcp-go", "1.0.0", server.WithLogging())
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
		toolHandlers:     make(map[string]func(ctx context.Context, request mcp.CallToolRequest) (*mcp.CallToolResult, error)),
	}

	// Initialize path validator for security
	pathValidator, err := pathguard.NewValidator(cfg.ProjectRoot, pathguard.DefaultOptions())
	if err != nil {
		logger.Warn("failed to create path validator, using permissive fallback", "error", err)
		// Create a permissive validator that at least checks for traversal
		pathValidator, _ = pathguard.NewValidator("/", pathguard.Options{AllowAbsolute: true, MaxPathDepth: 100})
	}
	srv.pathValidator = pathValidator

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
	return server.ServeStdio(s.MCPServer)
}

// registerResources defines and registers all available MCP resources.
func (s *Server) registerResources() {
	// index://status exposes live indexing telemetry for health checks and agent orchestration.
	s.MCPServer.AddResource(mcp.NewResource("index://status", "Indexing Status",
		mcp.WithResourceDescription("Current indexing status and background progress diagnostics."),
		mcp.WithMIMEType("application/json"),
	), func(ctx context.Context, request mcp.ReadResourceRequest) ([]mcp.ResourceContents, error) {
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

		return []mcp.ResourceContents{
			mcp.TextResourceContents{
				URI:      "index://status",
				MIMEType: "application/json",
				Text:     string(jsonBytes),
			},
		}, nil
	})
	// config://project returns the active runtime configuration used by this server process.
	s.MCPServer.AddResource(mcp.NewResource("config://project", "Project Configuration",
		mcp.WithResourceDescription("Active configuration for the vector-mcp-go server."),
		mcp.WithMIMEType("application/json"),
	), func(ctx context.Context, request mcp.ReadResourceRequest) ([]mcp.ResourceContents, error) {
		jsonBytes, _ := json.MarshalIndent(s.cfg, "", "  ")
		return []mcp.ResourceContents{
			mcp.TextResourceContents{
				URI:      "config://project",
				MIMEType: "application/json",
				Text:     string(jsonBytes),
			},
		}, nil
	})
	// docs://guide provides a concise markdown quick-start for client agents and developers.
	s.MCPServer.AddResource(mcp.NewResource("docs://guide", "Usage Guide",
		mcp.WithResourceDescription("Quick guide on how to use vector-mcp-go effectively."),
		mcp.WithMIMEType("text/markdown"),
	), func(ctx context.Context, request mcp.ReadResourceRequest) ([]mcp.ResourceContents, error) {
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
		return []mcp.ResourceContents{
			mcp.TextResourceContents{
				URI:      "docs://guide",
				MIMEType: "text/markdown",
				Text:     guide,
			},
		}, nil
	})
}

// registerPrompts defines and registers all available MCP prompts.
func (s *Server) registerPrompts() {
	// generate-docstring scaffolds a high-context writing prompt for entity-level documentation.
	s.MCPServer.AddPrompt(mcp.NewPrompt("generate-docstring",
		mcp.WithPromptDescription("Generates a highly contextual prompt to write professional documentation."),
		mcp.WithArgument("file_path", mcp.ArgumentDescription("The relative path of the file"), mcp.RequiredArgument()),
		mcp.WithArgument("entity_name", mcp.ArgumentDescription("The name of the function or class to document"), mcp.RequiredArgument()),
	), func(ctx context.Context, request mcp.GetPromptRequest) (*mcp.GetPromptResult, error) {
		filePath := request.Params.Arguments["file_path"]
		entityName := request.Params.Arguments["entity_name"]

		prompt := fmt.Sprintf("Please generate a professional docstring for the entity '%s' in file '%s'. "+
			"Include parameter descriptions, return values, and any relevant implementation details based on the context.",
			entityName, filePath)

		return &mcp.GetPromptResult{
			Description: "Prompt for generating documentation",
			Messages: []mcp.PromptMessage{
				{
					Role: mcp.RoleUser,
					Content: mcp.TextContent{
						Type: "text",
						Text: prompt,
					},
				},
			},
		}, nil
	})
	// analyze-architecture produces an architecture-review prompt for system-level reasoning.
	s.MCPServer.AddPrompt(mcp.NewPrompt("analyze-architecture",
		mcp.WithPromptDescription("Analyzes the project architecture and generates a summary prompt."),
	), func(ctx context.Context, request mcp.GetPromptRequest) (*mcp.GetPromptResult, error) {
		prompt := "Analyze the current project's architecture. Focus on package boundaries, dependency flow, and key design patterns used. If this is a monorepo, identify the core packages and their interactions."

		return &mcp.GetPromptResult{
			Description: "Architectural analysis prompt",
			Messages: []mcp.PromptMessage{
				{
					Role: mcp.RoleUser,
					Content: mcp.TextContent{
						Type: "text",
						Text: prompt,
					},
				},
			},
		}, nil
	})
}

// registerTools defines and registers all available MCP tools and their handlers.
func (s *Server) registerTools() {
	// Helper to add tool and track handler
	addTool := func(tool mcp.Tool, handler func(ctx context.Context, request mcp.CallToolRequest) (*mcp.CallToolResult, error)) {
		wrapped := s.wrapToolHandler(tool.Name, handler)
		s.MCPServer.AddTool(tool, wrapped)
		s.toolHandlers[tool.Name] = wrapped
	}

	// search_workspace is the unified discovery tool for semantic, lexical, and graph-driven lookups.
	addTool(mcp.NewTool("search_workspace",
		mcp.WithDescription("Unified search engine for deep codebase exploration. Use this for semantic search (vector), exact text/regex matching (ripgrep), following code relationship graphs (calls/imports), or checking indexing progress."),
		mcp.WithString("action", mcp.Description("The type of search: 'vector' (semantic similarity), 'regex' (exact text/pattern match), 'graph' (follow relationships), or 'index_status' (check background progress).")),
		mcp.WithString("query", mcp.Description("The search query, symbol name, or regex pattern.")),
		mcp.WithNumber("limit", mcp.Description("Max number of results to return (default 10).")),
		mcp.WithString("path", mcp.Description("Optional file or directory path to restrict the search scope.")),
	), s.handleSearchWorkspace)

	// workspace_manager centralizes workspace lifecycle operations and indexing control.
	addTool(mcp.NewTool("workspace_manager",
		mcp.WithDescription("Core project management tools. Use this to switch active project roots, trigger specialized indexing runs, or retrieve detailed system diagnostics and state reports."),
		mcp.WithString("action", mcp.Description("Management action: 'set_project_root' (update active workspace), 'trigger_index' (start re-indexing), or 'get_indexing_diagnostics' (detailed health/state report).")),
		mcp.WithString("path", mcp.Description("The absolute path to the project root or a specific directory to act upon.")),
	), s.handleWorkspaceManager)

	// lsp_query exposes high-precision LSP capabilities (definition, refs, hierarchy, impact).
	addTool(mcp.NewTool("lsp_query",
		mcp.WithDescription("High-precision symbol analysis via the Language Server Protocol (LSP). Use this for jumping to definitions, finding all references across the workspace, exploring large type hierarchies, or analyzing the impact of cross-file changes."),
		mcp.WithString("action", mcp.Description("LSP capability: 'definition' (find symbol source), 'references' (find all usages), 'type_hierarchy' (supertypes/subtypes), or 'impact_analysis' (downstream dependencies).")),
		mcp.WithString("path", mcp.Description("Absolute path to the file containing the symbol.")),
		mcp.WithNumber("line", mcp.Description("0-indexed line number of the symbol.")),
		mcp.WithNumber("character", mcp.Description("0-indexed character offset of the symbol.")),
	), s.handleLspQuery)

	// analyze_code provides structural and quality diagnostics over indexed project content.
	addTool(mcp.NewTool("analyze_code",
		mcp.WithDescription("Advanced codebase diagnostic suite. Use this to generate AST-based structural skeletons, detect dead (unused) exported symbols, find semantically duplicated code blocks, or validate dependency health against manifest files."),
		mcp.WithString("action", mcp.Description("Analysis type: 'ast_skeleton' (structural map), 'dead_code' (find unused exports), 'duplicate_code' (semantic clones), or 'dependencies' (validate package.json/go.mod imports).")),
		mcp.WithString("path", mcp.Description("Subdirectory or file path to analyze.")),
	), s.handleAnalyzeCode)

	// modify_workspace handles guarded workspace mutations and post-change validation workflows.
	addTool(mcp.NewTool("modify_workspace",
		mcp.WithDescription("Safe and structured codebase mutation tools. Use this for applying small search-and-replace patches, creating new files with content, or running formatters/linters (like go fmt) to ensure code quality."),
		mcp.WithString("action", mcp.Description("Mutation action: 'apply_patch' (search-and-replace), 'create_file' (new file), 'run_linter' (format code), 'verify_patch' (dry-run/check integrity), or 'auto_fix' (LSP-driven fixes).")),
		mcp.WithString("path", mcp.Description("Target file path for the mutation.")),
		mcp.WithString("content", mcp.Description("Complete file content or patch context.")),
		mcp.WithString("search", mcp.Description("Exact text block to find and replace.")),
		mcp.WithString("replace", mcp.Description("New text block to insert.")),
		mcp.WithString("tool", mcp.Description("Linter or formatter tool name (e.g., 'go fmt').")),
	), s.handleModifyWorkspace)

	// index_status returns quick indexing progress for polling loops and readiness checks.
	addTool(mcp.NewTool("index_status", mcp.WithDescription("Check current indexing status and background progress.")), s.handleIndexStatus)
	// trigger_project_index starts full or scoped re-indexing operations.
	addTool(mcp.NewTool("trigger_project_index",
		mcp.WithDescription("Manually trigger a full re-index of the project."),
		mcp.WithString("project_path", mcp.Description("Absolute path to the project root.")),
	), s.handleTriggerProjectIndex)
	// get_related_context returns nearest semantic neighbors for a target file.
	addTool(mcp.NewTool("get_related_context",
		mcp.WithDescription("Retrieve semantically related code and symbols for a specific file."),
		mcp.WithString("filePath", mcp.Description("Path to the source file.")),
	), s.handleGetRelatedContext)

	// store_context persists deterministic project knowledge for future retrieval.
	addTool(mcp.NewTool("store_context",
		mcp.WithDescription("Store general project rules or architectural decisions."),
		mcp.WithString("text", mcp.Description("The text context to store.")),
	), s.handleStoreContext)
	// delete_context removes targeted context entries or clears project memory entirely.
	addTool(mcp.NewTool("delete_context",
		mcp.WithDescription("Delete specific shared memory context, or completely wipe a project's vector index."),
		mcp.WithString("target_path", mcp.Description("The exact file path, context ID, or 'ALL' to clear the whole project.")),
	), s.handleDeleteContext)
	// distill_package_purpose summarizes package intent and key entities.
	addTool(mcp.NewTool("distill_package_purpose",
		mcp.WithDescription("Generates a high-level semantic summary of a package's primary purpose and key entities."),
		mcp.WithString("path", mcp.Description("Relative or absolute path of the package directory to distill.")),
	), s.handleDistillPackagePurpose)
	// trace_data_flow follows symbol usage patterns through project-level references.
	addTool(mcp.NewTool("trace_data_flow",
		mcp.WithDescription("Traces the usage of a specific field or symbol across the project."),
		mcp.WithString("field_name", mcp.Description("The name of the field or symbol to trace")),
	), s.handleTraceDataFlow)
}

func (s *Server) wrapToolHandler(name string, handler func(ctx context.Context, request mcp.CallToolRequest) (*mcp.CallToolResult, error)) func(context.Context, mcp.CallToolRequest) (*mcp.CallToolResult, error) {
	return func(ctx context.Context, request mcp.CallToolRequest) (*mcp.CallToolResult, error) {
		spanName := "mcp.tool." + name
		if args, ok := request.Params.Arguments.(map[string]any); ok {
			if action, ok := args["action"].(string); ok && action != "" {
				spanName += "." + action
			}
		}

		ctx, span := tracing.StartSpan(ctx, "mcp.server", spanName)
		defer span.End()

		result, err := handler(ctx, request)
		if err != nil && metrics.ErrorsTotal != nil {
			metrics.ErrorsTotal.Inc()
		}
		return result, err
	}
}

// SendNotification sends a logging message notification to all connected clients.
func (s *Server) SendNotification(level mcp.LoggingLevel, data any, logger string) {
	params := map[string]any{
		"level": level,
		"data":  data,
	}
	if logger != "" {
		params["logger"] = logger
	}
	s.MCPServer.SendNotificationToAllClients("notifications/message", params)
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
	handler, ok := s.toolHandlers[name]
	if !ok {
		return nil, fmt.Errorf("tool not found: %s", name)
	}

	// Create a CallToolRequest from the args
	req := mcp.CallToolRequest{}
	req.Params.Name = name
	req.Params.Arguments = args

	return handler(ctx, req)
}

// ListTools returns a list of all tools registered with the MCP server.
func (s *Server) ListTools() []mcp.Tool {
	var tools []mcp.Tool
	for _, t := range s.MCPServer.ListTools() {
		tools = append(tools, t.Tool)
	}
	return tools
}

// GetEmbedder returns the embedding engine used by the server.
func (s *Server) GetEmbedder() indexer.Embedder {
	return s.embedder
}
