package indexer

import (
	"testing"
)

func TestIsIgnoredFile(t *testing.T) {
	tests := []struct {
		name     string
		filename string
		want     bool
	}{
		// Exact matches
		{"exact match package-lock.json", "package-lock.json", true},
		{"exact match pnpm-lock.yaml", "pnpm-lock.yaml", true},
		{"exact match yarn.lock", "yarn.lock", true},
		{"exact match go.sum", "go.sum", true},

		// Suffix matches
		{"suffix match .map", "app.map", true},
		{"suffix match .min.js", "script.min.js", true},
		{"suffix match .svg", "icon.svg", true},

		// Suffix matching exact
		{"suffix match exactly .map", ".map", true},
		{"suffix match exactly .min.js", ".min.js", true},
		{"suffix match exactly .svg", ".svg", true},

		// Non-matches
		{"non-match regular file", "main.go", false},
		{"non-match contains exact but not exact", "package-lock.json.txt", false},
		{"non-match contains suffix but not at end", "script.min.js.map.txt", false},
		{"non-match almost suffix", "app.min.js.txt", false},
		{"non-match empty string", "", false},
		{"non-match prefix of exact", "package-lock", false},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := IsIgnoredFile(tt.filename); got != tt.want {
				t.Errorf("IsIgnoredFile(%q) = %v, want %v", tt.filename, got, tt.want)
			}
		})
	}
}
