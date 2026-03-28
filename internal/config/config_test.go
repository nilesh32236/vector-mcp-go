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
		t.Setenv("EMBEDDER_POOL_SIZE", "")
		t.Setenv("API_PORT", "")
		t.Setenv("GEMINI_DEFAULT_MODEL", "")
		t.Setenv("DISABLE_FILE_WATCHER", "")
		t.Setenv("HF_TOKEN", "")
		t.Setenv("GEMINI_API_KEY", "")

		cfg := LoadConfig("", "", "")

		if cfg.DataDir != dataDir {
			t.Errorf("expected DataDir %s, got %s", dataDir, cfg.DataDir)
		}
		if cfg.DbPath != filepath.Join(dataDir, "lancedb") {
			t.Errorf("expected DbPath %s, got %s", filepath.Join(dataDir, "lancedb"), cfg.DbPath)
		}
		if cfg.ApiPort != "47821" {
			t.Errorf("expected ApiPort 47821, got %s", cfg.ApiPort)
		}
		if cfg.ModelName != "BAAI/bge-small-en-v1.5" {
			t.Errorf("expected ModelName BAAI/bge-small-en-v1.5, got %s", cfg.ModelName)
		}
		if cfg.EmbedderPoolSize != 1 {
			t.Errorf("expected EmbedderPoolSize 1, got %d", cfg.EmbedderPoolSize)
		}
		if cfg.DefaultGeminiModel != "gemini-2.5-flash" {
			t.Errorf("expected DefaultGeminiModel gemini-2.5-flash, got %s", cfg.DefaultGeminiModel)
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
		t.Setenv("GEMINI_DEFAULT_MODEL", "gemini-pro")
		t.Setenv("DISABLE_FILE_WATCHER", "true")
		t.Setenv("HF_TOKEN", "test-hf-token")
		t.Setenv("GEMINI_API_KEY", "test-gemini-key")

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
		if cfg.EmbedderPoolSize != 5 {
			t.Errorf("expected EmbedderPoolSize 5, got %d", cfg.EmbedderPoolSize)
		}
		if cfg.ApiPort != "9090" {
			t.Errorf("expected ApiPort 9090, got %s", cfg.ApiPort)
		}
		if cfg.DefaultGeminiModel != "gemini-pro" {
			t.Errorf("expected DefaultGeminiModel gemini-pro, got %s", cfg.DefaultGeminiModel)
		}
		if !cfg.DisableWatcher {
			t.Errorf("expected DisableWatcher true, got %v", cfg.DisableWatcher)
		}
		if cfg.HFToken != "test-hf-token" {
			t.Errorf("expected HFToken test-hf-token, got %s", cfg.HFToken)
		}
		if cfg.GeminiApiKey != "test-gemini-key" {
			t.Errorf("expected GeminiApiKey test-gemini-key, got %s", cfg.GeminiApiKey)
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
