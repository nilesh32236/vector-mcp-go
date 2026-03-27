package indexer

import (
	"crypto/sha256"
	"encoding/hex"
	"os"
	"path/filepath"
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
		{"Uppercase exact match", "PACKAGE-LOCK.JSON", true},
		{"Mixed case exact match", "pnpm-Lock.yaml", true},
		{"Uppercase suffix", "app.js.MAP", true},
		{"Mixed case suffix", "vendor.MIN.js", true},
		{"Uppercase SVG", "icon.SVG", true},
		{"Image PNG", "image.png", true},
		{"Image JPG", "image.jpg", true},

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

func TestGetHash(t *testing.T) {
	tempDir := t.TempDir()

	// 1. Normal file
	normalFile := filepath.Join(tempDir, "normal.txt")
	normalContent := []byte("hello world")
	if err := os.WriteFile(normalFile, normalContent, 0644); err != nil {
		t.Fatalf("Failed to create normal file: %v", err)
	}
	normalHashBytes := sha256.Sum256(normalContent)
	expectedNormalHash := hex.EncodeToString(normalHashBytes[:])

	// 2. Empty file
	emptyFile := filepath.Join(tempDir, "empty.txt")
	emptyContent := []byte("")
	if err := os.WriteFile(emptyFile, emptyContent, 0644); err != nil {
		t.Fatalf("Failed to create empty file: %v", err)
	}
	emptyHashBytes := sha256.Sum256(emptyContent)
	expectedEmptyHash := hex.EncodeToString(emptyHashBytes[:])

	// 3. Directory
	dirPath := filepath.Join(tempDir, "dir")
	if err := os.Mkdir(dirPath, 0755); err != nil {
		t.Fatalf("Failed to create directory: %v", err)
	}

	tests := []struct {
		name        string
		path        string
		expected    string
		expectError bool
	}{
		{"Normal file", normalFile, expectedNormalHash, false},
		{"Empty file", emptyFile, expectedEmptyHash, false},
		{"Non-existent file", filepath.Join(tempDir, "does-not-exist.txt"), "", true},
		{"Directory instead of file", dirPath, "", true},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			hash, err := GetHash(tt.path)

			if tt.expectError {
				if err == nil {
					t.Errorf("GetHash(%q) expected error, got nil", tt.path)
				}
			} else {
				if err != nil {
					t.Errorf("GetHash(%q) unexpected error: %v", tt.path, err)
				}
				if hash != tt.expected {
					t.Errorf("GetHash(%q) = %q, expected %q", tt.path, hash, tt.expected)
				}
			}
		})
	}
}
