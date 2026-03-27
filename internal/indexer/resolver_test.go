package indexer

import (
	"testing"
)

func TestStripJSONC(t *testing.T) {
	tests := []struct {
		name     string
		input    string
		expected string
	}{
		{
			name:     "no comments",
			input:    `{"a": 1}`,
			expected: `{"a": 1}`,
		},
		{
			name:     "line comment with newline",
			input:    `{"a": 1} // comment` + "\n" + `{"b": 2}`,
			expected: `{"a": 1} ` + "\n" + `{"b": 2}`,
		},
		{
			name:     "line comment without trailing newline",
			input:    `{"a": 1} // comment`,
			expected: `{"a": 1} `,
		},
		{
			name:     "block comment",
			input:    `{"a": 1} /* comment */`,
			expected: `{"a": 1} `,
		},
		{
			name:     "multi-line block comment",
			input:    "/* multi\nline\n*/{\"a\": 1}",
			expected: `{"a": 1}`,
		},
		{
			name:     "URL in string",
			input:    `{"url": "http://example.com"}`,
			expected: `{"url": "http://example.com"}`,
		},
		{
			name:     "block comment syntax in string",
			input:    `{"text": "/* not a comment */"}`,
			expected: `{"text": "/* not a comment */"}`,
		},
		{
			name:     "escaped quote in string",
			input:    `{"text": "\"// not a comment\""}`,
			expected: `{"text": "\"// not a comment\""}`,
		},
		{
			name:     "empty string",
			input:    ``,
			expected: ``,
		},
		{
			name:     "only line comment",
			input:    `// only comment`,
			expected: ``,
		},
		{
			name:     "escaped backslash before quote",
			input:    `{"text": "\\"} // comment`,
			expected: `{"text": "\\"} `,
		},
		{
			name:     "multiple comments",
			input:    `{ // comment 1` + "\n" + `/* comment 2 */ "a": 1 }`,
			expected: `{ ` + "\n" + ` "a": 1 }`,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := stripJSONC(tt.input)
			if got != tt.expected {
				t.Errorf("stripJSONC(%q) = %q; want %q", tt.input, got, tt.expected)
			}
		})
	}
}
