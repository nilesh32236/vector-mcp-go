package util

import (
	"fmt"
	"os"
	"path/filepath"
)

// FindWorkspaceRoot takes an absolute file path and traverses upwards
// directory by directory until it finds a project root marker.
func FindWorkspaceRoot(filePath string) (string, error) {
	if !filepath.IsAbs(filePath) {
		absPath, err := filepath.Abs(filePath)
		if err != nil {
			return "", fmt.Errorf("failed to get absolute path: %v", err)
		}
		filePath = absPath
	}

	currentDir := filePath
	// If filePath is actually a file, start from its directory
	if info, err := os.Stat(filePath); err == nil && !info.IsDir() {
		currentDir = filepath.Dir(filePath)
	}

	markers := []string{"go.mod", "package.json", "Cargo.toml", ".git"}

	for {
		for _, marker := range markers {
			markerPath := filepath.Join(currentDir, marker)
			if _, err := os.Stat(markerPath); err == nil {
				return currentDir, nil
			}
		}

		parent := filepath.Dir(currentDir)
		if parent == currentDir {
			// Reached the root of the filesystem
			break
		}
		currentDir = parent
	}

	return "", fmt.Errorf("no workspace root found for path: %s", filePath)
}
