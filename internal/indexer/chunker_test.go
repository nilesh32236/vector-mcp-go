package indexer

import (
	"context"
	"strings"
	"testing"
)

func TestIsTreeSitterSupported(t *testing.T) {
	tests := []struct {
		ext      string
		expected bool
	}{
		{".go", true},
		{".js", true},
		{".jsx", true},
		{".ts", true},
		{".tsx", true},
		{".php", true},
		{".py", true},
		{".rs", true},
		{".html", true},
		{".css", true},
		{".txt", false},
		{".md", false},
		{"", false},
	}

	for _, tt := range tests {
		t.Run(tt.ext, func(t *testing.T) {
			result := isTreeSitterSupported(tt.ext)
			if result != tt.expected {
				t.Errorf("isTreeSitterSupported(%q) = %v; want %v", tt.ext, result, tt.expected)
			}
		})
	}
}

func TestCountLines(t *testing.T) {
	tests := []struct {
		input    string
		expected int
	}{
		{"", 0},
		{"hello", 0},
		{"hello\nworld", 1},
		{"hello\nworld\n", 2},
		{"\n\n\n", 3},
	}

	for _, tt := range tests {
		t.Run(tt.input, func(t *testing.T) {
			result := countLines(tt.input)
			if result != tt.expected {
				t.Errorf("countLines(%q) = %v; want %v", tt.input, result, tt.expected)
			}
		})
	}
}

func TestFastChunk(t *testing.T) {
	// Empty string
	chunks := fastChunk("")
	if len(chunks) != 0 {
		t.Errorf("fastChunk(\"\") returned %v chunks; want 0", len(chunks))
	}

	// Short string
	shortStr := "Hello, World!"
	chunks = fastChunk(shortStr)
	if len(chunks) != 1 {
		t.Errorf("fastChunk(shortStr) returned %v chunks; want 1", len(chunks))
	} else if chunks[0].Content != shortStr {
		t.Errorf("fastChunk(shortStr) content = %q; want %q", chunks[0].Content, shortStr)
	}

	// Long string
	longStrRunes := make([]rune, 6000)
	for i := range longStrRunes {
		longStrRunes[i] = 'a'
	}
	longStr := string(longStrRunes)

	chunks = fastChunk(longStr)

	// Expect 3 chunks:
	// Chunk 1: 0 - 3000
	// Chunk 2: 2500 - 5500
	// Chunk 3: 5000 - 6000
	expectedChunks := 3
	if len(chunks) != expectedChunks {
		t.Errorf("fastChunk(longStr) returned %v chunks; want %v", len(chunks), expectedChunks)
	}

	if len(chunks) > 0 {
		if len([]rune(chunks[0].Content)) != 3000 {
			t.Errorf("Chunk 0 length = %v; want 3000", len([]rune(chunks[0].Content)))
		}
		if len([]rune(chunks[1].Content)) != 3000 {
			t.Errorf("Chunk 1 length = %v; want 3000", len([]rune(chunks[1].Content)))
		}
		if len([]rune(chunks[2].Content)) != 1000 {
			t.Errorf("Chunk 2 length = %v; want 1000", len([]rune(chunks[2].Content)))
		}
	}
}

func TestSplitIfNeeded(t *testing.T) {
	// Short chunk
	shortChunk := Chunk{Content: "Hello", StartLine: 1, EndLine: 1}
	chunks := splitIfNeeded(shortChunk)
	if len(chunks) != 1 {
		t.Errorf("splitIfNeeded(shortChunk) returned %v chunks; want 1", len(chunks))
	}

	// Long chunk
	longChunkRunes := make([]rune, 16000)
	for i := range longChunkRunes {
		longChunkRunes[i] = 'a'
	}
	longChunk := Chunk{Content: string(longChunkRunes), StartLine: 1, EndLine: 1}

	chunks = splitIfNeeded(longChunk)

	// Expect 3 chunks:
	// Chunk 1: 0 - 8000
	// Chunk 2: 7200 - 15200
	// Chunk 3: 14400 - 16000
	expectedChunks := 3
	if len(chunks) != expectedChunks {
		t.Errorf("splitIfNeeded(longChunk) returned %v chunks; want %v", len(chunks), expectedChunks)
	}
}

func TestParseRelationships(t *testing.T) {
	tests := []struct {
		name     string
		text     string
		ext      string
		expected []string
	}{
		{
			name: "TypeScript imports",
			text: `import { X, Y } from 'module1';
import 'module2';
require('module3');`,
			ext:      ".ts",
			expected: []string{"module1", "module2", "module3", "X", "Y"},
		},
		{
			name: "Go imports",
			text: `import "fmt"
import (
	"context"
	"strings"
	alias "github.com/pkg/errors"
)`,
			ext:      ".go",
			expected: []string{"fmt", "context", "strings", "github.com/pkg/errors"},
		},
		{
			name: "PHP requires",
			text: `require_once 'vendor/autoload.php';
use App\Models\User;
use Some\Namespace\ClassA, Some\Namespace\ClassB as B;`,
			ext:      ".php",
			expected: []string{"vendor/autoload.php", "App\\Models\\User", "Some\\Namespace\\ClassA", "Some\\Namespace\\ClassB"},
		},
		{
			name:     "Unsupported extension",
			text:     `import "fmt"`,
			ext:      ".txt",
			expected: nil,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result := parseRelationships(tt.text, tt.ext)

			// Simple unordered match
			if len(result) != len(tt.expected) {
				t.Fatalf("parseRelationships() len = %v; want %v (result: %v)", len(result), len(tt.expected), result)
			}

			expectedMap := make(map[string]bool)
			for _, exp := range tt.expected {
				expectedMap[exp] = true
			}

			for _, res := range result {
				if !expectedMap[res] {
					t.Errorf("parseRelationships() unexpected relationship: %v", res)
				}
			}
		})
	}
}

func TestCreateChunks(t *testing.T) {
	// Test unsupported file type (falls back to fastChunk)
	unsupportedText := "This is a simple text file.\nIt has two lines."
	unsupportedFilePath := "test.txt"

	chunks := CreateChunks(context.TODO(), unsupportedText, unsupportedFilePath)
	if len(chunks) == 0 {
		t.Fatal("CreateChunks(test.txt) returned 0 chunks")
	}

	// Verify ContextualString is built properly
	// Expected format: File: <file>. Entity: <scope>. Type: <type>. Docstring: <doc>. Calls: <callsStr>. Code: <code>
	expectedPrefix := "File: test.txt. Entity: Global. Type: . Docstring: None. Calls: None. Code:"
	if len(chunks[0].ContextualString) < len(expectedPrefix) || chunks[0].ContextualString[:len(expectedPrefix)] != expectedPrefix {
		t.Errorf("ContextualString mismatch.\nExpected prefix: %q\nGot: %q", expectedPrefix, chunks[0].ContextualString)
	}

	// Test supported file type (uses treeSitterChunk)
	supportedText := `
package main

import "fmt"

func hello() {
	fmt.Println("Hello, World!")
}
`
	supportedFilePath := "test.go"

	goChunks := CreateChunks(context.TODO(), supportedText, supportedFilePath)
	if len(goChunks) == 0 {
		t.Fatal("CreateChunks(test.go) returned 0 chunks")
	}

	// For Go file, we should have at least the package/import as a gap, or the function as a tree-sitter chunk
	foundFunc := false
	for _, c := range goChunks {
		if len(c.Symbols) > 0 && c.Symbols[0] == "hello" {
			foundFunc = true
			if c.Type != "function_declaration" {
				t.Errorf("Expected function_declaration chunk type, got %s", c.Type)
			}
			expectedContextPrefix := "File: test.go. Entity: hello. Type: function_declaration. Docstring: None. Calls: Println."
			if len(c.ContextualString) < len(expectedContextPrefix) || c.ContextualString[:len(expectedContextPrefix)] != expectedContextPrefix {
				t.Errorf("ContextualString mismatch.\nExpected prefix: %q\nGot: %q", expectedContextPrefix, c.ContextualString)
			}

			// Should also have "fmt" in calls since we extract that
			foundCall := false
			for _, call := range c.Calls {
				if call == "Println" {
					foundCall = true
					break
				}
			}
			if !foundCall {
				t.Errorf("Expected to find call 'Println' in chunk calls: %v", c.Calls)
			}

			break
		}
	}

	if !foundFunc {
		t.Errorf("Did not find chunk with symbol 'hello' for Go file. Chunks returned: %d", len(goChunks))
	}
}

func TestStructuralMetadata(t *testing.T) {
	code := `
package test
// User represents a user in the system.
type User struct {
	ID   int
	Name string
}

type Service interface {
	GetUser(id int) *User
}
`
	chunks := CreateChunks(context.TODO(), code, "test.go")

	foundStruct := false
	foundInterface := false

	for _, c := range chunks {
		if len(c.Symbols) > 0 && c.Symbols[0] == "User" {
			foundStruct = true
			if c.StructuralMetadata["field:ID"] != "int" {
				t.Errorf("Expected field:ID=int, got %s", c.StructuralMetadata["field:ID"])
			}
			if c.StructuralMetadata["field:Name"] != "string" {
				t.Errorf("Expected field:Name=string, got %s", c.StructuralMetadata["field:Name"])
			}
			if !strings.Contains(c.Docstring, "User represents a user") {
				t.Errorf("Docstring mismatch, got %q", c.Docstring)
			}
		}
		if len(c.Symbols) > 0 && c.Symbols[0] == "Service" {
			foundInterface = true
			if c.StructuralMetadata["method:GetUser"] != "defined" {
				t.Errorf("Expected method:GetUser=defined, got %s", c.StructuralMetadata["method:GetUser"])
			}
		}
	}

	if !foundStruct {
		t.Error("Did not find chunk for struct User")
	}
	if !foundInterface {
		t.Error("Did not find chunk for interface Service")
	}
}

func BenchmarkParseRelationships(b *testing.B) {
	text := `import { X, Y } from 'module1';
import 'module2';
require('module3');`
	for i := 0; i < b.N; i++ {
		parseRelationships(text, ".ts")
	}
}
