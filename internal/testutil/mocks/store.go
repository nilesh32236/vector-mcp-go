// Package mocks provides mock implementations of interfaces for testing.
package mocks

import (
	"context"
	"sync"

	"github.com/nilesh32236/vector-mcp-go/internal/db"
)

// MockStore implements the IndexerStore interface for testing.
type MockStore struct {
	mu          sync.RWMutex
	records     []db.Record
	statuses    map[string]string
	pathHashes  map[string]string
	insertErr   error
	searchErr   error
	statusErr   error
	deleteErr   error
	searchCalls int
	insertCalls int
}

// NewMockStore creates a new MockStore.
func NewMockStore() *MockStore {
	return &MockStore{
		records:    make([]db.Record, 0),
		statuses:   make(map[string]string),
		pathHashes: make(map[string]string),
	}
}

// WithInsertError sets an error to return on Insert.
func (m *MockStore) WithInsertError(err error) *MockStore {
	m.insertErr = err
	return m
}

// WithSearchError sets an error to return on Search operations.
func (m *MockStore) WithSearchError(err error) *MockStore {
	m.searchErr = err
	return m
}

// WithStatusError sets an error to return on Status operations.
func (m *MockStore) WithStatusError(err error) *MockStore {
	m.statusErr = err
	return m
}

// WithDeleteError sets an error to return on Delete operations.
func (m *MockStore) WithDeleteError(err error) *MockStore {
	m.deleteErr = err
	return m
}

// WithRecords sets the initial records in the store.
func (m *MockStore) WithRecords(records []db.Record) *MockStore {
	m.mu.Lock()
	m.records = records
	m.mu.Unlock()
	return m
}

// GetCallCounts returns the number of times Search and Insert were called.
func (m *MockStore) GetCallCounts() (searchCalls, insertCalls int) {
	m.mu.RLock()
	defer m.mu.RUnlock()
	return m.searchCalls, m.insertCalls
}

// Search implements Searcher.
func (m *MockStore) Search(ctx context.Context, embedding []float32, topK int, projectIDs []string, category string) ([]db.Record, error) {
	m.mu.Lock()
	m.searchCalls++
	m.mu.Unlock()

	if m.searchErr != nil {
		return nil, m.searchErr
	}

	m.mu.RLock()
	defer m.mu.RUnlock()

	if topK > len(m.records) {
		topK = len(m.records)
	}
	return m.records[:topK], nil
}

// SearchWithScore implements Searcher.
func (m *MockStore) SearchWithScore(ctx context.Context, queryEmbedding []float32, topK int, projectIDs []string, category string) ([]db.Record, []float32, error) {
	m.mu.Lock()
	m.searchCalls++
	m.mu.Unlock()

	if m.searchErr != nil {
		return nil, nil, m.searchErr
	}

	m.mu.RLock()
	defer m.mu.RUnlock()

	if topK > len(m.records) {
		topK = len(m.records)
	}
	scores := make([]float32, topK)
	for i := range scores {
		scores[i] = 0.9 - float32(i)*0.1
	}
	return m.records[:topK], scores, nil
}

// HybridSearch implements Searcher.
func (m *MockStore) HybridSearch(ctx context.Context, query string, queryEmbedding []float32, topK int, projectIDs []string, category string) ([]db.Record, error) {
	m.mu.Lock()
	m.searchCalls++
	m.mu.Unlock()

	if m.searchErr != nil {
		return nil, m.searchErr
	}

	m.mu.RLock()
	defer m.mu.RUnlock()

	if topK > len(m.records) {
		topK = len(m.records)
	}
	return m.records[:topK], nil
}

// LexicalSearch implements Searcher.
func (m *MockStore) LexicalSearch(ctx context.Context, query string, topK int, projectIDs []string, category string) ([]db.Record, error) {
	m.mu.Lock()
	m.searchCalls++
	m.mu.Unlock()

	if m.searchErr != nil {
		return nil, m.searchErr
	}

	m.mu.RLock()
	defer m.mu.RUnlock()

	var results []db.Record
	for _, r := range m.records {
		if topK > 0 && len(results) >= topK {
			break
		}
		// Simple substring match for testing
		results = append(results, r)
	}
	return results, nil
}

// Insert implements StoreManager.
func (m *MockStore) Insert(ctx context.Context, records []db.Record) error {
	m.mu.Lock()
	m.insertCalls++
	defer m.mu.Unlock()

	if m.insertErr != nil {
		return m.insertErr
	}

	m.records = append(m.records, records...)
	return nil
}

// DeleteByPrefix implements StoreManager.
func (m *MockStore) DeleteByPrefix(ctx context.Context, prefix string, projectID string) error {
	m.mu.Lock()
	defer m.mu.Unlock()

	if m.deleteErr != nil {
		return m.deleteErr
	}

	var filtered []db.Record
	for _, r := range m.records {
		if r.Metadata["path"] != prefix {
			filtered = append(filtered, r)
		}
	}
	m.records = filtered
	return nil
}

// ClearProject implements StoreManager.
func (m *MockStore) ClearProject(ctx context.Context, projectID string) error {
	m.mu.Lock()
	defer m.mu.Unlock()

	if m.deleteErr != nil {
		return m.deleteErr
	}

	var filtered []db.Record
	for _, r := range m.records {
		if r.Metadata["project_id"] != projectID {
			filtered = append(filtered, r)
		}
	}
	m.records = filtered
	return nil
}

// GetPathHashMapping implements StoreManager.
func (m *MockStore) GetPathHashMapping(ctx context.Context, projectID string) (map[string]string, error) {
	m.mu.RLock()
	defer m.mu.RUnlock()

	result := make(map[string]string)
	for path, hash := range m.pathHashes {
		result[path] = hash
	}
	return result, nil
}

// GetAllRecords implements StoreManager.
func (m *MockStore) GetAllRecords(ctx context.Context) ([]db.Record, error) {
	m.mu.RLock()
	defer m.mu.RUnlock()

	return m.records, nil
}

// GetAllMetadata implements StoreManager.
func (m *MockStore) GetAllMetadata(ctx context.Context) ([]db.Record, error) {
	m.mu.RLock()
	defer m.mu.RUnlock()

	return m.records, nil
}

// GetByPath implements StoreManager.
func (m *MockStore) GetByPath(ctx context.Context, path string, projectID string) ([]db.Record, error) {
	m.mu.RLock()
	defer m.mu.RUnlock()

	var results []db.Record
	for _, r := range m.records {
		if r.Metadata["path"] == path {
			results = append(results, r)
		}
	}
	return results, nil
}

// GetByPrefix implements StoreManager.
func (m *MockStore) GetByPrefix(ctx context.Context, prefix string, projectID string) ([]db.Record, error) {
	m.mu.RLock()
	defer m.mu.RUnlock()

	var results []db.Record
	for _, r := range m.records {
		// Simple prefix check
		results = append(results, r)
	}
	return results, nil
}

// Count implements StoreManager.
func (m *MockStore) Count() int64 {
	m.mu.RLock()
	defer m.mu.RUnlock()
	return int64(len(m.records))
}

// GetStatus implements StatusProvider.
func (m *MockStore) GetStatus(ctx context.Context, projectID string) (string, error) {
	m.mu.RLock()
	defer m.mu.RUnlock()

	if m.statusErr != nil {
		return "", m.statusErr
	}
	return m.statuses[projectID], nil
}

// GetAllStatuses implements StatusProvider.
func (m *MockStore) GetAllStatuses(ctx context.Context) (map[string]string, error) {
	m.mu.RLock()
	defer m.mu.RUnlock()

	if m.statusErr != nil {
		return nil, m.statusErr
	}

	result := make(map[string]string)
	for k, v := range m.statuses {
		result[k] = v
	}
	return result, nil
}

// SetStatus sets a project status (helper for tests).
func (m *MockStore) SetStatus(projectID, status string) {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.statuses[projectID] = status
}

// SetPathHash sets a path hash (helper for tests).
func (m *MockStore) SetPathHash(path, hash string) {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.pathHashes[path] = hash
}
