package indexer

import (
	"testing"
)

func TestIsIgnoredDir(t *testing.T) {
	tests := []struct {
		name     string
		dir      string
		expected bool
	}{
		// Ignored directories
		{"Node modules", "node_modules", true},
		{"Git directory", ".git", true},
		{"Next.js directory", ".next", true},
		{"Turbo repo directory", ".turbo", true},
		{"Dist directory", "dist", true},
		{"Build directory", "build", true},
		{"Generated directory", "generated", true},
		{"Coverage directory", "coverage", true},
		{"Out directory", "out", true},
		{"Vendor directory", "vendor", true},
		{"Vector DB directory", ".vector-db", true},
		{"Data directory", ".data", true},

		// Not ignored directories
		{"Source directory", "src", false},
		{"Internal directory", "internal", false},
		{"Cmd directory", "cmd", false},
		{"Empty string", "", false},
		{"Hidden file but not in list", ".config", false},
		{"Partial match", "node_modules_backup", false},
		{"Prefix match", "my_node_modules", false},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result := IsIgnoredDir(tt.dir)
			if result != tt.expected {
				t.Errorf("IsIgnoredDir(%q) = %v, expected %v", tt.dir, result, tt.expected)
			}
		})
	}
}

func TestIsIgnoredFile(t *testing.T) {
	tests := []struct {
		name     string
		file     string
		expected bool
	}{
		// Exact matches
		{"NPM lockfile", "package-lock.json", true},
		{"PNPM lockfile", "pnpm-lock.yaml", true},
		{"Yarn lockfile", "yarn.lock", true},
		{"Go sum file", "go.sum", true},

		// Suffix matches
		{"Source map file", "app.js.map", true},
		{"Minified JS file", "vendor.min.js", true},
		{"SVG file", "icon.svg", true},

		// Not ignored files
		{"Go source file", "scanner.go", false},
		{"TypeScript file", "index.ts", false},
		{"React file", "App.tsx", false},
		{"Markdown file", "README.md", false},
		{"Package file", "package.json", false}, // But package-lock.json is
		{"Go mod file", "go.mod", false},        // But go.sum is
		{"Normal JS file", "utils.js", false},   // But min.js is
		{"Empty string", "", false},
		{"Suffix only", ".map", true},
		{"Suffix in middle", "app.map.js", false},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result := IsIgnoredFile(tt.file)
			if result != tt.expected {
				t.Errorf("IsIgnoredFile(%q) = %v, expected %v", tt.file, result, tt.expected)
			}
		})
	}
}
