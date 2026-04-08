// Package indexer provides tools for chunking code and documentation and preparing them for indexing.
package indexer

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
)

// WorkspaceResolver handles path mapping for monorepos and TypeScript path aliases.
type WorkspaceResolver struct {
	ProjectRoot string
	PathAliases map[string]string
	Workspaces  map[string]string
}

// InitResolver initializes a new WorkspaceResolver for the given project root.
func InitResolver(projectRoot string) *WorkspaceResolver {
	r := &WorkspaceResolver{
		ProjectRoot: projectRoot,
		PathAliases: make(map[string]string),
		Workspaces:  make(map[string]string),
	}

	r.parseTsConfig()
	r.parseWorkspaces()

	return r
}

// stripJSONC removes // and /* */ comments from a JSON string safely
func stripJSONC(input string) string {
	var result strings.Builder
	inString := false
	inLineComment := false
	inBlockComment := false
	runes := []rune(input)

	for i := 0; i < len(runes); i++ {
		r := runes[i]

		if inLineComment {
			if r == '\n' {
				inLineComment = false
				result.WriteRune(r)
			}
			continue
		}

		if inBlockComment {
			if r == '*' && i+1 < len(runes) && runes[i+1] == '/' {
				inBlockComment = false
				i++
			}
			continue
		}

		if r == '"' {
			backslashes := 0
			for j := i - 1; j >= 0 && runes[j] == '\\'; j-- {
				backslashes++
			}
			if backslashes%2 == 0 {
				inString = !inString
			}
		}

		if !inString {
			if r == '/' && i+1 < len(runes) {
				if runes[i+1] == '/' {
					inLineComment = true
					i++
					continue
				}
				if runes[i+1] == '*' {
					inBlockComment = true
					i++
					continue
				}
			}
		}

		result.WriteRune(r)
	}

	return result.String()
}

func (r *WorkspaceResolver) parseTsConfig() {
	path := filepath.Join(r.ProjectRoot, "tsconfig.json")
	data, err := os.ReadFile(path)
	if err != nil {
		return
	}

	cleanJSON := stripJSONC(string(data))
	var config struct {
		CompilerOptions struct {
			Paths map[string][]string `json:"paths"`
		} `json:"compilerOptions"`
	}

	if err := json.Unmarshal([]byte(cleanJSON), &config); err != nil {
		return
	}

	for alias, targets := range config.CompilerOptions.Paths {
		if len(targets) > 0 {
			// Handle simple prefix mapping e.g. "@/*" -> "src/*"
			cleanAlias := strings.TrimSuffix(alias, "*")
			cleanTarget := strings.TrimSuffix(targets[0], "*")
			cleanTarget = strings.TrimPrefix(cleanTarget, "./")
			r.PathAliases[cleanAlias] = cleanTarget
		}
	}
}

func (r *WorkspaceResolver) parseWorkspaces() {
	// 1. Check pnpm-workspace.yaml
	pnpmPath := filepath.Join(r.ProjectRoot, "pnpm-workspace.yaml")
	if data, err := os.ReadFile(pnpmPath); err == nil {
		content := string(data)
		lines := strings.Split(content, "\n")
		inPackages := false
		for _, line := range lines {
			line = strings.TrimSpace(line)
			if strings.HasPrefix(line, "packages:") {
				inPackages = true
				continue
			}
			if inPackages && strings.HasPrefix(line, "- ") {
				pattern := strings.TrimPrefix(line, "- ")
				pattern = strings.Trim(pattern, "'\"")
				r.findNestedPackages(pattern)
			}
		}
	}

	// 2. Check package.json workspaces
	pkgPath := filepath.Join(r.ProjectRoot, "package.json")
	if data, err := os.ReadFile(pkgPath); err == nil {
		var pkg struct {
			Workspaces []string `json:"workspaces"`
		}
		if err := json.Unmarshal(data, &pkg); err == nil {
			for _, pattern := range pkg.Workspaces {
				r.findNestedPackages(pattern)
			}
		}
	}
}

func (r *WorkspaceResolver) findNestedPackages(pattern string) {
	fullPattern := filepath.Join(r.ProjectRoot, pattern, "package.json")
	matches, _ := filepath.Glob(fullPattern)
	for _, match := range matches {
		data, err := os.ReadFile(match)
		if err != nil {
			continue
		}
		var pkg struct {
			Name string `json:"name"`
		}
		if err := json.Unmarshal(data, &pkg); err == nil && pkg.Name != "" {
			relDir, _ := filepath.Rel(r.ProjectRoot, filepath.Dir(match))
			r.Workspaces[pkg.Name] = relDir
		}
	}
}

// Resolve converts an import path to a relative file path if it matches an alias or workspace.
func (r *WorkspaceResolver) Resolve(importPath string) (string, bool) {
	// 1. Try Path Aliases
	for alias, target := range r.PathAliases {
		if strings.HasPrefix(importPath, alias) {
			return filepath.Join(target, strings.TrimPrefix(importPath, alias)), true
		}
	}

	// 2. Try Workspaces
	for pkgName, relDir := range r.Workspaces {
		if importPath == pkgName {
			return relDir, true
		}
		if strings.HasPrefix(importPath, pkgName+"/") {
			return filepath.Join(relDir, strings.TrimPrefix(importPath, pkgName+"/")), true
		}
	}

	return "", false
}
