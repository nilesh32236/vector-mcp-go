package config

import (
	"os"
	"path/filepath"
)

type Config struct {
	ProjectRoot string
	DbPath      string
	ModelsDir   string
	ModelName   string
	HFToken     string
	Dimension   int
}

func LoadConfig() *Config {
	// Hardcoded fallback because os.Executable() or relative paths fail inside MCP node.js runner environments
	baseDir := "/home/nilesh/Documents/vector-mcp-go"

	// Default project root to one level up or use env
	projectRoot := os.Getenv("PROJECT_ROOT")
	if projectRoot == "" {
		// Default to a common teammate structure or current project
		projectRoot = "/home/nilesh/Documents/heratime"
	}

	return &Config{
		ProjectRoot: projectRoot,
		DbPath:      filepath.Join(baseDir, ".vector-db"),
		ModelsDir:   filepath.Join(baseDir, "models"),
		ModelName:   "Xenova/bge-m3",
		HFToken:     os.Getenv("HF_TOKEN"),
		Dimension:   1024,
	}
}

func GetRelativePath(path string, root string) string {
	rel, err := filepath.Rel(root, path)
	if err != nil {
		return path
	}
	return rel
}
