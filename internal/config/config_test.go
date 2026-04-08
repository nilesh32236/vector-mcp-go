package config

import (
	"path/filepath"
	"testing"
)

func TestLoadConfig(t *testing.T) {
	tempDir := t.TempDir()

	t.Run("Default values", func(t *testing.T) {
		dataDir := filepath.Join(tempDir, "data_default")
		t.Setenv("DATA_DIR", dataDir)
		// Unset/Set empty others that might be in the environment
		t.Setenv("DB_PATH", "")
		t.Setenv("MODELS_DIR", "")
		t.Setenv("LOG_PATH", "")
		t.Setenv("PROJECT_ROOT", "")
		t.Setenv("MODEL_NAME", "")
		t.Setenv("RERANKER_MODEL_NAME", "")
		t.Setenv("EMBEDDER_POOL_SIZE", "")
		t.Setenv("API_PORT", "")
		t.Setenv("DISABLE_FILE_WATCHER", "")
		t.Setenv("LOG_LEVEL", "")
		t.Setenv("LOG_FORMAT", "")
		t.Setenv("MATRYOSHKA_DIM", "")
		t.Setenv("HF_TOKEN", "")

		cfg := LoadConfig("", "", "")

		if cfg.DataDir != dataDir {
			t.Errorf("expected DataDir %s, got %s", dataDir, cfg.DataDir)
		}
		if cfg.DbPath != filepath.Join(dataDir, "lancedb") {
			t.Errorf("expected DbPath %s, got %s", filepath.Join(dataDir, "lancedb"), cfg.DbPath)
		}
		if cfg.APIPort != "47821" {
			t.Errorf("expected APIPort 47821, got %s", cfg.APIPort)
		}
		if cfg.ModelName != "BAAI/bge-small-en-v1.5" {
			t.Errorf("expected ModelName BAAI/bge-small-en-v1.5, got %s", cfg.ModelName)
		}
		if cfg.EmbedderPoolSize != 1 {
			t.Errorf("expected EmbedderPoolSize 1, got %d", cfg.EmbedderPoolSize)
		}
		if cfg.LogLevel != "info" {
			t.Errorf("expected LogLevel info, got %s", cfg.LogLevel)
		}
		if cfg.LogFormat != LogFormatJSON {
			t.Errorf("expected LogFormat json, got %s", cfg.LogFormat)
		}
		if cfg.MatryoshkaDim != 0 {
			t.Errorf("expected MatryoshkaDim 0, got %d", cfg.MatryoshkaDim)
		}
	})

	t.Run("Overrides and EnvVars", func(t *testing.T) {
		customDataDir := filepath.Join(tempDir, "custom_data")
		customModelsDir := filepath.Join(tempDir, "custom_models")
		customDbPath := filepath.Join(tempDir, "custom_db")

		t.Setenv("PROJECT_ROOT", "/tmp/project")
		t.Setenv("MODEL_NAME", "custom-model")
		t.Setenv("EMBEDDER_POOL_SIZE", "5")
		t.Setenv("API_PORT", "9090")
		t.Setenv("DISABLE_FILE_WATCHER", "true")
		t.Setenv("HF_TOKEN", "test-hf-token")
		t.Setenv("RERANKER_MODEL_NAME", "custom-reranker")
		t.Setenv("LOG_LEVEL", "debug")
		t.Setenv("LOG_FORMAT", "text")
		t.Setenv("MATRYOSHKA_DIM", "256")

		cfg := LoadConfig(customDataDir, customModelsDir, customDbPath)

		if cfg.DataDir != customDataDir {
			t.Errorf("expected DataDir %s, got %s", customDataDir, cfg.DataDir)
		}
		if cfg.ModelsDir != customModelsDir {
			t.Errorf("expected ModelsDir %s, got %s", customModelsDir, cfg.ModelsDir)
		}
		if cfg.DbPath != customDbPath {
			t.Errorf("expected DbPath %s, got %s", customDbPath, cfg.DbPath)
		}
		if cfg.ProjectRoot != "/tmp/project" {
			t.Errorf("expected ProjectRoot /tmp/project, got %s", cfg.ProjectRoot)
		}
		if cfg.ModelName != "custom-model" {
			t.Errorf("expected ModelName custom-model, got %s", cfg.ModelName)
		}
		if cfg.RerankerModelName != "custom-reranker" {
			t.Errorf("expected RerankerModelName custom-reranker, got %s", cfg.RerankerModelName)
		}
		if cfg.EmbedderPoolSize != 5 {
			t.Errorf("expected EmbedderPoolSize 5, got %d", cfg.EmbedderPoolSize)
		}
		if cfg.APIPort != "9090" {
			t.Errorf("expected APIPort 9090, got %s", cfg.APIPort)
		}
		if !cfg.DisableWatcher {
			t.Errorf("expected DisableWatcher true, got %v", cfg.DisableWatcher)
		}
		if cfg.HFToken != "test-hf-token" {
			t.Errorf("expected HFToken test-hf-token, got %s", cfg.HFToken)
		}
		if cfg.LogLevel != "debug" {
			t.Errorf("expected LogLevel debug, got %s", cfg.LogLevel)
		}
		if cfg.LogFormat != "text" {
			t.Errorf("expected LogFormat text, got %s", cfg.LogFormat)
		}
		if cfg.MatryoshkaDim != 256 {
			t.Errorf("expected MatryoshkaDim 256, got %d", cfg.MatryoshkaDim)
		}
	})

	t.Run("Invalid pool size", func(t *testing.T) {
		t.Setenv("DATA_DIR", filepath.Join(tempDir, "data_invalid_pool"))
		t.Setenv("EMBEDDER_POOL_SIZE", "abc")
		cfg := LoadConfig("", "", "")
		if cfg.EmbedderPoolSize != 1 {
			t.Errorf("expected default pool size 1 for invalid input, got %d", cfg.EmbedderPoolSize)
		}

		t.Setenv("EMBEDDER_POOL_SIZE", "0")
		cfg = LoadConfig("", "", "")
		if cfg.EmbedderPoolSize != 1 {
			t.Errorf("expected default pool size 1 for non-positive input, got %d", cfg.EmbedderPoolSize)
		}
	})

	t.Run("Disable reranker with none sentinel", func(t *testing.T) {
		t.Setenv("DATA_DIR", filepath.Join(tempDir, "data_reranker_none"))
		t.Setenv("RERANKER_MODEL_NAME", "none")
		cfg := LoadConfig("", "", "")
		if cfg.RerankerModelName != "" {
			t.Errorf("expected empty RerankerModelName for none sentinel, got %q", cfg.RerankerModelName)
		}
	})

	t.Run("Invalid matryoshka dim falls back to disabled", func(t *testing.T) {
		t.Setenv("DATA_DIR", filepath.Join(tempDir, "data_matryoshka_invalid"))
		t.Setenv("MATRYOSHKA_DIM", "-64")
		cfg := LoadConfig("", "", "")
		if cfg.MatryoshkaDim != 0 {
			t.Errorf("expected MatryoshkaDim 0 for invalid input, got %d", cfg.MatryoshkaDim)
		}
	})
}

func TestGetRelativePath(t *testing.T) {
	root := "/home/user/project"

	tests := []struct {
		name     string
		path     string
		expected string
	}{
		{"Inside", "/home/user/project/internal/config.go", "internal/config.go"},
		{"Root", "/home/user/project", "."},
		{"Outside", "/home/user/other/file.go", "../other/file.go"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := GetRelativePath(tt.path, root)
			if got != tt.expected {
				t.Errorf("GetRelativePath(%s, %s) = %s, want %s", tt.path, root, got, tt.expected)
			}
		})
	}
}
