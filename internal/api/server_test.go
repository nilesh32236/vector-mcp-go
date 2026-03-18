package api

import (
	"bytes"
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/nilesh32236/vector-mcp-go/internal/config"
	"github.com/nilesh32236/vector-mcp-go/internal/db"
)

type mockEmbedder struct {
	dimension int
}

func (m *mockEmbedder) Embed(ctx context.Context, text string) ([]float32, error) {
	emb := make([]float32, m.dimension)
	for i := range emb {
		emb[i] = 0.1
	}
	return emb, nil
}

// Better yet, let's just create a real temp db store for the test
func setupTestServer(t *testing.T) (*Server, func()) {
	cfg := &config.Config{
		ApiPort:   "8080",
		Dimension: 1024,
	}

	tempDir := t.TempDir()
	store, err := db.Connect(context.Background(), tempDir, "test_collection")
	if err != nil {
		t.Fatalf("failed to connect to db: %v", err)
	}

	sg := func(ctx context.Context) (*db.Store, error) {
		return store, nil
	}

	emb := &mockEmbedder{dimension: 1024}

	srv := NewServer(cfg, sg, emb)

	cleanup := func() {
		// nothing special to cleanup
	}

	return srv, cleanup
}

func TestHealthEndpoint(t *testing.T) {
	srv, cleanup := setupTestServer(t)
	defer cleanup()

	req, err := http.NewRequest("GET", "/api/health", nil)
	if err != nil {
		t.Fatal(err)
	}

	rr := httptest.NewRecorder()
	srv.srv.Handler.ServeHTTP(rr, req)

	if status := rr.Code; status != http.StatusOK {
		t.Errorf("handler returned wrong status code: got %v want %v",
			status, http.StatusOK)
	}

	var resp map[string]string
	if err := json.NewDecoder(rr.Body).Decode(&resp); err != nil {
		t.Fatal(err)
	}

	if resp["status"] != "ok" {
		t.Errorf("expected status 'ok', got '%v'", resp["status"])
	}
}

func TestContextEndpoint(t *testing.T) {
	srv, cleanup := setupTestServer(t)
	defer cleanup()

	body := ContextRequest{
		Text:   "This is some manual context",
		Source: "manual_input",
		Metadata: map[string]string{
			"custom_key": "custom_value",
		},
	}
	bodyBytes, _ := json.Marshal(body)

	req, err := http.NewRequest("POST", "/api/context", bytes.NewReader(bodyBytes))
	if err != nil {
		t.Fatal(err)
	}

	rr := httptest.NewRecorder()
	srv.srv.Handler.ServeHTTP(rr, req)

	if status := rr.Code; status != http.StatusOK {
		t.Errorf("handler returned wrong status code: got %v want %v", status, http.StatusOK)
	}

	var resp map[string]string
	if err := json.NewDecoder(rr.Body).Decode(&resp); err != nil {
		t.Fatal(err)
	}

	if resp["status"] != "success" {
		t.Errorf("expected status 'success', got '%v'", resp["status"])
	}

	// Verify it was actually inserted
	store, _ := srv.storeGetter(context.Background())
	if store.Count() != 1 {
		t.Errorf("expected 1 record in store, got %d", store.Count())
	}
}

func TestTodoEndpoint(t *testing.T) {
	srv, cleanup := setupTestServer(t)
	defer cleanup()

	body := TodoRequest{
		Title:       "Fix a bug",
		Description: "The bug is very buggy",
		Priority:    "high",
	}
	bodyBytes, _ := json.Marshal(body)

	req, err := http.NewRequest("POST", "/api/todo", bytes.NewReader(bodyBytes))
	if err != nil {
		t.Fatal(err)
	}

	rr := httptest.NewRecorder()
	srv.srv.Handler.ServeHTTP(rr, req)

	if status := rr.Code; status != http.StatusOK {
		t.Errorf("handler returned wrong status code: got %v want %v", status, http.StatusOK)
	}

	var resp map[string]string
	if err := json.NewDecoder(rr.Body).Decode(&resp); err != nil {
		t.Fatal(err)
	}

	if resp["status"] != "success" {
		t.Errorf("expected status 'success', got '%v'", resp["status"])
	}

	store, _ := srv.storeGetter(context.Background())
	if store.Count() != 1 {
		t.Errorf("expected 1 record in store, got %d", store.Count())
	}
}

func TestSearchEndpoint(t *testing.T) {
	srv, cleanup := setupTestServer(t)
	defer cleanup()

	// 1. Insert something via context endpoint
	ctxReqBody := ContextRequest{
		Text:   "This is search target",
		Source: "test",
		Metadata: map[string]string{
			"key": "value",
		},
	}
	ctxBytes, _ := json.Marshal(ctxReqBody)
	req1, _ := http.NewRequest("POST", "/api/context", bytes.NewReader(ctxBytes))
	rr1 := httptest.NewRecorder()
	srv.srv.Handler.ServeHTTP(rr1, req1)

	// 2. Search for it
	searchBody := SearchRequest{
		Query: "search target",
		TopK:  5,
	}
	searchBytes, _ := json.Marshal(searchBody)
	req2, _ := http.NewRequest("POST", "/api/search", bytes.NewReader(searchBytes))
	rr2 := httptest.NewRecorder()
	srv.srv.Handler.ServeHTTP(rr2, req2)

	if status := rr2.Code; status != http.StatusOK {
		t.Errorf("handler returned wrong status code: got %v want %v", status, http.StatusOK)
	}

	var resp []SearchResponse
	if err := json.NewDecoder(rr2.Body).Decode(&resp); err != nil {
		t.Fatal(err)
	}

	if len(resp) != 1 {
		t.Errorf("expected 1 search result, got %d", len(resp))
	} else {
		if resp[0].Text != "This is search target" {
			t.Errorf("expected text 'This is search target', got '%v'", resp[0].Text)
		}
		if resp[0].Metadata["type"] != "manual_context" {
			t.Errorf("expected metadata type 'manual_context', got '%v'", resp[0].Metadata["type"])
		}
	}
}
