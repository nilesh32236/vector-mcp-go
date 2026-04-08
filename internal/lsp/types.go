// Package lsp provides language server protocol client capabilities.
package lsp

// Position represents a position in a document.
type Position struct {
	Line      int `json:"line"`
	Character int `json:"character"`
}

// Range represents a text range in a document.
type Range struct {
	Start Position `json:"start"`
	End   Position `json:"end"`
}

// Location represents a code location for LSP responses.
type Location struct {
	URI   string `json:"uri"`
	Range Range  `json:"range"`
}

// Diagnostic represents an LSP diagnostic issue.
type Diagnostic struct {
	Range    Range  `json:"range"`
	Severity int    `json:"severity"`
	Message  string `json:"message"`
	Source   string `json:"source"`
}
