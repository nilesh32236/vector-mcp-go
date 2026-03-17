package config

import (
	"log/slog"
	"os"
	"path/filepath"
)

type Config struct {
	ProjectRoot string
	DataDir     string
	DbPath      string
	ModelsDir   string
	LogPath     string
	ModelName   string
	HFToken     string
	Dimension   int
	Logger      *slog.Logger
}

func LoadConfig() *Config {
	home, err := os.UserHomeDir()
	if err != nil {
		home = os.TempDir()
	}

	dataDir := filepath.Join(home, ".local", "share", "vector-mcp-go")
	dbPath := filepath.Join(dataDir, "lancedb")
	modelsDir := filepath.Join(dataDir, "models")
	logPath := filepath.Join(dataDir, "server.log")

	// Ensure directories exist
	os.MkdirAll(dbPath, 0755)
	os.MkdirAll(modelsDir, 0755)

	// Configure structured logging
	logFile, _ := os.OpenFile(logPath, os.O_CREATE|os.O_WRONLY|os.O_APPEND, 0666)
	var handler slog.Handler
	if logFile != nil {
		handler = slog.NewJSONHandler(logFile, nil)
	} else {
		handler = slog.NewJSONHandler(os.Stderr, nil)
	}
	logger := slog.New(handler)

	projectRoot := os.Getenv("PROJECT_ROOT")
	if projectRoot == "" {
		cwd, _ := os.Getwd()
		projectRoot = cwd
	}

	return &Config{
		ProjectRoot: projectRoot,
		DataDir:     dataDir,
		DbPath:      dbPath,
		ModelsDir:   modelsDir,
		LogPath:     logPath,
		ModelName:   "Xenova/bge-m3",
		HFToken:     os.Getenv("HF_TOKEN"),
		Dimension:   1024,
		Logger:      logger,
	}
}

func GetRelativePath(path string, root string) string {
	rel, err := filepath.Rel(root, path)
	if err != nil {
		return path
	}
	return rel
}
