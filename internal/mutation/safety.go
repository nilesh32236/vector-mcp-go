// Package mutation provides tools for safely applying and verifying code changes.
package mutation

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"strings"
	"sync"
	"time"

	"github.com/nilesh32236/vector-mcp-go/internal/lsp"
)

// SafetyChecker provides tools to verify code integrity before/after mutations.
type SafetyChecker struct {
	lspProvider func(path string) (*lsp.Manager, error)
}

// NewSafetyChecker creates a new SafetyChecker.
func NewSafetyChecker(provider func(path string) (*lsp.Manager, error)) *SafetyChecker {
	return &SafetyChecker{lspProvider: provider}
}

// VerifyPatchIntegrity checks if applying a search-and-replace patch introduces compiler errors.
func (c *SafetyChecker) VerifyPatchIntegrity(ctx context.Context, path, search, replace string) ([]lsp.Diagnostic, error) {
	if c.lspProvider == nil {
		return nil, fmt.Errorf("LSP provider not configured")
	}

	lspMgr, err := c.lspProvider(path)
	if err != nil {
		return nil, fmt.Errorf("failed to get LSP session: %w", err)
	}

	// 1. Read original
	content, err := os.ReadFile(path)
	if err != nil {
		return nil, err
	}

	// 2. Apply patch in-memory
	strContent := string(content)
	if !strings.Contains(strContent, search) {
		return nil, fmt.Errorf("search string not found")
	}
	newContent := strings.ReplaceAll(strContent, search, replace)

	// 3. Prepare to receive diagnostics
	diagChan := make(chan []lsp.Diagnostic, 1)
	uri := fmt.Sprintf("file://%s", path)

	var once sync.Once
	handler := func(payload []byte) {
		var msg struct {
			Params struct {
				URI         string           `json:"uri"`
				Diagnostics []lsp.Diagnostic `json:"diagnostics"`
			} `json:"params"`
		}
		if err := json.Unmarshal(payload, &msg); err == nil && msg.Params.URI == uri {
			once.Do(func() {
				diagChan <- msg.Params.Diagnostics
			})
		}
	}

	lspMgr.RegisterNotificationHandler("textDocument/publishDiagnostics", handler)

	// 4. Trigger LSP update
	// We use didOpen with the new content (even if already open, gopls will update)
	params := map[string]any{
		"textDocument": map[string]any{
			"uri":        uri,
			"languageId": "go",
			"version":    1,
			"text":       newContent,
		},
	}

	if err := lspMgr.EnsureStarted(ctx); err != nil {
		return nil, err
	}

	// didOpen is a notification
	_ = lspMgr.Notify(ctx, "textDocument/didOpen", params)

	// 5. Wait for diagnostics with timeout
	select {
	case diags := <-diagChan:
		return diags, nil
	case <-time.After(5 * time.Second):
		return nil, fmt.Errorf("timeout waiting for LSP diagnostics")
	case <-ctx.Done():
		return nil, ctx.Err()
	}
}

// AutoFixMutation takes a diagnostic and returns a human-readable explanation and suggestion
// for fixing the issue. In a real scenario, this could also interact with an LLM
// to provide a corrected patch.
func (c *SafetyChecker) AutoFixMutation(diag lsp.Diagnostic) string {
	severity := "Error"
	if diag.Severity == 2 {
		severity = "Warning"
	}
	return fmt.Sprintf("[%s] %s at line %d. Suggesting re-analysis of this block and checking for missing imports or type mismatches.", severity, diag.Message, diag.Range.Start.Line)
}

// Diagnostic is a re-export of lsp.Diagnostic for use in other packages.
type Diagnostic = lsp.Diagnostic
