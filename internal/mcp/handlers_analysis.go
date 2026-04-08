package mcp

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"sort"
	"strings"
	"sync"
	"time"

	"github.com/mark3labs/mcp-go/mcp"
	"github.com/nilesh32236/vector-mcp-go/internal/db"
	"github.com/nilesh32236/vector-mcp-go/internal/indexer"
	"github.com/nilesh32236/vector-mcp-go/internal/util"
)

// handleGetRelatedContext retrieves relevant code chunks and dependencies for a given file.
// It also explores usage samples for symbols found in the target file.
const (
	ProjectTypeNPM    = "npm"
	ProjectTypePython = "python"
)

func (s *Server) handleGetRelatedContext(ctx context.Context, request mcp.CallToolRequest) (*mcp.CallToolResult, error) {
	ctx, cancel := context.WithTimeout(ctx, 30*time.Second)
	defer cancel()
	filePath := request.GetString("filePath", "")
	maxTokensFloat := request.GetFloat("max_tokens", float64(indexer.MaxContextTokens))
	if maxTokensFloat <= 0 {
		maxTokensFloat = float64(indexer.MaxContextTokens)
	}
	maxTokens := util.ClampInt(int(maxTokensFloat), 1, indexer.MaxContextTokens)

	store, err := s.getStore(ctx)
	if err != nil {
		return mcp.NewToolResultError(err.Error()), nil
	}
	pids := []string{s.projectRoot()}
	crossProjs := request.GetStringSlice("cross_reference_projects", nil)
	if len(crossProjs) > 0 {
		pids = append(pids, crossProjs...)
	}
	records, err := store.GetByPath(ctx, filePath, s.projectRoot())
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
					} else if physPath, ok := s.monorepoResolve(d); ok {
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
	fmt.Fprintf(&out, "  <file path=\"%s\">\n    <metadata>\n", filePath)
	var depList []string
	for d := range allImportStrings {
		depList = append(depList, d)
	}
	depListJSON, _ := json.Marshal(depList)
	fmt.Fprintf(&out, "      <dependencies>%s</dependencies>\n", string(depListJSON))
	var symList []string
	for s := range allSymbols {
		symList = append(symList, s)
	}
	symListJSON, _ := json.Marshal(symList)
	fmt.Fprintf(&out, "      <symbols>%s</symbols>\n", string(symListJSON))
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
		// Optimization: Group records by path to avoid O(N*M) search
		pathMap := make(map[string][]db.Record)
		for _, dr := range allRecords {
			p := dr.Metadata["path"]
			pathMap[p] = append(pathMap[p], dr)
		}

		for importPath, physPath := range uniqueDeps {
			matchPath := strings.TrimSuffix(physPath, filepath.Ext(physPath))
			fmt.Fprintf(&out, "  <file path=\"%s\" resolved_from=\"%s\">\n", physPath, importPath)
			foundAny := false
			fileDeps := make(map[string]bool)
			fileSymbols := make(map[string]bool)

			// Try exact match first, then fallback to matchPath (for files without extensions in imports)
			fileChunks := pathMap[physPath]
			if len(fileChunks) == 0 {
				// Fallback to searching all keys for matchPath - still faster than full records scan
				for p, chunks := range pathMap {
					if strings.Contains(p, matchPath) {
						fileChunks = append(fileChunks, chunks...)
					}
				}
			}

			for _, dr := range fileChunks {
				projMatch := false
				for _, pid := range pids {
					if dr.Metadata["project_id"] == pid {
						projMatch = true
						break
					}
				}
				if projMatch {
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
				fmt.Fprintf(&out, "      <dependencies>%s</dependencies>\n", string(fdJSON))
				var fsList []string
				for s := range fileSymbols {
					fsList = append(fsList, s)
				}
				fsJSON, _ := json.Marshal(fsList)
				fmt.Fprintf(&out, "      <symbols>%s</symbols>\n", string(fsJSON))
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

				fmt.Fprintf(&out, "    <sample symbol=\"%s\" used_in=\"%s\">\n", s, dr.Metadata["path"])
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
	pids := []string{s.projectRoot()}
	crossProjs := request.GetStringSlice("cross_reference_projects", nil)
	if len(crossProjs) > 0 {
		pids = append(pids, crossProjs...)
	}
	store, _ := s.getStore(ctx)

	targetChunks, _ := store.GetByPrefix(ctx, targetPath, s.projectRoot())

	var out strings.Builder
	fmt.Fprintf(&out, "<duplicate_analysis target=\"%s\">\n", targetPath)

	// Optimization: Parallelize searches for each chunk
	type finding struct {
		originalFile string
		matchFile    string
		matchProject string
		content      string
	}
	findingsChan := make(chan finding, len(targetChunks)*5)
	var wg sync.WaitGroup
	semaphore := make(chan struct{}, 10) // Limit concurrency to 10 parallel searches

	for _, tc := range targetChunks {
		wg.Add(1)
		go func(chunk db.Record) {
			defer wg.Done()
			semaphore <- struct{}{}
			defer func() { <-semaphore }()

			emb := chunk.Embedding
			if len(emb) == 0 {
				e, err := s.embedder.Embed(ctx, chunk.Content)
				if err != nil {
					return
				}
				emb = e
			}

			var matches []db.Record
			if ds, ok := store.(*db.Store); ok {
				ms, _, _ := ds.SearchWithScore(ctx, emb, 5, pids, "")
				matches = ms
			} else {
				ms, _ := store.Search(ctx, emb, 5, pids, "")
				matches = ms
			}

			for _, m := range matches {
				if m.Metadata["path"] != chunk.Metadata["path"] || m.Metadata["project_id"] != chunk.Metadata["project_id"] {
					findingsChan <- finding{
						originalFile: chunk.Metadata["path"],
						matchFile:    m.Metadata["path"],
						matchProject: m.Metadata["project_id"],
						content:      m.Content,
					}
				}
			}
		}(tc)
	}

	go func() {
		wg.Wait()
		close(findingsChan)
	}()

	found := false
	for f := range findingsChan {
		found = true
		fmt.Fprintf(&out, "  <finding>\n    <original file=\"%s\">%s</original>\n", f.originalFile, f.originalFile)
		fmt.Fprintf(&out, "    <match file=\"%s\" project=\"%s\">\n", f.matchFile, f.matchProject)
		fmt.Fprintf(&out, "```\n%s\n```\n", f.content)
		out.WriteString("    </match>\n  </finding>\n")
	}

	if !found {
		out.WriteString("  <info>No significant duplicates found.</info>\n")
	}
	out.WriteString("</duplicate_analysis>")
	return mcp.NewToolResultText(out.String()), nil
}

// handleCheckDependencyHealth analyzes a directory's package.json against its indexed imports.
func (s *Server) handleCheckDependencyHealth(ctx context.Context, request mcp.CallToolRequest) (*mcp.CallToolResult, error) {
	dirPath := request.GetString("directory_path", ".")
	absPath, err := s.validatePath(dirPath)
	if err != nil {
		return mcp.NewToolResultError(fmt.Sprintf("invalid directory_path: %v", err)), nil
	}

	// 1. Resolve workspace root for manifest detection
	workspaceRoot, err := util.FindWorkspaceRoot(absPath)
	if err != nil {
		s.logger.Warn("Failed to find workspace root for dependency check, falling back to directory", "path", absPath, "error", err)
		workspaceRoot = absPath
	}

	// 2. Detect project type and find manifest
	projectType := "unknown"
	manifestPath := ""
	if _, err := os.Stat(filepath.Join(workspaceRoot, "package.json")); err == nil {
		projectType = ProjectTypeNPM
		manifestPath = filepath.Join(workspaceRoot, "package.json")
	} else if _, err := os.Stat(filepath.Join(workspaceRoot, "go.mod")); err == nil {
		projectType = "go"
		manifestPath = filepath.Join(workspaceRoot, "go.mod")
	} else if _, err := os.Stat(filepath.Join(workspaceRoot, "requirements.txt")); err == nil {
		projectType = ProjectTypePython
		manifestPath = filepath.Join(workspaceRoot, "requirements.txt")
	}

	if manifestPath == "" {
		return mcp.NewToolResultError("No supported manifest found (package.json, go.mod, requirements.txt) in " + dirPath), nil
	}

	// 2. Parse Manifest
	depSet := make(map[string]bool)
	content, _ := os.ReadFile(manifestPath)
	if projectType == ProjectTypeNPM {
		var pkg struct {
			Deps    map[string]string `json:"dependencies"`
			DevDeps map[string]string `json:"devDependencies"`
		}
		_ = json.Unmarshal(content, &pkg)
		for d := range pkg.Deps {
			depSet[d] = true
		}
		for d := range pkg.DevDeps {
			depSet[d] = true
		}
	} else {
		// Basic line-based parsing for go.mod and requirements.txt
		lines := strings.Split(string(content), "\n")
		for _, line := range lines {
			line = strings.TrimSpace(line)
			if line == "" || strings.HasPrefix(line, "//") || strings.HasPrefix(line, "#") {
				continue
			}
			parts := strings.Fields(line)
			if projectType == "go" && len(parts) >= 2 {
				depSet[parts[0]] = true
			} else if projectType == ProjectTypePython {
				depSet[strings.Split(line, "==")[0]] = true
			}
		}
	}

	// Extract the Go module path for same-module import detection.
	goModulePath := ""
	if projectType == "go" {
		for _, line := range strings.Split(string(content), "\n") {
			if parts := strings.Fields(strings.TrimSpace(line)); len(parts) == 2 && parts[0] == "module" {
				goModulePath = parts[1]
				break
			}
		}
	}

	// 3. Find all imports in indexed chunks for this directory
	store, err := s.getStore(ctx)
	if err != nil {
		return mcp.NewToolResultError(err.Error()), nil
	}

	relDirPath, _ := filepath.Rel(s.projectRoot(), absPath)
	if relDirPath == "." {
		relDirPath = ""
	}

	records, err := store.GetByPrefix(ctx, relDirPath, s.projectRoot())
	if err != nil {
		return mcp.NewToolResultError(fmt.Sprintf("Failed to fetch records: %v", err)), nil
	}

	missingDeps := make(map[string][]string) // dep -> files
	for _, r := range records {
		var rels []string
		if relStr := r.Metadata["relationships"]; relStr != "" {
			if err := json.Unmarshal([]byte(relStr), &rels); err == nil {
				for _, dep := range rels {
					if projectType == ProjectTypeNPM {
						// Skip local imports and monorepo prefix
						if strings.HasPrefix(dep, ".") || strings.HasPrefix(dep, "/") || strings.HasPrefix(dep, "@herexa/") {
							continue
						}
						var pkgName string
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
						// Skip stdlib (no dots) and same-module imports.
						if !strings.Contains(dep, ".") || (goModulePath != "" && strings.HasPrefix(dep, goModulePath)) {
							continue
						}
						if !depSet[dep] {
							missingDeps[dep] = append(missingDeps[dep], r.Metadata["path"])
						}
					} else if projectType == ProjectTypePython {
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
	fmt.Fprintf(&out, "## ⚠️ Dependency Health Report (%s)\n\n", projectType)
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

		fmt.Fprintf(&out, "### `%s`\n", dep)
		out.WriteString("Imported in:\n")
		for _, f := range sortedFiles {
			fmt.Fprintf(&out, "- %s\n", f)
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
	case ProjectTypePython:
		docStyle = "Python docstrings (PEP 257 format)"
	}

	store, err := s.getStore(ctx)
	if err != nil {
		return mcp.NewToolResultError(err.Error()), nil
	}

	// Use LexicalSearch to find the entity in the file
	records, err := store.GetByPath(ctx, filePath, s.projectRoot())
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

	var records []db.Record
	if ds, ok := store.(*db.Store); ok {
		records, err = ds.GetAllMetadata(ctx)
	} else {
		records, err = store.GetAllRecords(ctx)
	}
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
			fmt.Fprintf(&sb, "    \"%s\" --> \"%s\"\n", src, target)
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

	targetPath := request.GetString("target_path", "")
	isLibrary := request.GetBool("is_library", false)

	store, err := s.getStore(ctx)
	if err != nil {
		return mcp.NewToolResultError(err.Error()), nil
	}

	var records []db.Record
	if ds, ok := store.(*db.Store); ok {
		records, err = ds.GetAllMetadata(ctx)
	} else {
		records, err = store.GetAllRecords(ctx)
	}
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
		if targetPath != "" && !strings.HasPrefix(filePath, targetPath) {
			continue
		}

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
		fmt.Fprintf(&out, "- **`%s`** in `%s`\n", d.name, d.path)
	}

	return mcp.NewToolResultText(out.String()), nil
}

// handleVerifyImplementationGap cross-references feedback/documentation against the codebase.
func (s *Server) handleVerifyImplementationGap(ctx context.Context, request mcp.CallToolRequest) (*mcp.CallToolResult, error) {
	ctx, cancel := context.WithTimeout(ctx, 45*time.Second)
	defer cancel()

	query := request.GetString("query", "")
	if query == "" {
		return mcp.NewToolResultError("query is required (e.g., 'user authentication' or 'feature requirement')"), nil
	}

	store, err := s.getStore(ctx)
	if err != nil {
		return mcp.NewToolResultError(err.Error()), nil
	}

	pids := []string{s.projectRoot()}
	emb, err := s.embedder.Embed(ctx, query)
	if err != nil {
		return mcp.NewToolResultError(fmt.Sprintf("Failed to embed query: %v", err)), nil
	}

	// 1. Search for Requirements/Feedback (category: document)
	docs, err := store.HybridSearch(ctx, query, emb, 5, pids, "document")
	if err != nil {
		return mcp.NewToolResultError(fmt.Sprintf("Failed to search documents: %v", err)), nil
	}

	// 2. Search for Implementation (category: code)
	code, err := store.HybridSearch(ctx, query, emb, 10, pids, "code")
	if err != nil {
		return mcp.NewToolResultError(fmt.Sprintf("Failed to search code: %v", err)), nil
	}

	var out strings.Builder
	fmt.Fprintf(&out, "# Verification Analysis for: '%s'\n\n", query)

	out.WriteString("## 📄 Found Requirements / Feedback\n")
	if len(docs) == 0 {
		out.WriteString("No matching documentation or feedback found.\n")
	} else {
		for i, d := range docs {
			fmt.Fprintf(&out, "### Req %d: %s\n", i+1, d.Metadata["path"])
			out.WriteString(d.Content + "\n\n")
		}
	}

	out.WriteString("\n## 💻 Potential Implementation (Code)\n")
	if len(code) == 0 {
		out.WriteString("No matching implementation found in the codebase.\n")
	} else {
		for i, c := range code {
			fmt.Fprintf(&out, "### Code %d: %s (Lines %s-%s)\n", i+1, c.Metadata["path"], c.Metadata["start_line"], c.Metadata["end_line"])
			out.WriteString("```\n" + c.Content + "\n```\n\n")
		}
	}

	out.WriteString("\n## 🔍 GAP Analysis Guidance\n")
	out.WriteString("Compare the 'Found Requirements' with the 'Potential Implementation'. Look for:\n")
	out.WriteString("- Unhandled edge cases mentioned in feedback.\n")
	out.WriteString("- Missing validation logic required by documentation.\n")
	out.WriteString("- Architectural mismatches between design and implementation.\n")

	return mcp.NewToolResultText(out.String()), nil
}

// handleFindMissingTests identifies exported symbols that lack corresponding test coverage.
func (s *Server) handleFindMissingTests(ctx context.Context, _ mcp.CallToolRequest) (*mcp.CallToolResult, error) {
	ctx, cancel := context.WithTimeout(ctx, 60*time.Second)
	defer cancel()

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

	sourceExports := make(map[string]exportedSymbol)
	testUsages := make(map[string]bool)

	for _, r := range records {
		filePath := r.Metadata["path"]
		isTestFile := strings.HasSuffix(filePath, "_test.go") || strings.Contains(filePath, "/test/") || strings.Contains(filePath, "/tests/")

		if isTestFile {
			// Track usages in test files
			var calls []string
			if err := json.Unmarshal([]byte(r.Metadata["calls"]), &calls); err == nil {
				for _, call := range calls {
					testUsages[call] = true
				}
			}
			var rels []string
			if err := json.Unmarshal([]byte(r.Metadata["relationships"]), &rels); err == nil {
				for _, rel := range rels {
					testUsages[rel] = true
				}
			}
			// Also track the content for explicit mentions
			for _, word := range strings.Fields(r.Content) {
				testUsages[word] = true
			}
		} else {
			// Track exports in source files
			t := r.Metadata["type"]
			if t == "function" || t == "class" || t == "variable" || t == "arrow_function" {
				var syms []string
				if err := json.Unmarshal([]byte(r.Metadata["symbols"]), &syms); err == nil {
					for _, sym := range syms {
						if sym != "" && !strings.HasPrefix(sym, "_") { // Only exported/public
							sourceExports[sym] = exportedSymbol{name: sym, path: filePath}
						}
					}
				}
			}
		}
	}

	var missing []exportedSymbol
	for name, info := range sourceExports {
		// Heuristic: check if the symbol name or any part of it (if it's a method) is in testUsages
		found := testUsages[name]
		if !found && strings.Contains(name, ".") {
			parts := strings.Split(name, ".")
			found = testUsages[parts[len(parts)-1]]
		}

		if !found {
			missing = append(missing, info)
		}
	}

	if len(missing) == 0 {
		return mcp.NewToolResultText("✅ Coverage Check: All exported symbols appear to have some test representation."), nil
	}

	sort.Slice(missing, func(i, j int) bool {
		if missing[i].path == missing[j].path {
			return missing[i].name < missing[j].name
		}
		return missing[i].path < missing[j].path
	})

	var out strings.Builder
	out.WriteString("## 🧪 Missing Test Coverage Report\n\n")
	out.WriteString("The following exported symbols were found in source files but were not detected in any test files:\n\n")
	for _, m := range missing {
		fmt.Fprintf(&out, "- **`%s`** in `%s`\n", m.name, m.path)
	}

	return mcp.NewToolResultText(out.String()), nil
}

// handleListAPIEndpoints identifies potential API route definitions in the codebase.
func (s *Server) handleListAPIEndpoints(ctx context.Context, _ mcp.CallToolRequest) (*mcp.CallToolResult, error) {
	ctx, cancel := context.WithTimeout(ctx, 30*time.Second)
	defer cancel()

	store, err := s.getStore(ctx)
	if err != nil {
		return mcp.NewToolResultError(err.Error()), nil
	}

	// We look for common routing keywords
	keywords := []string{"HandleFunc", "mux.Handle", "app.GET", "app.POST", "router.Register", "Route(", "@app.route", "FastAPI()"}

	var allMatches []db.Record
	for _, kw := range keywords {
		matches, _ := store.LexicalSearch(ctx, kw, 20, []string{s.projectRoot()}, "code")
		allMatches = append(allMatches, matches...)
	}

	if len(allMatches) == 0 {
		return mcp.NewToolResultText("No API routing patterns detected."), nil
	}

	// Deduplicate by content/path
	uniqueMatches := make(map[string]db.Record)
	for _, m := range allMatches {
		key := m.Metadata["path"] + ":" + m.Metadata["start_line"]
		uniqueMatches[key] = m
	}

	var out strings.Builder
	out.WriteString("## 🌐 Detected API Endpoints / Routes\n\n")

	paths := make([]string, 0, len(uniqueMatches))
	for k := range uniqueMatches {
		paths = append(paths, k)
	}
	sort.Strings(paths)

	for _, p := range paths {
		m := uniqueMatches[p]
		fmt.Fprintf(&out, "### %s (Line %s)\n", m.Metadata["path"], m.Metadata["start_line"])
		out.WriteString("```\n" + strings.TrimSpace(m.Content) + "\n```\n\n")
	}

	return mcp.NewToolResultText(out.String()), nil
}

// handleGetCodeHistory retrieves recent git history for a specific file.
func (s *Server) handleGetCodeHistory(ctx context.Context, request mcp.CallToolRequest) (*mcp.CallToolResult, error) {
	filePath := request.GetString("file_path", "")
	if filePath == "" {
		return mcp.NewToolResultError("file_path is required"), nil
	}

	absPath, err := s.validatePath(filePath)
	if err != nil {
		return mcp.NewToolResultError(fmt.Sprintf("invalid file_path: %v", err)), nil
	}

	// Check if git is available and it's a git repo
	cmd := exec.CommandContext(ctx, "git", "log", "-n", "10", "--pretty=format:%h - %an, %ar : %s", "--", absPath)
	cmd.Dir = s.projectRoot()
	output, err := cmd.CombinedOutput()
	if err != nil {
		return mcp.NewToolResultError(fmt.Sprintf("Failed to run git log: %v\nOutput: %s", err, string(output))), nil
	}

	if len(output) == 0 {
		return mcp.NewToolResultText(fmt.Sprintf("No git history found for %s (or file is not tracked).", filePath)), nil
	}

	var out strings.Builder
	fmt.Fprintf(&out, "## 📜 Git History for %s\n\n", filePath)
	out.WriteString(string(output))

	return mcp.NewToolResultText(out.String()), nil
}

// handleGetSummarizedContext retrieves context for a query and returns chunks programmatically.
func (s *Server) handleGetSummarizedContext(ctx context.Context, request mcp.CallToolRequest) (*mcp.CallToolResult, error) {
	ctx, cancel := context.WithTimeout(ctx, 45*time.Second)
	defer cancel()

	query := request.GetString("query", "")
	if query == "" {
		return mcp.NewToolResultError("query is required"), nil
	}
	topK := util.ClampInt(int(request.GetFloat("topK", 5)), 1, 100)

	store, err := s.getStore(ctx)
	if err != nil {
		return mcp.NewToolResultError(err.Error()), nil
	}

	emb, err := s.embedder.Embed(ctx, query)
	if err != nil {
		return mcp.NewToolResultError(fmt.Sprintf("Failed to embed query: %v", err)), nil
	}

	records, err := store.HybridSearch(ctx, query, emb, topK, []string{s.projectRoot()}, "")
	if err != nil {
		return mcp.NewToolResultError(fmt.Sprintf("Failed to search database: %v", err)), nil
	}

	if len(records) == 0 {
		return mcp.NewToolResultText("No matching context found to summarize."), nil
	}

	var combinedText strings.Builder
	fmt.Fprintf(&combinedText, "### 📚 Retrieved Context for: '%s'\n\n", query)
	for _, r := range records {
		fmt.Fprintf(&combinedText, "**File**: %s\n**Content**:\n%s\n---\n", r.Metadata["path"], r.Content)
	}

	return mcp.NewToolResultText(combinedText.String()), nil
}

// handleVerifyProposedChange checks a proposed code change against stored Knowledge Items programmatically.
func (s *Server) handleVerifyProposedChange(ctx context.Context, request mcp.CallToolRequest) (*mcp.CallToolResult, error) {
	ctx, cancel := context.WithTimeout(ctx, 60*time.Second)
	defer cancel()

	proposedChange := request.GetString("proposed_change", "")
	if proposedChange == "" {
		return mcp.NewToolResultError("proposed_change is required"), nil
	}

	pids := []string{s.projectRoot()}
	crossProjs := request.GetStringSlice("cross_reference_projects", nil)
	if len(crossProjs) > 0 {
		pids = append(pids, crossProjs...)
	}

	store, err := s.getStore(ctx)
	if err != nil {
		return mcp.NewToolResultError(err.Error()), nil
	}

	emb, err := s.embedder.Embed(ctx, proposedChange)
	if err != nil {
		return mcp.NewToolResultError(fmt.Sprintf("Failed to embed proposed change: %v", err)), nil
	}

	docRecords, err := store.HybridSearch(ctx, proposedChange, emb, 10, pids, "document")
	if err != nil {
		return mcp.NewToolResultError(fmt.Sprintf("Failed to search documents: %v", err)), nil
	}

	codeRecords, err := store.HybridSearch(ctx, proposedChange, emb, 5, pids, "code")
	if err != nil {
		s.logger.Warn("Failed to fetch code examples for verification", "error", err)
	}

	if len(docRecords) == 0 && len(codeRecords) == 0 {
		return mcp.NewToolResultText("### 🛡️ Verification Result\n\nNo specific Knowledge Items or Architectural Decisions were found that directly relate to this change.\n\n**Recommendation**: Proceed with standard code review. If this is a new pattern, consider documenting it using `store_context`."), nil
	}

	var out strings.Builder
	out.WriteString("### 🛡️ Verification Result (Manual Review Required)\n\n")
	out.WriteString("Please manually check your change against these identified rules:\n\n")

	for _, r := range docRecords {
		fmt.Fprintf(&out, "#### Rule/Decision (from %s):\n%s\n---\n", r.Metadata["path"], r.Content)
	}

	if len(codeRecords) > 0 {
		out.WriteString("\n### Similar Existing Implementation Patterns:\n")
		for _, r := range codeRecords {
			fmt.Fprintf(&out, "#### File: %s\n%s\n---\n", r.Metadata["path"], r.Content)
		}
	}

	return mcp.NewToolResultText(out.String()), nil
}

// handleDistillKnowledge analyzes a directory or file and programmatically extracts content.
func (s *Server) handleDistillKnowledge(ctx context.Context, request mcp.CallToolRequest) (*mcp.CallToolResult, error) {
	ctx, cancel := context.WithTimeout(ctx, 30*time.Second)
	defer cancel()

	path := request.GetString("path", "")
	if path == "" {
		return mcp.NewToolResultError("path is required"), nil
	}

	projectRoot := s.projectRoot()

	store, err := s.getStore(ctx)
	if err != nil {
		return mcp.NewToolResultError(fmt.Errorf("failed to get store: %w", err).Error()), nil
	}

	records, err := store.Search(ctx, make([]float32, 768), 50, []string{projectRoot}, "code")
	if err != nil {
		return mcp.NewToolResultError(fmt.Errorf("failed to retrieve records: %w", err).Error()), nil
	}

	var relevantContent strings.Builder
	count := 0

	for _, r := range records {
		relPath := r.Metadata["path"]

		if strings.HasPrefix(relPath, path) {
			fmt.Fprintf(&relevantContent, "File: %s\nContent:\n%s\n---\n", relPath, r.Content)
			count++
		}
	}

	if count == 0 {
		return mcp.NewToolResultError(fmt.Sprintf("No indexed content found for path: %s. Ensure the project is indexed.", path)), nil
	}

	contentStr := relevantContent.String()
	truncatedContent := util.TruncateRuneSafe(contentStr, 10000)
	toolText := truncatedContent
	if truncatedContent != contentStr {
		toolText = truncatedContent + "\n... [Truncated for length]"
	}

	storeErr := s.storeContext(ctx, truncatedContent, projectRoot)
	if storeErr != nil {
		s.logger.Error("Failed to store distilled context", "error", storeErr)
		return mcp.NewToolResultText(fmt.Sprintf("### 🧠 Distilled Knowledge (Storage Failed)\n\n%s\n\n**Warning**: Failed to index this KI automatically.", toolText)), nil
	}

	return mcp.NewToolResultText(fmt.Sprintf("### ✅ Knowledge Distilled & Indexed\n\n%s", toolText)), nil
}

// storeContext is a helper to save KIs to the database
func (s *Server) storeContext(ctx context.Context, text string, projectID string) error {
	store, err := s.getStore(ctx)
	if err != nil {
		return err
	}

	emb, err := s.embedder.Embed(ctx, text)
	if err != nil {
		return err
	}

	docID := fmt.Sprintf("ki_%d", time.Now().UnixNano())
	record := db.Record{
		ID:        docID,
		Content:   text,
		Embedding: emb,
		Metadata: map[string]string{
			"type":       "document",
			"project_id": projectID,
			"path":       "distilled_knowledge",
			"created_at": time.Now().Format(time.RFC3339),
		},
	}

	return store.Insert(ctx, []db.Record{record})
}

// handleAnalyzeCode unifies codebase analysis tasks into a single "Fat Tool".
func (s *Server) handleAnalyzeCode(ctx context.Context, request mcp.CallToolRequest) (*mcp.CallToolResult, error) {
	action := request.GetString("action", "")
	path := request.GetString("path", "")

	switch action {
	case "ast_skeleton":
		// Route to topological mapping/skeleton logic
		return s.handleGetCodebaseSkeleton(ctx, mcp.CallToolRequest{
			Params: mcp.CallToolParams{
				Arguments: map[string]any{
					"target_path": path,
				},
			},
		})
	case "dependencies":
		// Route to dependency check
		return s.handleCheckDependencyHealth(ctx, mcp.CallToolRequest{
			Params: mcp.CallToolParams{
				Arguments: map[string]any{
					"directory_path": path,
				},
			},
		})
	case "duplicate_code":
		// Route to duplication check
		return s.handleFindDuplicateCode(ctx, mcp.CallToolRequest{
			Params: mcp.CallToolParams{
				Arguments: map[string]any{
					"target_path": path,
				},
			},
		})
	case "dead_code":
		// Route to dead code check
		return s.handleFindDeadCode(ctx, mcp.CallToolRequest{
			Params: mcp.CallToolParams{
				Arguments: map[string]any{
					"target_path": path,
				},
			},
		})
	default:
		return mcp.NewToolResultError(fmt.Sprintf("Invalid action: %s. Must be 'ast_skeleton', 'dependencies', 'duplicate_code', or 'dead_code'", action)), nil
	}
}
