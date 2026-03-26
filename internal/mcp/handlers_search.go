package mcp

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strings"

	"github.com/mark3labs/mcp-go/mcp"
	"github.com/nilesh32236/vector-mcp-go/internal/db"
	"github.com/nilesh32236/vector-mcp-go/internal/indexer"
)

// handleFilesystemGrep performs a keyword or regex search across the project's files.
func (s *Server) handleFilesystemGrep(ctx context.Context, request mcp.CallToolRequest) (*mcp.CallToolResult, error) {
	query := request.GetString("query", "")
	if query == "" {
		return mcp.NewToolResultError("query is required"), nil
	}
	includePattern := request.GetString("include_pattern", "")

	isRegex := false
	if args, ok := request.Params.Arguments.(map[string]interface{}); ok {
		isRegex, _ = args["is_regex"].(bool)
	}

	var re *regexp.Regexp
	if isRegex {
		var err error
		re, err = regexp.Compile(query)
		if err != nil {
			return mcp.NewToolResultError(fmt.Sprintf("Invalid regex: %v", err)), nil
		}
	}

	var results []string
	maxMatches := 100
	matchCount := 0

	err := filepath.WalkDir(s.cfg.ProjectRoot, func(path string, d os.DirEntry, err error) error {
		if err != nil || d.IsDir() {
			return nil
		}
		if matchCount >= maxMatches {
			return filepath.SkipDir
		}

		relPath, _ := filepath.Rel(s.cfg.ProjectRoot, path)
		if indexer.IsIgnoredDir(filepath.Base(filepath.Dir(path))) || indexer.IsIgnoredFile(d.Name()) {
			return nil
		}

		if includePattern != "" {
			matched, _ := filepath.Match(includePattern, d.Name())
			if !matched {
				return nil
			}
		}

		content, err := os.ReadFile(path)
		if err != nil {
			return nil
		}

		lines := strings.Split(string(content), "\n")
		for i, line := range lines {
			matched := false
			if isRegex {
				matched = re.MatchString(line)
			} else {
				matched = strings.Contains(strings.ToLower(line), strings.ToLower(query))
			}

			if matched {
				results = append(results, fmt.Sprintf("%s:%d: %s", relPath, i+1, strings.TrimSpace(line)))
				matchCount++
				if matchCount >= maxMatches {
					break
				}
			}
		}

		return nil
	})

	if err != nil {
		return mcp.NewToolResultError(fmt.Sprintf("Error during grep: %v", err)), nil
	}

	if len(results) == 0 {
		return mcp.NewToolResultText("No matches found."), nil
	}

	var out strings.Builder
	out.WriteString(fmt.Sprintf("### Grep Results for '%s' (%d matches):\n\n", query, len(results)))
	for _, res := range results {
		out.WriteString(fmt.Sprintf("%s\n", res))
	}

	if matchCount >= maxMatches {
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
	topK := int(request.GetFloat("topK", 10))
	category := request.GetString("category", "") // code, document, or empty
	pathFilter := request.GetString("path_filter", "")
	maxTokens := int(request.GetFloat("max_tokens", float64(indexer.MaxContextTokens)))
	if maxTokens <= 0 {
		maxTokens = indexer.MaxContextTokens
	}

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
	out.WriteString(fmt.Sprintf("### Search Results for '%s':\n\n", query))
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

		out.WriteString(fmt.Sprintf("#### Result %d: %s%s\n", i+1, r.Metadata["path"], lineRange))
		if cat := r.Metadata["category"]; cat != "" {
			out.WriteString(fmt.Sprintf("- **Category**: %s\n", cat))
		}
		if syms := r.Metadata["symbols"]; syms != "" {
			out.WriteString(fmt.Sprintf("- **Entities**: %s\n", syms))
		}
		out.WriteString(fmt.Sprintf("```\n%s\n```\n\n", r.Content))
		currentTokenCount += tokens
	}

	return mcp.NewToolResultText(out.String()), nil
}
