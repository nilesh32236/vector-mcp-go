package config

import (
	"io"
	"log/slog"
	"os"
	"path/filepath"

	"github.com/joho/godotenv"
)

type Config struct {
	ProjectRoot    string
	DataDir        string
	DbPath         string
	ModelsDir      string
	LogPath        string
	ModelName      string
	HFToken        string
	Dimension      int
	DisableWatcher bool
	ApiPort        string
	Logger         *slog.Logger
}

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

	// Ensure directories exist
	os.MkdirAll(dbPath, 0755)
	os.MkdirAll(modelsDir, 0755)

	// Configure structured logging
	logFile, _ := os.OpenFile(logPath, os.O_CREATE|os.O_WRONLY|os.O_APPEND, 0666)
	var writer io.Writer
	if logFile != nil {
		writer = io.MultiWriter(os.Stderr, logFile)
	} else {
		writer = os.Stderr
	}
	handler := slog.NewJSONHandler(writer, nil)
	logger := slog.New(handler)

	projectRoot := os.Getenv("PROJECT_ROOT")
	if projectRoot == "" {
		cwd, _ := os.Getwd()
		projectRoot = cwd
	}

	modelName := os.Getenv("MODEL_NAME")
	if modelName == "" {
		modelName = "Xenova/bge-m3"
	}

	disableWatcher := os.Getenv("DISABLE_FILE_WATCHER") == "true"

	apiPort := os.Getenv("API_PORT")
	if apiPort == "" {
		apiPort = "8080"
	}

	return &Config{
		ProjectRoot:    projectRoot,
		DataDir:        dataDir,
		DbPath:         dbPath,
		ModelsDir:      modelsDir,
		LogPath:        logPath,
		ModelName:      modelName,
		HFToken:        os.Getenv("HF_TOKEN"),
		Dimension:      1024,
		DisableWatcher: disableWatcher,
		ApiPort:        apiPort,
		Logger:         logger,
	}
}

func GetRelativePath(path string, root string) string {
	rel, err := filepath.Rel(root, path)
	if err != nil {
		return path
	}
	return rel
}
