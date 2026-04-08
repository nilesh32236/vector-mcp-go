package mcp

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
	"sync"
	"time"

	"github.com/mark3labs/mcp-go/mcp"
	"github.com/nilesh32236/vector-mcp-go/internal/db"
	"github.com/nilesh32236/vector-mcp-go/internal/indexer"
	"github.com/nilesh32236/vector-mcp-go/internal/util"
)

// handleFilesystemGrep performs a keyword or regex search across the project's files.
func (s *Server) handleFilesystemGrep(ctx context.Context, request mcp.CallToolRequest) (*mcp.CallToolResult, error) {
	query := request.GetString("query", "")
	if query == "" {
		return mcp.NewToolResultError("query is required"), nil
	}
	includePattern := request.GetString("include_pattern", "")

	isRegex := false
	if args, ok := request.Params.Arguments.(map[string]any); ok {
		isRegex, _ = args["is_regex"].(bool)
	}

	var re *regexp.Regexp
	var lowerQuery string
	if isRegex {
		var err error
		re, err = regexp.Compile(query)
		if err != nil {
			return mcp.NewToolResultError(fmt.Sprintf("Invalid regex: %v", err)), nil
		}
	} else {
		lowerQuery = strings.ToLower(query)
	}

	maxMatches := 100
	type Match struct {
		Path    string
		Line    int
		Content string
	}
	matchChan := make(chan Match, maxMatches)
	errChan := make(chan error, 1)
	doneChan := make(chan struct{})

	// Context with timeout to prevent hanging
	ctx, cancel := context.WithTimeout(ctx, 30*time.Second)
	defer cancel()

	// File discovery and worker pool
	paths := make(chan string, 100)
	var wg sync.WaitGroup
	numWorkers := 8

	lowerQuery := strings.ToLower(query)

	for i := 0; i < numWorkers; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for path := range paths {
				select {
				case <-ctx.Done():
					return
				default:
					content, err := os.ReadFile(path)
					if err != nil {
						continue
					}

					relPath, _ := filepath.Rel(s.cfg.ProjectRoot, path)
					lines := strings.Split(string(content), "\n")
					for i, line := range lines {
						matched := false
						if isRegex {
							matched = re.MatchString(line)
						} else {
							matched = strings.Contains(strings.ToLower(line), lowerQuery)
						}

						if matched {
							select {
							case matchChan <- Match{Path: relPath, Line: i + 1, Content: strings.TrimSpace(line)}:
							case <-ctx.Done():
								return
							default:
								return // matches limit reach implicitly by channel capacity if we were careful, but we handle it below
							}
						}
					}
				}
			}
		}()
	}

	go func() {
		defer close(paths)
		err := filepath.WalkDir(s.cfg.ProjectRoot, func(path string, d os.DirEntry, err error) error {
			if err != nil {
				return nil
			}
			if d.IsDir() {
				if indexer.IsIgnoredDir(d.Name()) {
					return filepath.SkipDir
				}
				return nil
			}

			if indexer.IsIgnoredFile(d.Name()) {
				return nil
			}

			if includePattern != "" {
				matched, _ := filepath.Match(includePattern, d.Name())
				if !matched {
					return nil
				}
			}

			select {
			case paths <- path:
			case <-ctx.Done():
				return filepath.SkipDir
			}
			return nil
		})
		if err != nil {
			errChan <- err
		}
	}()

	go func() {
		wg.Wait()
		close(doneChan)
	}()

	var results []Match
	limitReached := false

collect:
	for {
		select {
		case match := <-matchChan:
			results = append(results, match)
			if len(results) >= maxMatches {
				limitReached = true
				cancel() // Stop workers
				break collect
			}
		case <-doneChan:
			break collect
		case err := <-errChan:
			return mcp.NewToolResultError(fmt.Sprintf("Error during grep: %v", err)), nil
		case <-ctx.Done():
			if ctx.Err() == context.DeadlineExceeded {
				return mcp.NewToolResultError("Grep timed out"), nil
			}
			break collect
		}
	}

	if len(results) == 0 {
		return mcp.NewToolResultText("No matches found."), nil
	}

	// Sort results by path and line
	sort.Slice(results, func(i, j int) bool {
		if results[i].Path != results[j].Path {
			return results[i].Path < results[j].Path
		}
		return results[i].Line < results[j].Line
	})

	var out strings.Builder
	fmt.Fprintf(&out, "### Grep Results for '%s' (%d matches):\n\n", query, len(results))
	for _, res := range results {
		fmt.Fprintf(&out, "%s:%d: %s\n", res.Path, res.Line, res.Content)
	}

	if limitReached {
		out.WriteString("\n... (limit reached, more matches may exist)")
	}

	return mcp.NewToolResultText(out.String()), nil
}

// handleSearchCodebase provides an advanced search interface across the entire codebase.
func (s *Server) handleSearchCodebase(ctx context.Context, request mcp.CallToolRequest) (*mcp.CallToolResult, error) {
	query := request.GetString("query", "")
	if query == "" {
		return mcp.NewToolResultError("query is required"), nil
	}
	topK := util.ClampInt(int(request.GetFloat("topK", 10)), 1, 100)
	category := request.GetString("category", "") // code, document, or empty
	pathFilter := request.GetString("path_filter", "")
	maxTokensFloat := request.GetFloat("max_tokens", float64(indexer.MaxContextTokens))
	if maxTokensFloat <= 0 {
		maxTokensFloat = float64(indexer.MaxContextTokens)
	}
	maxTokens := util.ClampInt(int(maxTokensFloat), 1, indexer.MaxContextTokens)

	pids := request.GetStringSlice("cross_reference_projects", nil)
	if len(pids) == 0 {
		pids = []string{s.cfg.ProjectRoot}
	}

	emb, err := s.embedder.Embed(ctx, query)
	if err != nil {
		return mcp.NewToolResultError(fmt.Sprintf("Failed to embed query: %v", err)), nil
	}

	store, err := s.getStore(ctx)
	if err != nil {
		return mcp.NewToolResultError(fmt.Sprintf("Failed to get store: %v", err)), nil
	}

	// For hybrid search, we fetch more to allow filtering and reranking
	results, err := store.HybridSearch(ctx, query, emb, topK*5, pids, category)
	if err != nil {
		return mcp.NewToolResultError(fmt.Sprintf("Failed to search database: %v", err)), nil
	}

	var filtered []db.Record
	for _, r := range results {
		// 1. Path Filter
		if pathFilter != "" && !strings.Contains(r.Metadata["path"], pathFilter) {
			continue
		}
		filtered = append(filtered, r)
	}

	// 2. Rerank results if cross-encoder is available
	if len(filtered) > 1 {
		var texts []string
		for _, r := range filtered {
			texts = append(texts, r.Content)
		}

		scores, err := s.embedder.RerankBatch(ctx, query, texts)
		if err == nil && len(scores) == len(filtered) {
			// Sort filtered by rerank scores
			type ScoredRecord struct {
				Record db.Record
				Score  float32
			}
			var ranked []ScoredRecord
			for i, r := range filtered {
				ranked = append(ranked, ScoredRecord{Record: r, Score: scores[i]})
			}
			sort.Slice(ranked, func(i, j int) bool {
				return ranked[i].Score > ranked[j].Score
			})

			// Take topK
			filtered = nil
			for i := 0; i < len(ranked) && i < topK; i++ {
				filtered = append(filtered, ranked[i].Record)
			}
		} else {
			// Fallback to topK if reranking fails
			if len(filtered) > topK {
				filtered = filtered[:topK]
			}
		}
	} else if len(filtered) > topK {
		filtered = filtered[:topK]
	}

	if len(filtered) == 0 {
		return mcp.NewToolResultText("No matches found matching the criteria."), nil
	}

	var out strings.Builder
	fmt.Fprintf(&out, "### Search Results for '%s':\n\n", query)
	currentTokenCount := 0

	for i, r := range filtered {
		tokens := indexer.EstimateTokens(r.Content)
		if currentTokenCount+tokens > maxTokens {
			out.WriteString("... (truncating further results to stay within context window)")
			break
		}

		lineRange := ""
		if start, ok := r.Metadata["start_line"]; ok {
			if end, ok := r.Metadata["end_line"]; ok {
				lineRange = fmt.Sprintf(" (Lines %s-%s)", start, end)
			}
		}

		fmt.Fprintf(&out, "#### Result %d: %s%s\n", i+1, r.Metadata["path"], lineRange)
		if cat := r.Metadata["category"]; cat != "" {
			fmt.Fprintf(&out, "- **Category**: %s\n", cat)
		}
		if syms := r.Metadata["symbols"]; syms != "" {
			fmt.Fprintf(&out, "- **Entities**: %s\n", syms)
		}
		fmt.Fprintf(&out, "```\n%s\n```\n\n", r.Content)
		currentTokenCount += tokens
	}

	resultText := out.String()
	truncated := util.TruncateRuneSafe(resultText, 12000)
	if truncated != resultText {
		truncated += "\n... [Truncated for length]"
	}

	return mcp.NewToolResultText(truncated), nil
}

// handleSearchWorkspace unifies vector, regex, graph, and index status tools into a single "Fat Tool".
func (s *Server) handleSearchWorkspace(ctx context.Context, request mcp.CallToolRequest) (*mcp.CallToolResult, error) {
	action := request.GetString("action", "")
	query := request.GetString("query", "")
	limitFloat := request.GetFloat("limit", 10)
	pathFilter := request.GetString("path", "")

	if limitFloat <= 0 {
		limitFloat = 10
	}
	limit := util.ClampInt(int(limitFloat), 1, 100)

	switch action {
	case "vector":
		// Route to vector search logic
		return s.handleSearchCodebase(ctx, mcp.CallToolRequest{
			Params: mcp.CallToolParams{
				Arguments: map[string]any{
					"query":       query,
					"topK":        limit,
					"path_filter": pathFilter,
				},
			},
		})
	case "regex":
		// Route to exact match/regex logic
		return s.handleFilesystemGrep(ctx, mcp.CallToolRequest{
			Params: mcp.CallToolParams{
				Arguments: map[string]any{
					"query":           query,
					"is_regex":        true, // Default to true if they use search_workspace
					"include_pattern": pathFilter,
				},
			},
		})
	case "graph":
		// Route to graph queries (e.g. interface implementations)
		return s.handleGetInterfaceImplementations(ctx, mcp.CallToolRequest{
			Params: mcp.CallToolParams{
				Arguments: map[string]any{
					"interface_name": query,
				},
			},
		})
	case "index_status":
		// Route to index status
		return s.handleIndexStatus(ctx, mcp.CallToolRequest{})
	default:
		return mcp.NewToolResultError(fmt.Sprintf("Invalid action: %s. Must be 'vector', 'regex', 'graph', or 'index_status'", action)), nil
	}
}
