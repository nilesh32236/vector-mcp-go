// Package config provides configuration management for the vector-mcp-go application,
// handling environment variables, logging setup, and path resolutions.
package config

import (
	"io"
	"log/slog"
	"os"
	"path/filepath"
	"strconv"
	"strings"

	"github.com/joho/godotenv"
)

// Default directory and file permissions.
const (
	DirPerm  = 0755
	FilePerm = 0644
	// DefaultDimension is the default embedding dimension.
	DefaultDimension = 1024
	// LogFormatJSON is the JSON log format.
	LogFormatJSON = "json"
)

// Config holds the application configuration parameters.
type Config struct {
	ProjectRoot        string
	DataDir            string
	DbPath             string
	ModelsDir          string
	LogPath            string
	LogLevel           string
	LogFormat          string
	ModelName          string
	RerankerModelName  string
	HFToken            string
	Dimension          int
	MatryoshkaDim      int
	DisableWatcher     bool
	EnableLiveIndexing bool
	EmbedderPoolSize   int
	APIPort            string
	Logger             *slog.Logger
	AllowedOrigins     []string
}

// LoadConfig initializes and returns a new Config instance, loading values from environment variables
// and applying overrides where provided.
func LoadConfig(dataDirOverride, modelsDirOverride, dbPathOverride string) *Config {
	_ = godotenv.Load() // Ignore error if .env file doesn't exist

	home, err := os.UserHomeDir()
	if err != nil {
		home = os.TempDir()
	}

	dataDir := dataDirOverride
	if dataDir == "" {
		dataDir = os.Getenv("DATA_DIR")
		if dataDir == "" {
			dataDir = filepath.Join(home, ".local", "share", "vector-mcp-go")
		}
	}

	dbPath := dbPathOverride
	if dbPath == "" {
		dbPath = os.Getenv("DB_PATH")
		if dbPath == "" {
			dbPath = filepath.Join(dataDir, "lancedb")
		}
	}

	modelsDir := modelsDirOverride
	if modelsDir == "" {
		modelsDir = os.Getenv("MODELS_DIR")
		if modelsDir == "" {
			modelsDir = filepath.Join(dataDir, "models")
		}
	}

	logPath := os.Getenv("LOG_PATH")
	if logPath == "" {
		logPath = filepath.Join(dataDir, "server.log")
	}

	logLevel := os.Getenv("LOG_LEVEL")
	if logLevel == "" {
		logLevel = "info"
	}

	logFormat := os.Getenv("LOG_FORMAT")
	if logFormat == "" {
		logFormat = "json"
	}
	logFormat = strings.ToLower(logFormat)

	// Ensure directories exist
	_ = os.MkdirAll(dbPath, DirPerm)
	_ = os.MkdirAll(modelsDir, DirPerm)

	// Configure structured logging
	logFile, _ := os.OpenFile(logPath, os.O_CREATE|os.O_WRONLY|os.O_APPEND, FilePerm)
	var writer io.Writer
	if logFile != nil {
		writer = io.MultiWriter(os.Stderr, logFile)
	} else {
		writer = os.Stderr
	}
	handlerOptions := &slog.HandlerOptions{
		Level: parseLogLevel(logLevel),
	}

	var handler slog.Handler
	if logFormat == "text" {
		handler = slog.NewTextHandler(writer, handlerOptions)
	} else {
		logFormat = "json"
		handler = slog.NewJSONHandler(writer, handlerOptions)
	}
	logger := slog.New(handler)

	projectRoot := os.Getenv("PROJECT_ROOT")
	if projectRoot == "" {
		cwd, _ := os.Getwd()
		projectRoot = cwd
	}

	modelName := os.Getenv("MODEL_NAME")
	if modelName == "" {
		modelName = "BAAI/bge-small-en-v1.5"
	}

	rerankerModelName := os.Getenv("RERANKER_MODEL_NAME")
	switch rerankerModelName {
	case "":
		rerankerModelName = "cross-encoder/ms-marco-MiniLM-L-6-v2"
	case "none":
		rerankerModelName = ""
	}

	disableWatcher := os.Getenv("DISABLE_FILE_WATCHER") == "true"
	enableLiveIndexing := os.Getenv("ENABLE_LIVE_INDEXING") == "true"

	embedderPoolSize := 1
	if v := os.Getenv("EMBEDDER_POOL_SIZE"); v != "" {
		if n, err := strconv.Atoi(v); err == nil && n > 0 {
			embedderPoolSize = n
		}
	}

	apiPort := os.Getenv("API_PORT")
	if apiPort == "" {
		apiPort = "47821"
	}

	matryoshkaDim := 0
	if v := os.Getenv("MATRYOSHKA_DIM"); v != "" {
		if n, err := strconv.Atoi(v); err == nil {
			matryoshkaDim = max(n, 0)
		}
	}

	allowedOriginsStr := os.Getenv("ALLOWED_ORIGINS")
	var allowedOrigins []string
	if allowedOriginsStr != "" {
		parts := strings.Split(allowedOriginsStr, ",")
		for _, p := range parts {
			trimmed := strings.TrimSpace(p)
			if trimmed != "" {
				allowedOrigins = append(allowedOrigins, trimmed)
			}
		}
	}

	return &Config{
		ProjectRoot:        projectRoot,
		DataDir:            dataDir,
		DbPath:             dbPath,
		ModelsDir:          modelsDir,
		LogPath:            logPath,
		LogLevel:           logLevel,
		LogFormat:          logFormat,
		ModelName:          modelName,
		RerankerModelName:  rerankerModelName,
		HFToken:            os.Getenv("HF_TOKEN"),
		Dimension:          DefaultDimension,
		MatryoshkaDim:      matryoshkaDim,
		DisableWatcher:     disableWatcher,
		EnableLiveIndexing: enableLiveIndexing,
		EmbedderPoolSize:   embedderPoolSize,
		APIPort:            apiPort,
		Logger:             logger,
		AllowedOrigins:     allowedOrigins,
	}
}

// GetRelativePath returns the relative path from root to path, or the original path if it fails.
func GetRelativePath(path string, root string) string {
	rel, err := filepath.Rel(root, path)
	if err != nil {
		return path
	}
	return rel
}

func parseLogLevel(level string) slog.Level {
	switch strings.ToLower(level) {
	case "debug":
		return slog.LevelDebug
	case "warn", "warning":
		return slog.LevelWarn
	case "error":
		return slog.LevelError
	default:
		return slog.LevelInfo
	}
}
