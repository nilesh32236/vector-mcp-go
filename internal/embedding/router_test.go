package embedding

import (
	"testing"
)

func TestDetectLanguageFromPath(t *testing.T) {
	tests := []struct {
		path     string
		expected string
	}{
		// Basic cases from langMap in router.go
		{"main.go", "go"},
		{"app.py", "python"},
		{"index.js", "javascript"},
		{"app.ts", "typescript"},
		{"view.jsx", "javascript"},
		{"view.tsx", "typescript"},
		{"App.java", "java"},
		{"main.c", "c"},
		{"main.cpp", "cpp"},
		{"main.cc", "cpp"},
		{"main.cxx", "cpp"},
		{"main.rs", "rust"},
		{"script.rb", "ruby"},
		{"api.php", "php"},
		{"App.swift", "swift"},
		{"Main.kt", "kotlin"},
		{"Main.scala", "scala"},
		{"App.cs", "csharp"},
		{"script.sh", "bash"},
		{"script.bash", "bash"},

		// Case sensitivity
		{"MAIN.GO", "go"},
		{"App.PY", "python"},

		// Multiple dots
		{"util.test.go", "go"},

		// No extension or unknown
		{"Makefile", ""},
		{"README", ""},
		{"unsupported.xyz", ""},
		{".gitignore", ""},
	}

	for _, tt := range tests {
		t.Run(tt.path, func(t *testing.T) {
			result := DetectLanguageFromPath(tt.path)
			if result != tt.expected {
				t.Errorf("DetectLanguageFromPath(%q) = %q; want %q", tt.path, result, tt.expected)
			}
		})
	}
}

func TestDetectContentType(t *testing.T) {
	tests := []struct {
		name     string
		path     string
		content  string
		expected ContentType
	}{
		{"Go file", "main.go", "package main\nfunc main() {}", ContentTypeCode},
		{"Markdown file", "README.md", "# Documentation", ContentTypeDoc},
		{"JSON file", "config.json", `{"key": "value"}`, ContentTypeConfig},
		{"YAML file", "config.yaml", "key: value", ContentTypeConfig},
		{"YML file", "config.yml", "key: value", ContentTypeConfig},
		{"Python file", "main.py", "def hello():\n    pass", ContentTypeCode},
		{"JS file", "app.js", "function test() { return 1; }", ContentTypeCode},
		{"Text file", "data.txt", "some plain text", ContentTypeDoc},
		{"RST file", "document.rst", "Title\n=====", ContentTypeDoc},
		{"Adoc file", "document.adoc", "= Title", ContentTypeDoc},
		{"Shell script", "script.sh", "#!/bin/bash\necho hello", ContentTypeCode},
		{"CSS file", "style.css", "body { color: red; }", ContentTypeGeneral},
		{"Go content no path", "", "package main\nfunc main() { }", ContentTypeCode},
		{"Plain text no path", "", "Just some words", ContentTypeGeneral},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result := DetectContentType(tt.content, tt.path)
			if result != tt.expected {
				t.Errorf("DetectContentType(%q, %q) = %s; want %s", tt.content, tt.path, result, tt.expected)
			}
		})
	}
}
