package mocks

import (
	"context"
	"sync"
)

// MockLSPManager implements a mock LSP manager for testing.
type MockLSPManager struct {
	mu              sync.RWMutex
	started         bool
	startErr        error
	stopErr         error
	initialized     bool
	definitions     map[string][]Location
	references      map[string][]Location
	diagnostics     map[string][]Diagnostic
	callCount       int
	initializeCalls int
}

// Location represents a code location for LSP responses.
type Location struct {
	URI   string `json:"uri"`
	Range Range  `json:"range"`
}

// Range represents a text range in a document.
type Range struct {
	Start Position `json:"start"`
	End   Position `json:"end"`
}

// Position represents a position in a document.
type Position struct {
	Line      int `json:"line"`
	Character int `json:"character"`
}

// Diagnostic represents an LSP diagnostic.
type Diagnostic struct {
	Range    Range  `json:"range"`
	Severity int    `json:"severity"`
	Message  string `json:"message"`
	Source   string `json:"source"`
}

// NewMockLSPManager creates a new mock LSP manager.
func NewMockLSPManager() *MockLSPManager {
	return &MockLSPManager{
		definitions: make(map[string][]Location),
		references:  make(map[string][]Location),
		diagnostics: make(map[string][]Diagnostic),
	}
}

// WithStartError sets an error to return on Start.
func (m *MockLSPManager) WithStartError(err error) *MockLSPManager {
	m.startErr = err
	return m
}

// WithStopError sets an error to return on Stop.
func (m *MockLSPManager) WithStopError(err error) *MockLSPManager {
	m.stopErr = err
	return m
}

// WithDefinition sets a definition response for a position.
func (m *MockLSPManager) WithDefinition(uri string, line, char int, locations []Location) *MockLSPManager {
	m.mu.Lock()
	defer m.mu.Unlock()
	key := formatKey(uri, line, char)
	m.definitions[key] = locations
	return m
}

// WithReferences sets a references response for a position.
func (m *MockLSPManager) WithReferences(uri string, line, char int, locations []Location) *MockLSPManager {
	m.mu.Lock()
	defer m.mu.Unlock()
	key := formatKey(uri, line, char)
	m.references[key] = locations
	return m
}

// WithDiagnostics sets diagnostics for a file.
func (m *MockLSPManager) WithDiagnostics(uri string, diagnostics []Diagnostic) *MockLSPManager {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.diagnostics[uri] = diagnostics
	return m
}

// GetCallCount returns the total number of calls.
func (m *MockLSPManager) GetCallCount() int {
	m.mu.RLock()
	defer m.mu.RUnlock()
	return m.callCount
}

// Start simulates starting the LSP server.
func (m *MockLSPManager) Start(ctx context.Context) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.callCount++
	if m.startErr != nil {
		return m.startErr
	}
	m.started = true
	return nil
}

// Stop simulates stopping the LSP server.
func (m *MockLSPManager) Stop() error {
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
func (m *MockLSPManager) IsStarted() bool {
	m.mu.RLock()
	defer m.mu.RUnlock()
	return m.started
}

// Initialize simulates LSP initialization.
func (m *MockLSPManager) Initialize(ctx context.Context, rootPath string) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.initializeCalls++
	m.initialized = true
	return nil
}

// GetDefinition simulates going to definition.
func (m *MockLSPManager) GetDefinition(ctx context.Context, uri string, line, char int) ([]Location, error) {
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
func (m *MockLSPManager) GetReferences(ctx context.Context, uri string, line, char int) ([]Location, error) {
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
func (m *MockLSPManager) GetDiagnostics(uri string) []Diagnostic {
	m.mu.RLock()
	defer m.mu.RUnlock()
	return m.diagnostics[uri]
}

// formatKey creates a key from URI, line, and character.
func formatKey(uri string, line, char int) string {
	return uri + ":" + string(rune(line)) + ":" + string(rune(char))
}
