package mutation

import (
	"encoding/json"
	"testing"
)

func TestNewSafetyChecker(t *testing.T) {
	checker := NewSafetyChecker(nil)

	if checker == nil {
		t.Fatal("Expected non-nil SafetyChecker")
	}

	// Test with nil provider
	if checker.lspProvider != nil {
		t.Error("Expected nil lspProvider")
	}
}

func TestSafetyChecker_VerifyPatchIntegrity_NilProvider(t *testing.T) {
	checker := NewSafetyChecker(nil)

	diags, err := checker.VerifyPatchIntegrity(nil, "/test/path", "old", "new")
	if err == nil {
		t.Error("Expected error with nil LSP provider")
	}
	if diags != nil {
		t.Error("Expected nil diagnostics on error")
	}
}

func TestSafetyChecker_VerifyPatchIntegrity_ProviderError(t *testing.T) {
	// The actual signature expects *lsp.LSPManager
	checker := &SafetyChecker{
		lspProvider: nil, // This will cause the nil provider error
	}

	diags, err := checker.VerifyPatchIntegrity(nil, "/test/path", "old", "new")
	if err == nil {
		t.Error("Expected error with nil LSP provider")
	}
	if diags != nil {
		t.Error("Expected nil diagnostics on error")
	}
}

func TestAutoFixMutation_Error(t *testing.T) {
	checker := NewSafetyChecker(nil)

	diag := Diagnostic{
		Range: Range{
			Start: Position{Line: 10, Character: 0},
			End:   Position{Line: 10, Character: 20},
		},
		Severity: 1,
		Message:  "undefined variable",
		Source:   "test",
	}

	result := checker.AutoFixMutation(diag)

	if result == "" {
		t.Error("Expected non-empty auto-fix message")
	}
}

func TestAutoFixMutation_Warning(t *testing.T) {
	checker := NewSafetyChecker(nil)

	diag := Diagnostic{
		Range: Range{
			Start: Position{Line: 5, Character: 0},
			End:   Position{Line: 5, Character: 10},
		},
		Severity: 2, // Warning
		Message:  "unused variable",
		Source:   "test",
	}

	result := checker.AutoFixMutation(diag)

	if result == "" {
		t.Error("Expected non-empty auto-fix message")
	}
}

func TestDiagnostic_JSON(t *testing.T) {
	diag := Diagnostic{
		Range: Range{
			Start: Position{Line: 1, Character: 0},
			End:   Position{Line: 1, Character: 10},
		},
		Severity: 1,
		Message:  "test message",
		Source:   "test source",
	}

	data, err := json.Marshal(diag)
	if err != nil {
		t.Fatalf("Failed to marshal Diagnostic: %v", err)
	}

	var unmarshaled Diagnostic
	if err := json.Unmarshal(data, &unmarshaled); err != nil {
		t.Fatalf("Failed to unmarshal Diagnostic: %v", err)
	}

	if unmarshaled.Message != diag.Message {
		t.Errorf("Expected message %q, got %q", diag.Message, unmarshaled.Message)
	}
	if unmarshaled.Range.Start.Line != diag.Range.Start.Line {
		t.Errorf("Expected line %d, got %d", diag.Range.Start.Line, unmarshaled.Range.Start.Line)
	}
}

func TestPosition_JSON(t *testing.T) {
	pos := Position{Line: 42, Character: 10}

	data, err := json.Marshal(pos)
	if err != nil {
		t.Fatalf("Failed to marshal Position: %v", err)
	}

	var unmarshaled Position
	if err := json.Unmarshal(data, &unmarshaled); err != nil {
		t.Fatalf("Failed to unmarshal Position: %v", err)
	}

	if unmarshaled.Line != pos.Line {
		t.Errorf("Expected line %d, got %d", pos.Line, unmarshaled.Line)
	}
	if unmarshaled.Character != pos.Character {
		t.Errorf("Expected character %d, got %d", pos.Character, unmarshaled.Character)
	}
}

func TestRange_JSON(t *testing.T) {
	r := Range{
		Start: Position{Line: 0, Character: 0},
		End:   Position{Line: 10, Character: 20},
	}

	data, err := json.Marshal(r)
	if err != nil {
		t.Fatalf("Failed to marshal Range: %v", err)
	}

	var unmarshaled Range
	if err := json.Unmarshal(data, &unmarshaled); err != nil {
		t.Fatalf("Failed to unmarshal Range: %v", err)
	}

	if unmarshaled.Start.Line != r.Start.Line {
		t.Errorf("Expected start line %d, got %d", r.Start.Line, unmarshaled.Start.Line)
	}
	if unmarshaled.End.Line != r.End.Line {
		t.Errorf("Expected end line %d, got %d", r.End.Line, unmarshaled.End.Line)
	}
}
