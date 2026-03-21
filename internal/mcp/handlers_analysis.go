package mcp

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
	"time"

	"github.com/mark3labs/mcp-go/mcp"
	"github.com/nilesh32236/vector-mcp-go/internal/config"
	"github.com/nilesh32236/vector-mcp-go/internal/db"
	"github.com/nilesh32236/vector-mcp-go/internal/indexer"
)

// handleGetRelatedContext retrieves relevant code chunks and dependencies for a given file.
// It also explores usage samples for symbols found in the target file.
func (s *Server) handleGetRelatedContext(ctx context.Context, request mcp.CallToolRequest) (*mcp.CallToolResult, error) {
	ctx, cancel := context.WithTimeout(ctx, 30*time.Second)
	defer cancel()
	filePath := request.GetString("filePath", "")
	maxTokens := int(request.GetFloat("max_tokens", float64(indexer.MaxContextTokens)))
	if maxTokens <= 0 {
		maxTokens = indexer.MaxContextTokens
	}

	store, err := s.getStore(ctx)
	if err != nil {
		return mcp.NewToolResultError(err.Error()), nil
	}
	pids := []string{s.cfg.ProjectRoot}
	crossProjs := request.GetStringSlice("cross_reference_projects", nil)
	if len(crossProjs) > 0 {
		pids = append(pids, crossProjs...)
	}
	records, err := store.GetByPath(ctx, filePath, s.cfg.ProjectRoot)
	if err != nil || len(records) == 0 {
		return mcp.NewToolResultText(fmt.Sprintf("No context found for file: %s", filePath)), nil
	}
	uniqueDeps := make(map[string]string)
	allImportStrings := make(map[string]bool)
	allSymbols := make(map[string]bool)
	for _, r := range records {
		var deps []string
		if relStr := r.Metadata["relationships"]; relStr != "" {
			if err := json.Unmarshal([]byte(relStr), &deps); err == nil {
				for _, d := range deps {
					allImportStrings[d] = true
					if strings.HasPrefix(d, "./") || strings.HasPrefix(d, "../") {
						uniqueDeps[d] = filepath.Join(filepath.Dir(filePath), d)
					} else if physPath, ok := s.monorepoResolver.Resolve(d); ok {
						uniqueDeps[d] = physPath
					}
				}
			}
		}
		var symbols []string
		if symStr := r.Metadata["symbols"]; symStr != "" {
			if err := json.Unmarshal([]byte(symStr), &symbols); err == nil {
				for _, s := range symbols {
					allSymbols[s] = true
				}
			}
		}
	}
	var out strings.Builder
	out.WriteString("<context>\n")
	currentTokenCount := 0
	var omittedFiles []string
	out.WriteString(fmt.Sprintf("  <file path=\"%s\">\n    <metadata>\n", filePath))
	var depList []string
	for d := range allImportStrings {
		depList = append(depList, d)
	}
	depListJSON, _ := json.Marshal(depList)
	out.WriteString(fmt.Sprintf("      <dependencies>%s</dependencies>\n", string(depListJSON)))
	var symList []string
	for s := range allSymbols {
		symList = append(symList, s)
	}
	symListJSON, _ := json.Marshal(symList)
	out.WriteString(fmt.Sprintf("      <symbols>%s</symbols>\n", string(symListJSON)))
	out.WriteString("    </metadata>\n")
	for _, r := range records {
		tokens := indexer.EstimateTokens(r.Content)
		if currentTokenCount+tokens > maxTokens {
			omittedFiles = append(omittedFiles, filePath)
			continue
		}
		out.WriteString("    <code_chunk>\n" + r.Content + "\n    </code_chunk>\n")
		currentTokenCount += tokens
	}
	out.WriteString("  </file>\n")
	if len(uniqueDeps) > 0 {
		allRecords, _ := store.GetAllRecords(ctx)
		for importPath, physPath := range uniqueDeps {
			matchPath := strings.TrimSuffix(physPath, filepath.Ext(physPath))
			out.WriteString(fmt.Sprintf("  <file path=\"%s\" resolved_from=\"%s\">\n", physPath, importPath))
			foundAny := false
			fileDeps := make(map[string]bool)
			fileSymbols := make(map[string]bool)
			var fileChunks []db.Record
			for _, dr := range allRecords {
				projMatch := false
				for _, pid := range pids {
					if dr.Metadata["project_id"] == pid {
						projMatch = true
						break
					}
				}
				if projMatch && (dr.Metadata["path"] == physPath || strings.Contains(dr.Metadata["path"], matchPath)) {
					fileChunks = append(fileChunks, dr)
					var dps []string
					if err := json.Unmarshal([]byte(dr.Metadata["relationships"]), &dps); err == nil {
						for _, d := range dps {
							fileDeps[d] = true
						}
					}
					var sys []string
					if err := json.Unmarshal([]byte(dr.Metadata["symbols"]), &sys); err == nil {
						for _, s := range sys {
							fileSymbols[s] = true
						}
					}
				}
			}
			if len(fileChunks) > 0 {
				out.WriteString("    <metadata>\n")
				var fdList []string
				for d := range fileDeps {
					fdList = append(fdList, d)
				}
				fdJSON, _ := json.Marshal(fdList)
				out.WriteString(fmt.Sprintf("      <dependencies>%s</dependencies>\n", string(fdJSON)))
				var fsList []string
				for s := range fileSymbols {
					fsList = append(fsList, s)
				}
				fsJSON, _ := json.Marshal(fsList)
				out.WriteString(fmt.Sprintf("      <symbols>%s</symbols>\n", string(fsJSON)))
				out.WriteString("    </metadata>\n")
				for _, dr := range fileChunks {
					tokens := indexer.EstimateTokens(dr.Content)
					if currentTokenCount+tokens > maxTokens {
						omittedFiles = append(omittedFiles, dr.Metadata["path"])
						continue
					}
					out.WriteString("    <code_chunk>\n" + dr.Content + "\n    </code_chunk>\n")
					currentTokenCount += tokens
					foundAny = true
				}
			}
			if !foundAny && currentTokenCount < maxTokens {
				out.WriteString("    <error>No indexed chunks found.</error>\n")
			}
			out.WriteString("  </file>\n")
		}
	}
	if len(omittedFiles) > 0 {
		out.WriteString("  <omitted_matches>\n    <files>")
		omittedJSON, _ := json.Marshal(omittedFiles)
		out.WriteString(string(omittedJSON) + "</files>\n  </omitted_matches>\n")
	}

	// Usage Samples: Optimized cross-file symbol search
	if len(allSymbols) > 0 {
		out.WriteString("  <usage_samples>\n")
		foundUsage := false
		for s := range allSymbols {
			// Find usage of symbol 's' across projects
			usages, err := store.LexicalSearch(ctx, s, 5, pids, "")
			if err != nil {
				continue
			}

			for _, dr := range usages {
				if dr.Metadata["path"] == filePath {
					continue
				}
				
				tokens := indexer.EstimateTokens(dr.Content)
				if currentTokenCount+tokens > maxTokens {
					continue
				}

				out.WriteString(fmt.Sprintf("    <sample symbol=\"%s\" used_in=\"%s\">\n", s, dr.Metadata["path"]))
				out.WriteString(dr.Content + "\n")
				out.WriteString("    </sample>\n")
				currentTokenCount += tokens
				foundUsage = true
			}
		}
		if !foundUsage {
			out.WriteString("    <info>No external usage samples found.</info>\n")
		}
		out.WriteString("  </usage_samples>\n")
	}

	out.WriteString("</context>")
	return mcp.NewToolResultText(out.String()), nil
}

// handleFindDuplicateCode scans for semantically similar code across the codebase.
func (s *Server) handleFindDuplicateCode(ctx context.Context, request mcp.CallToolRequest) (*mcp.CallToolResult, error) {
	ctx, cancel := context.WithTimeout(ctx, 30*time.Second)
	defer cancel()
	targetPath := request.GetString("target_path", "")
	pids := []string{s.cfg.ProjectRoot}
	crossProjs := request.GetStringSlice("cross_reference_projects", nil)
	if len(crossProjs) > 0 {
		pids = append(pids, crossProjs...)
	}
	store, _ := s.getStore(ctx)
	allRecords, _ := store.GetAllRecords(ctx)
	var targetChunks []db.Record
	for _, r := range allRecords {
		if r.Metadata["project_id"] == s.cfg.ProjectRoot && strings.HasPrefix(r.Metadata["path"], targetPath) {
			targetChunks = append(targetChunks, r)
		}
	}
	var out strings.Builder
	out.WriteString(fmt.Sprintf("<duplicate_analysis target=\"%s\">\n", targetPath))
	found := false
	for _, tc := range targetChunks {
		emb, _ := s.embedder.Embed(ctx, tc.Content)

		var matches []db.Record
		if ds, ok := store.(*db.Store); ok {
			ms, _, _ := ds.SearchWithScore(ctx, emb, 5, pids, "")
			matches = ms
		} else {
			ms, _ := store.Search(ctx, emb, 5, pids, "")
			matches = ms
		}

		for _, m := range matches {
			if m.Metadata["path"] != tc.Metadata["path"] || m.Metadata["project_id"] != tc.Metadata["project_id"] {
				out.WriteString(fmt.Sprintf("  <finding>\n    <original file=\"%s\">%s</original>\n", tc.Metadata["path"], tc.Metadata["path"]))
				out.WriteString(fmt.Sprintf("    <match file=\"%s\" project=\"%s\">%s</match>\n  </finding>\n", m.Metadata["path"], m.Metadata["project_id"], m.Metadata["path"]))
				found = true
			}
		}
	}
	if !found {
		out.WriteString("  <summary>No duplicates found.</summary>\n")
	}
	out.WriteString("</duplicate_analysis>")
	return mcp.NewToolResultText(out.String()), nil
}

// handleGetCodebaseSkeleton returns a tree-like representation of the project's directory structure.
func (s *Server) handleGetCodebaseSkeleton(ctx context.Context, request mcp.CallToolRequest) (*mcp.CallToolResult, error) {
	ctx, cancel := context.WithTimeout(ctx, 30*time.Second)
	defer cancel()
	targetPath := request.GetString("target_path", s.cfg.ProjectRoot)
	if targetPath == "" {
		targetPath = s.cfg.ProjectRoot
	}
	if !filepath.IsAbs(targetPath) {
		targetPath = filepath.Join(s.cfg.ProjectRoot, targetPath)
	}
	maxDepth := int(request.GetFloat("max_depth", 3))
	includePattern := request.GetString("include_pattern", "")
	excludePattern := request.GetString("exclude_pattern", "")
	maxItems := int(request.GetFloat("max_items", 1000))

	info, err := os.Stat(targetPath)
	if err != nil {
		return mcp.NewToolResultError(fmt.Sprintf("Invalid target_path: %v", err)), nil
	}
	if !info.IsDir() {
		targetPath = filepath.Dir(targetPath)
	}
	var out strings.Builder
	out.WriteString(fmt.Sprintf("Directory Tree: %s (Depth Limit: %d)\n", targetPath, maxDepth))
	itemCount := 0
	truncated := false
	err = filepath.WalkDir(targetPath, func(path string, d os.DirEntry, err error) error {
		if err != nil {
			return nil
		}
		if path == targetPath {
			return nil
		}
		relPath, err := filepath.Rel(targetPath, path)
		if err != nil {
			return nil
		}
		depth := strings.Count(relPath, string(os.PathSeparator)) + 1
		if d.IsDir() {
			if indexer.IsIgnoredDir(d.Name()) {
				return filepath.SkipDir
			}
			if depth > maxDepth {
				return filepath.SkipDir
			}
		} else {
			if indexer.IsIgnoredFile(d.Name()) {
				return nil
			}
			if depth > maxDepth {
				return nil
			}

			// Pattern filtering
			if includePattern != "" {
				matched, _ := filepath.Match(includePattern, d.Name())
				if !matched {
					return nil
				}
			}
			if excludePattern != "" {
				matched, _ := filepath.Match(excludePattern, d.Name())
				if matched {
					return nil
				}
			}
		}
		if itemCount >= maxItems {
			truncated = true
			return filepath.SkipDir
		}
		itemCount++
		indent := strings.Repeat("│   ", depth-1)
		out.WriteString(fmt.Sprintf("%s├── %s\n", indent, d.Name()))
		return nil
	})
	if err != nil {
		return mcp.NewToolResultError(fmt.Sprintf("Error walking directory: %v", err)), nil
	}
	if truncated {
		out.WriteString(fmt.Sprintf("... (tree truncated, reached %d item limit)\n", maxItems))
	}
	return mcp.NewToolResultText(fmt.Sprintf("<codebase_skeleton>\n%s</codebase_skeleton>", out.String())), nil
}

// handleCheckDependencyHealth analyzes imports in the codebase and identifies missing manifest declarations.
func (s *Server) handleCheckDependencyHealth(ctx context.Context, request mcp.CallToolRequest) (*mcp.CallToolResult, error) {
	dirPath := request.GetString("directory_path", "")
	if dirPath == "" {
		return mcp.NewToolResultError("directory_path is required"), nil
	}

	absPath, err := filepath.Abs(dirPath)
	if err != nil {
		return mcp.NewToolResultError(fmt.Sprintf("Invalid path: %v", err)), nil
	}

	depSet := make(map[string]bool)
	projectType := "unknown"

	// 1. Detect project type and load dependencies
	if _, err := os.Stat(filepath.Join(absPath, "package.json")); err == nil {
		projectType = "npm"
		pkgData, _ := os.ReadFile(filepath.Join(absPath, "package.json"))
		var pkg struct {
			Dependencies    map[string]string `json:"dependencies"`
			DevDependencies map[string]string `json:"devDependencies"`
		}
		if err := json.Unmarshal(pkgData, &pkg); err == nil {
			for d := range pkg.Dependencies {
				depSet[d] = true
			}
			for d := range pkg.DevDependencies {
				depSet[d] = true
			}
		}
	} else if _, err := os.Stat(filepath.Join(absPath, "go.mod")); err == nil {
		projectType = "go"
		modData, _ := os.ReadFile(filepath.Join(absPath, "go.mod"))
		lines := strings.Split(string(modData), "\n")
		// Very simple go.mod parser
		for _, line := range lines {
			line = strings.TrimSpace(line)
			if strings.HasPrefix(line, "require ") {
				parts := strings.Fields(line)
				if len(parts) >= 2 {
					depSet[parts[1]] = true
				}
			}
		}
	} else if _, err := os.Stat(filepath.Join(absPath, "requirements.txt")); err == nil {
		projectType = "python"
		reqData, _ := os.ReadFile(filepath.Join(absPath, "requirements.txt"))
		lines := strings.Split(string(reqData), "\n")
		for _, line := range lines {
			line = strings.TrimSpace(line)
			if line != "" && !strings.HasPrefix(line, "#") {
				// extract pkg name before == or >=
				pkgName := regexp.MustCompile(`^([a-zA-Z0-9_\-]+)`).FindString(line)
				if pkgName != "" {
					depSet[pkgName] = true
				}
			}
		}
	}

	if projectType == "unknown" {
		return mcp.NewToolResultError("Could not identify project type (no package.json, go.mod, or requirements.txt found)"), nil
	}

	// 2. Fetch Chunks
	store, err := s.getStore(ctx)
	if err != nil {
		return mcp.NewToolResultError(err.Error()), nil
	}

	relDirPath := config.GetRelativePath(absPath, s.cfg.ProjectRoot)
	records, err := store.GetByPrefix(ctx, relDirPath, s.cfg.ProjectRoot)
	if err != nil {
		return mcp.NewToolResultError(fmt.Sprintf("Failed to fetch records: %v", err)), nil
	}

	// 3. Analyze Relationships
	missingDeps := make(map[string][]string) // dep -> files

	for _, r := range records {
		var rels []string
		if relStr := r.Metadata["relationships"]; relStr != "" {
			if err := json.Unmarshal([]byte(relStr), &rels); err == nil {
				for _, dep := range rels {
					if projectType == "npm" {
						// Skip local imports and monorepo prefix
						if strings.HasPrefix(dep, ".") || strings.HasPrefix(dep, "/") || strings.HasPrefix(dep, "@herexa/") {
							continue
						}
						pkgName := dep
						parts := strings.Split(dep, "/")
						if strings.HasPrefix(dep, "@") && len(parts) > 1 {
							pkgName = parts[0] + "/" + parts[1]
						} else {
							pkgName = parts[0]
						}
						if !depSet[pkgName] {
							missingDeps[pkgName] = append(missingDeps[pkgName], r.Metadata["path"])
						}
					} else if projectType == "go" {
						// Standard library check (simplified: no dots usually)
						if !strings.Contains(dep, ".") || strings.HasPrefix(dep, s.cfg.ProjectRoot) {
							continue
						}
						if !depSet[dep] {
							missingDeps[dep] = append(missingDeps[dep], r.Metadata["path"])
						}
					} else if projectType == "python" {
						if strings.HasPrefix(dep, ".") {
							continue
						}
						if !depSet[dep] {
							missingDeps[dep] = append(missingDeps[dep], r.Metadata["path"])
						}
					}
				}
			}
		}
	}

	// 4. Output Report
	if len(missingDeps) == 0 {
		return mcp.NewToolResultText(fmt.Sprintf("✅ Dependency Health Check (%s): All external imports are correctly declared.", projectType)), nil
	}

	var out strings.Builder
	out.WriteString(fmt.Sprintf("## ⚠️ Dependency Health Report (%s)\n\n", projectType))
	out.WriteString("The following external dependencies are imported but missing from your manifest:\n\n")

	var deps []string
	for d := range missingDeps {
		deps = append(deps, d)
	}
	sort.Strings(deps)

	for _, dep := range deps {
		files := missingDeps[dep]
		uniqueFiles := make(map[string]bool)
		for _, f := range files {
			uniqueFiles[f] = true
		}
		var sortedFiles []string
		for f := range uniqueFiles {
			sortedFiles = append(sortedFiles, f)
		}
		sort.Strings(sortedFiles)

		out.WriteString(fmt.Sprintf("### `%s`\n", dep))
		out.WriteString("Imported in:\n")
		for _, f := range sortedFiles {
			out.WriteString(fmt.Sprintf("- %s\n", f))
		}
		out.WriteString("\n")
	}

	return mcp.NewToolResultText(out.String()), nil
}

// handleGenerateDocstringPrompt creates a rich LLM prompt to assist in generating documentation for a code entity.
func (s *Server) handleGenerateDocstringPrompt(ctx context.Context, request mcp.CallToolRequest) (*mcp.CallToolResult, error) {
	filePath := request.GetString("file_path", "")
	entityName := request.GetString("entity_name", "")
	language := request.GetString("language", "")

	if filePath == "" || entityName == "" {
		return mcp.NewToolResultError("file_path and entity_name are required"), nil
	}

	if language == "" {
		ext := strings.ToLower(filepath.Ext(filePath))
		switch ext {
		case ".go":
			language = "Go"
		case ".ts", ".js", ".tsx", ".jsx":
			language = "TypeScript/JavaScript"
		case ".py":
			language = "Python"
		}
	}

	docStyle := "professional documentation comment"
	switch strings.ToLower(language) {
	case "go":
		docStyle = "Godoc comments"
	case "typescript/javascript", "typescript", "javascript", "ts", "js":
		docStyle = "JSDoc comments"
	case "python":
		docStyle = "Python docstrings (PEP 257 format)"
	}

	store, err := s.getStore(ctx)
	if err != nil {
		return mcp.NewToolResultError(err.Error()), nil
	}

	// Use LexicalSearch to find the entity in the file
	records, err := store.GetByPath(ctx, filePath, s.cfg.ProjectRoot)
	if err != nil {
		return mcp.NewToolResultError(fmt.Sprintf("Failed to fetch records for file: %v", err)), nil
	}

	var match *db.Record
	for _, r := range records {
		var syms []string
		if symStr := r.Metadata["symbols"]; symStr != "" {
			if err := json.Unmarshal([]byte(symStr), &syms); err == nil {
				for _, s := range syms {
					if s == entityName {
						match = &r
						break
					}
				}
			}
		}
		if match != nil {
			break
		}
	}

	if match == nil {
		return mcp.NewToolResultError(fmt.Sprintf("Entity '%s' not found in file '%s'", entityName, filePath)), nil
	}

	// Construct Prompt
	content := match.Content
	calls := match.Metadata["calls"]
	symbols := match.Metadata["symbols"]
	relationships := match.Metadata["relationships"]

	prompt := fmt.Sprintf(`Please write a professional %s for the following code.
Architecture Context:
- Entity: %s
- Internal Calls made: %s
- File Imports: %s

Code:
%s`, docStyle, symbols, calls, relationships, content)

	return mcp.NewToolResultText(prompt), nil
}

// handleAnalyzeArchitecture generates a Mermaid graph of package dependencies.
func (s *Server) handleAnalyzeArchitecture(ctx context.Context, request mcp.CallToolRequest) (*mcp.CallToolResult, error) {
	monorepoPrefix := request.GetString("monorepo_prefix", "@herexa/")
	store, err := s.getStore(ctx)
	if err != nil {
		return mcp.NewToolResultError(err.Error()), nil
	}

	records, err := store.GetAllRecords(ctx)
	if err != nil {
		return mcp.NewToolResultError(fmt.Sprintf("Failed to fetch all records: %v", err)), nil
	}

	// adjacency list: source_package -> target_package -> exists
	adj := make(map[string]map[string]bool)

	for _, r := range records {
		path := r.Metadata["path"]
		if path == "" {
			continue
		}

		// Source package detection (e.g., apps/api/src/main.ts -> apps/api)
		parts := strings.Split(path, string(os.PathSeparator))
		if len(parts) < 2 {
			continue
		}
		srcPkg := parts[0]
		if len(parts) > 2 && (parts[0] == "apps" || parts[0] == "packages") {
			srcPkg = parts[0] + "/" + parts[1]
		}

		// Relationships
		var rels []string
		if relStr := r.Metadata["relationships"]; relStr != "" {
			if err := json.Unmarshal([]byte(relStr), &rels); err == nil {
				for _, rel := range rels {
					if strings.HasPrefix(rel, monorepoPrefix) {
						targetPkg := rel
						if adj[srcPkg] == nil {
							adj[srcPkg] = make(map[string]bool)
						}
						adj[srcPkg][targetPkg] = true
					}
				}
			}
		}
	}

	// Build Mermaid graph
	var sb strings.Builder
	sb.WriteString("graph TD\n")

	// Sort for deterministic output
	var sources []string
	for s := range adj {
		sources = append(sources, s)
	}
	sort.Strings(sources)

	for _, src := range sources {
		var targets []string
		for t := range adj[src] {
			targets = append(targets, t)
		}
		sort.Strings(targets)
		for _, target := range targets {
			sb.WriteString(fmt.Sprintf("    \"%s\" --> \"%s\"\n", src, target))
		}
	}

	return mcp.NewToolResultText(sb.String()), nil
}

// handleFindDeadCode identifies potentially unused exported code entities.
func (s *Server) handleFindDeadCode(ctx context.Context, request mcp.CallToolRequest) (*mcp.CallToolResult, error) {
	// Default excluded paths for common entry points and routing
	defaultExcludes := []string{"/api", "/routes", "/cmd", "main.go", "index.ts"}
	
	// If the user didn't provide exclude_paths, use defaults.
	// Note: We check if the key exists to distinguish between "not provided" and "provided as empty".
	var excludePaths []string
	if _, ok := request.GetArguments()["exclude_paths"]; ok {
		excludePaths = request.GetStringSlice("exclude_paths", nil)
	} else {
		excludePaths = defaultExcludes
	}

	isLibrary := request.GetBool("is_library", false)

	store, err := s.getStore(ctx)
	if err != nil {
		return mcp.NewToolResultError(err.Error()), nil
	}

	records, err := store.GetAllRecords(ctx)
	if err != nil {
		return mcp.NewToolResultError(fmt.Sprintf("Failed to fetch all records: %v", err)), nil
	}

	type exportedSymbol struct {
		name string
		path string
	}
	setA := make(map[string]exportedSymbol) // name -> info
	setB := make(map[string]bool)           // name -> used

	for _, r := range records {
		filePath := r.Metadata["path"]

		// Skip test files
		if strings.HasSuffix(filePath, "_test.go") || strings.Contains(filePath, "/test/") || strings.Contains(filePath, "/tests/") {
			continue
		}

		isExcluded := false
		if !isLibrary {
			for _, ep := range excludePaths {
				if strings.Contains(filePath, ep) {
					isExcluded = true
					break
				}
			}
		}

		// If library, only consider internal/ or explicitly private
		if isLibrary {
			isInternal := strings.Contains(filePath, "internal/") || strings.Contains(filePath, "private/") || strings.HasPrefix(filepath.Base(filePath), "_")
			if !isInternal {
				isExcluded = true
			}
		}

		// Set A: Exports (structural types only)
		t := r.Metadata["type"]
		if !isExcluded && (t == "function" || t == "class" || t == "variable" || t == "arrow_function") {
			var syms []string
			if err := json.Unmarshal([]byte(r.Metadata["symbols"]), &syms); err == nil {
				for _, sym := range syms {
					if sym != "" {
						// Skip common entry points and test functions
						if sym == "main" || strings.HasPrefix(sym, "Test") || strings.HasPrefix(sym, "Benchmark") || strings.HasPrefix(sym, "Example") {
							continue
						}
						// Whitelist NewServer as it's the package constructor
						if sym == "NewServer" {
							continue
						}
						setA[sym] = exportedSymbol{name: sym, path: filePath}
					}
				}
			}
		}

		// Set B: Usage
		// 1. Calls
		var calls []string
		if err := json.Unmarshal([]byte(r.Metadata["calls"]), &calls); err == nil {
			for _, call := range calls {
				setB[call] = true
			}
		}
		// 2. Relationships (Imports)
		var rels []string
		if err := json.Unmarshal([]byte(r.Metadata["relationships"]), &rels); err == nil {
			for _, rel := range rels {
				setB[rel] = true
			}
		}
	}

	// Set Difference
	var dead []exportedSymbol
	for name, info := range setA {
		// If the symbol is a method (contains a dot like Server.handleChat)
		// check if the method name itself is used
		baseName := name
		if dotIdx := strings.Index(name, "."); dotIdx != -1 {
			baseName = name[dotIdx+1:]
		}

		if !setB[name] && !setB[baseName] {
			dead = append(dead, info)
		}
	}

	if len(dead) == 0 {
		return mcp.NewToolResultText("✅ Dead Code Check: No unused exported symbols found."), nil
	}

	// Sort for deterministic output
	sort.Slice(dead, func(i, j int) bool {
		if dead[i].path == dead[j].path {
			return dead[i].name < dead[j].name
		}
		return dead[i].path < dead[j].path
	})

	var out strings.Builder
	out.WriteString("## 🔎 Potential Dead Code Report\n\n")
	out.WriteString("The following exported symbols are not explicitly used (imported or called) in the indexed codebase:\n\n")
	for _, d := range dead {
		out.WriteString(fmt.Sprintf("- **`%s`** in `%s`\n", d.name, d.path))
	}

	return mcp.NewToolResultText(out.String()), nil
}
