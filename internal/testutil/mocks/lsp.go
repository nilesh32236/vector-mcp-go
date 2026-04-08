package mocks

import (
	"context"
	"sync"

	"github.com/nilesh32236/vector-mcp-go/internal/lsp"
)

// Manager implements a mock LSP manager for testing.
type Manager struct {
	mu              sync.RWMutex
	started         bool
	startErr        error
	stopErr         error
	initialized     bool
	definitions     map[string][]lsp.Location
	references      map[string][]lsp.Location
	diagnostics     map[string][]lsp.Diagnostic
	callCount       int
	initializeCalls int
}

// NewMockLSPManager creates a new mock LSP manager.
func NewMockLSPManager() *Manager {
	return &Manager{
		definitions: make(map[string][]lsp.Location),
		references:  make(map[string][]lsp.Location),
		diagnostics: make(map[string][]lsp.Diagnostic),
	}
}

// WithStartError sets an error to return on Start.
func (m *Manager) WithStartError(err error) *Manager {
	m.startErr = err
	return m
}

// WithStopError sets an error to return on Stop.
func (m *Manager) WithStopError(err error) *Manager {
	m.stopErr = err
	return m
}

// WithDefinition sets a definition response for a position.
func (m *Manager) WithDefinition(uri string, line, char int, locations []lsp.Location) *Manager {
	m.mu.Lock()
	defer m.mu.Unlock()
	key := formatKey(uri, line, char)
	m.definitions[key] = locations
	return m
}

// WithReferences sets a references response for a position.
func (m *Manager) WithReferences(uri string, line, char int, locations []lsp.Location) *Manager {
	m.mu.Lock()
	defer m.mu.Unlock()
	key := formatKey(uri, line, char)
	m.references[key] = locations
	return m
}

// WithDiagnostics sets diagnostics for a file.
func (m *Manager) WithDiagnostics(uri string, diagnostics []lsp.Diagnostic) *Manager {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.diagnostics[uri] = diagnostics
	return m
}

// GetCallCount returns the total number of calls.
func (m *Manager) GetCallCount() int {
	m.mu.RLock()
	defer m.mu.RUnlock()
	return m.callCount
}

// Start simulates starting the LSP server.
func (m *Manager) Start(_ context.Context) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.callCount++
	if m.startErr != nil {
		return m.startErr
	}
	m.started = true
	return nil
}

// EnsureStarted simulates ensuring the LSP server is started.
func (m *Manager) EnsureStarted(ctx context.Context) error {
	return m.Start(ctx)
}

// Stop simulates stopping the LSP server.
func (m *Manager) Stop() error {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.callCount++
	if m.stopErr != nil {
		return m.stopErr
	}
	m.started = false
	return nil
}

// IsStarted returns whether the server is started.
func (m *Manager) IsStarted() bool {
	m.mu.RLock()
	defer m.mu.RUnlock()
	return m.started
}

// Initialize simulates LSP initialization.
func (m *Manager) Initialize(_ context.Context) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.initializeCalls++
	m.initialized = true
	return nil
}

// Call simulates a generic LSP call.
func (m *Manager) Call(_ context.Context, method string, params any, result any) error {
	m.mu.Lock()
	m.callCount++
	m.mu.Unlock()
	return nil
}

// Notify simulates a generic LSP notification.
func (m *Manager) Notify(_ context.Context, method string, params any) error {
	m.mu.Lock()
	m.callCount++
	m.mu.Unlock()
	return nil
}

// RegisterNotificationHandler simulates registering a notification handler.
func (m *Manager) RegisterNotificationHandler(_ string, handler func([]byte)) {
	// Mock implementation doesn't need to do anything here for now
}

// GetDefinition simulates going to definition.
func (m *Manager) GetDefinition(_ context.Context, uri string, line, char int) ([]lsp.Location, error) {
	m.mu.Lock()
	m.callCount++
	m.mu.Unlock()

	m.mu.RLock()
	defer m.mu.RUnlock()
	key := formatKey(uri, line, char)
	if locations, ok := m.definitions[key]; ok {
		return locations, nil
	}
	return nil, nil
}

// GetReferences simulates finding references.
func (m *Manager) GetReferences(_ context.Context, uri string, line, char int) ([]lsp.Location, error) {
	m.mu.Lock()
	m.callCount++
	m.mu.Unlock()

	m.mu.RLock()
	defer m.mu.RUnlock()
	key := formatKey(uri, line, char)
	if locations, ok := m.references[key]; ok {
		return locations, nil
	}
	return nil, nil
}

// GetDiagnostics returns diagnostics for a file.
func (m *Manager) GetDiagnostics(uri string) []lsp.Diagnostic {
	m.mu.RLock()
	defer m.mu.RUnlock()
	return m.diagnostics[uri]
}

// formatKey creates a key from URI, line, and character.
func formatKey(uri string, line, char int) string {
	return uri + ":" + string(rune(line)) + ":" + string(rune(char))
}
