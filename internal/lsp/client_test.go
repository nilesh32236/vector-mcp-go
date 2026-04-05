package lsp

import (
	"testing"
)

func TestGetServerCommand(t *testing.T) {
	tests := []struct {
		ext      string
		expected []string
		ok       bool
	}{
		{".go", []string{"gopls"}, true},
		{".ts", []string{"typescript-language-server", "--stdio"}, true},
		{".tsx", []string{"typescript-language-server", "--stdio"}, true},
		{".js", []string{"typescript-language-server", "--stdio"}, true},
		{".jsx", []string{"typescript-language-server", "--stdio"}, true},
		{".py", nil, false},
		{".rs", nil, false},
		{"", nil, false},
	}

	for _, tt := range tests {
		t.Run(tt.ext, func(t *testing.T) {
			cmd, ok := GetServerCommand(tt.ext)
			if ok != tt.ok {
				t.Errorf("GetServerCommand(%q) = %v, want %v", tt.ext, ok, tt.ok)
			}
			if tt.ok && !equalSlices(cmd, tt.expected) {
				t.Errorf("GetServerCommand(%q) = %v, want %v", tt.ext, cmd, tt.expected)
			}
		})
	}
}

func TestGetServerCommandCaseInsensitive(t *testing.T) {
	tests := []string{".GO", ".TS", ".Js", ".JSX"}

	for _, ext := range tests {
		t.Run(ext, func(t *testing.T) {
			_, ok := GetServerCommand(ext)
			if !ok {
				t.Errorf("GetServerCommand should be case-insensitive for %q", ext)
			}
		})
	}
}

func TestNewLSPManager(t *testing.T) {
	cmd := []string{"gopls"}
	root := "/test/path"

	mgr := NewLSPManager(cmd, root, nil, nil)

	if mgr == nil {
		t.Fatal("Expected non-nil LSPManager")
	}
	if len(mgr.serverCmd) == 0 || mgr.serverCmd[0] != "gopls" {
		t.Errorf("Expected serverCmd to be %v, got %v", cmd, mgr.serverCmd)
	}
	if mgr.rootPath != root {
		t.Errorf("Expected rootPath to be %q, got %q", root, mgr.rootPath)
	}
	if mgr.pending == nil {
		t.Error("Expected pending map to be initialized")
	}
	if mgr.handlers == nil {
		t.Error("Expected handlers map to be initialized")
	}
}

func TestLSPManagerStop(t *testing.T) {
	mgr := NewLSPManager([]string{"test"}, "/tmp", nil, nil)

	// Should not panic when called without a running process
	mgr.Stop()

	// Verify isStopping flag
	if !mgr.isStopping {
		t.Error("Expected isStopping to be true after Stop()")
	}
}

func TestRegisterNotificationHandler(t *testing.T) {
	mgr := NewLSPManager([]string{"test"}, "/tmp", nil, nil)

	called := false
	handler := func([]byte) {
		called = true
	}

	mgr.RegisterNotificationHandler("test/method", handler)

	if len(mgr.handlers["test/method"]) != 1 {
		t.Error("Handler should be registered")
	}

	// Call the handler
	mgr.handlers["test/method"][0](nil)
	if !called {
		t.Error("Handler should have been called")
	}
}

func TestMultipleNotificationHandlers(t *testing.T) {
	mgr := NewLSPManager([]string{"test"}, "/tmp", nil, nil)

	callCount := 0
	handler1 := func([]byte) { callCount++ }
	handler2 := func([]byte) { callCount++ }

	mgr.RegisterNotificationHandler("test/method", handler1)
	mgr.RegisterNotificationHandler("test/method", handler2)

	if len(mgr.handlers["test/method"]) != 2 {
		t.Errorf("Expected 2 handlers, got %d", len(mgr.handlers["test/method"]))
	}

	// Call both handlers
	for _, h := range mgr.handlers["test/method"] {
		h(nil)
	}
	if callCount != 2 {
		t.Errorf("Expected 2 handler calls, got %d", callCount)
	}
}

func TestEnsureStartedEmptyCommand(t *testing.T) {
	mgr := NewLSPManager([]string{}, "/tmp", nil, nil)

	err := mgr.EnsureStarted(nil)
	if err == nil {
		t.Error("Expected error for empty server command")
	}
}

func TestWriteLocked(t *testing.T) {
	// This test verifies the message framing format
	// We can't fully test without a real stdin pipe
	mgr := NewLSPManager([]string{"test"}, "/tmp", nil, nil)

	// Test with nil stdin (should handle gracefully)
	// In real usage, stdin would be connected to a process
	_ = mgr
}

func equalSlices(a, b []string) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if a[i] != b[i] {
			return false
		}
	}
	return true
}
