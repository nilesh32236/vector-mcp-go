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

func TestScanFiles(t *testing.T) {
	tempDir := t.TempDir()

	// Create a diverse set of files and directories
	filesToCreate := []string{
		"main.go",
		"README.md",
		"utils.ts",
		"package.json",
		"style.css",
		// Files that should be ignored by default rules
		"package-lock.json", // Exact match ignored
		"app.min.js",        // Suffix match ignored
		"icon.svg",          // Suffix match ignored
		// Directories that should be ignored by default rules
		filepath.Join("node_modules", "module.js"),
		filepath.Join(".git", "config"),
		filepath.Join("dist", "bundle.js"),
		// Files to be ignored by custom .vector-ignore
		"secret.txt",
		filepath.Join("custom_ignore_dir", "file.go"),
		// File to be ignored by .gitignore (which shouldn't be loaded because .vector-ignore exists in this test)
		"should_not_ignore.txt", // Since we only add this to .gitignore, and .vector-ignore takes precedence, but wait, .txt is in AllowExts.
	}

	for _, f := range filesToCreate {
		fullPath := filepath.Join(tempDir, f)
		dir := filepath.Dir(fullPath)
		if err := os.MkdirAll(dir, 0755); err != nil {
			t.Fatalf("Failed to create dir %s: %v", dir, err)
		}
		if err := os.WriteFile(fullPath, []byte("content"), 0644); err != nil {
			t.Fatalf("Failed to create file %s: %v", fullPath, err)
		}
	}

	// Create .vector-ignore
	vectorIgnoreContent := []byte("secret.txt\ncustom_ignore_dir/\n")
	if err := os.WriteFile(filepath.Join(tempDir, ".vector-ignore"), vectorIgnoreContent, 0644); err != nil {
		t.Fatalf("Failed to create .vector-ignore: %v", err)
	}

	// Create .gitignore
	gitIgnoreContent := []byte("should_not_ignore.txt\n")
	if err := os.WriteFile(filepath.Join(tempDir, ".gitignore"), gitIgnoreContent, 0644); err != nil {
		t.Fatalf("Failed to create .gitignore: %v", err)
	}

	// Expected files (absolute paths)
	expectedFilesMap := map[string]bool{
		filepath.Join(tempDir, "main.go"):               true,
		filepath.Join(tempDir, "README.md"):             true,
		filepath.Join(tempDir, "utils.ts"):              true,
		filepath.Join(tempDir, "package.json"):          true,
		filepath.Join(tempDir, "style.css"):             true,
		filepath.Join(tempDir, "should_not_ignore.txt"): true, // Not ignored because .vector-ignore takes precedence over .gitignore
	}

	scannedFiles, err := ScanFiles(tempDir)
	if err != nil {
		t.Fatalf("ScanFiles failed: %v", err)
	}

	if len(scannedFiles) != len(expectedFilesMap) {
		t.Errorf("Expected %d files, got %d. Files: %v", len(expectedFilesMap), len(scannedFiles), scannedFiles)
	}

	for _, f := range scannedFiles {
		if !expectedFilesMap[f] {
			t.Errorf("Unexpected file found in scan: %s", f)
		}
	}

	// Test fall back to .gitignore when .vector-ignore doesn't exist
	t.Run("Fallback to .gitignore", func(t *testing.T) {
		tempDir2 := t.TempDir()

		// Create files
		if err := os.WriteFile(filepath.Join(tempDir2, "main.go"), []byte(""), 0644); err != nil {
			t.Fatalf("Failed to create file: %v", err)
		}
		if err := os.WriteFile(filepath.Join(tempDir2, "git_ignored.txt"), []byte(""), 0644); err != nil {
			t.Fatalf("Failed to create file: %v", err)
		}

		// Create ONLY .gitignore
		if err := os.WriteFile(filepath.Join(tempDir2, ".gitignore"), []byte("git_ignored.txt\n"), 0644); err != nil {
			t.Fatalf("Failed to create .gitignore: %v", err)
		}

		expectedFilesMap2 := map[string]bool{
			filepath.Join(tempDir2, "main.go"): true,
		}

		scannedFiles2, err := ScanFiles(tempDir2)
		if err != nil {
			t.Fatalf("ScanFiles failed: %v", err)
		}

		if len(scannedFiles2) != len(expectedFilesMap2) {
			t.Errorf("Expected %d files, got %d. Files: %v", len(expectedFilesMap2), len(scannedFiles2), scannedFiles2)
		}

		for _, f := range scannedFiles2 {
			if !expectedFilesMap2[f] {
				t.Errorf("Unexpected file found in scan: %s", f)
			}
		}
	})

	// Test with a non-existent directory
	t.Run("Non-existent directory", func(t *testing.T) {
		_, err := ScanFiles(filepath.Join(tempDir, "does-not-exist-12345"))
		if err == nil {
			t.Errorf("Expected an error when scanning a non-existent directory, got nil")
		}
	})
}

func TestEstimateTokens(t *testing.T) {
	tests := []struct {
		name     string
		text     string
		expected int
	}{
		{"Empty string", "", 0},
		{"Single word", "hello", 1},
		{"Multiple words", "hello world", 2},
		{"Multiple spaces and newlines", "hello   world\n\nfoo", 4},
		{"A longer phrase with punctuation", "This is a longer phrase. It has some punctuation!", 12}, // 9 words * 4 / 3 = 36 / 3 = 12
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result := EstimateTokens(tt.text)
			if result != tt.expected {
				t.Errorf("EstimateTokens(%q) = %d, expected %d", tt.text, result, tt.expected)
			}
		})
	}
}
