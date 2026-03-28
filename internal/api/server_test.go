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

func (m *mockEmbedder) EmbedBatch(ctx context.Context, texts []string) ([][]float32, error) {
	results := make([][]float32, len(texts))
	for i, text := range texts {
		emb, err := m.Embed(ctx, text)
		if err != nil {
			return nil, err
		}
		results[i] = emb
	}
	return results, nil
}

// Better yet, let's just create a real temp db store for the test
func setupTestServer(t *testing.T) (*Server, func()) {
	tempDir := t.TempDir()

	cfg := &config.Config{
		ApiPort:            "8080",
		Dimension:          1024,
		GeminiApiKey:       "test-key",
		DefaultGeminiModel: "gemini-test-model",
		DataDir:            tempDir,
	}

	store, err := db.Connect(context.Background(), tempDir, "test_collection", 1024)
	if err != nil {
		t.Fatalf("failed to connect to db: %v", err)
	}

	sg := func(ctx context.Context) (*db.Store, error) {
		return store, nil
	}

	emb := &mockEmbedder{dimension: 1024}

	srv := NewServer(cfg, sg, emb, nil)

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

func TestChatEndpoint(t *testing.T) {
	srv, cleanup := setupTestServer(t)
	defer cleanup()

	// 0. Create a session first
	req0, _ := http.NewRequest("POST", "/api/sessions", bytes.NewReader([]byte(`{"title": "Test Chat"}`)))
	rr0 := httptest.NewRecorder()
	srv.srv.Handler.ServeHTTP(rr0, req0)
	var sess map[string]interface{}
	json.NewDecoder(rr0.Body).Decode(&sess)
	sessionID := sess["id"].(string)

	// 1. Insert dummy context
	ctxReqBody := ContextRequest{
		Text:   "The indexing worker processes files in the background.",
		Source: "worker.go",
		Metadata: map[string]string{
			"path": "internal/worker/worker.go",
		},
	}
	ctxBytes, _ := json.Marshal(ctxReqBody)
	req1, _ := http.NewRequest("POST", "/api/context", bytes.NewReader(ctxBytes))
	rr1 := httptest.NewRecorder()
	srv.srv.Handler.ServeHTTP(rr1, req1)

	callCount := 0

	// 2. Setup mock Gemini server
	mockGemini := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		callCount++

		// Verify API Key
		if r.URL.Query().Get("key") != "test-key" {
			t.Errorf("expected API key 'test-key', got '%s'", r.URL.Query().Get("key"))
		}

		// Read and verify request body
		var geminiReq map[string]interface{}
		json.NewDecoder(r.Body).Decode(&geminiReq)

		sysInstr, ok := geminiReq["system_instruction"].(map[string]interface{})
		if !ok {
			t.Errorf("system_instruction is missing")
		} else {
			parts := sysInstr["parts"].([]interface{})
			text := parts[0].(map[string]interface{})["text"].(string)
			if !bytes.Contains([]byte(text), []byte("File: internal/worker/worker.go")) {
				t.Errorf("system instruction does not contain expected file path context")
			}
		}

		// First call: Simulate a function call
		if callCount == 1 {
			resp := `{
				"candidates": [
					{
						"content": {
							"parts": [{"functionCall": {"name": "save_manual_context", "args": {"content": "This is important to remember"}}}]
						}
					}
				]
			}`
			w.Header().Set("Content-Type", "application/json")
			w.Write([]byte(resp))
			return
		}

		// Second call: Return final text
		resp := `{
			"candidates": [
				{
					"content": {
						"parts": [{"text": "It processes files in the background."}]
					}
				}
			]
		}`
		w.Header().Set("Content-Type", "application/json")
		w.Write([]byte(resp))
	}))
	defer mockGemini.Close()

	// 3. Make chat request
	chatBody := ChatRequest{
		SessionID: sessionID,
		Message:   "How does the indexing worker work?",
	}
	chatBytes, _ := json.Marshal(chatBody)
	req2, _ := http.NewRequest("POST", "/api/chat", bytes.NewReader(chatBytes))
	req2.Header.Set("X-Test-Gemini-URL", mockGemini.URL)

	rr2 := httptest.NewRecorder()
	srv.srv.Handler.ServeHTTP(rr2, req2)

	if status := rr2.Code; status != http.StatusOK {
		t.Errorf("handler returned wrong status code: got %v want %v", status, http.StatusOK)
	}

	var resp ChatResponse
	if err := json.NewDecoder(rr2.Body).Decode(&resp); err != nil {
		t.Fatal(err)
	}

	if resp.ModelUsed != "gemini-test-model" {
		t.Errorf("expected model 'gemini-test-model', got '%s'", resp.ModelUsed)
	}
	if resp.Content != "It processes files in the background." {
		t.Errorf("expected generated content 'It processes files in the background.', got '%s'", resp.Content)
	}

	if callCount != 2 {
		t.Errorf("expected Gemini to be called 2 times (function call + final text), got %d", callCount)
	}

	// 4. Verify context was saved to LanceDB
	store, _ := srv.storeGetter(context.Background())
	if store.Count() != 2 { // 1 initial context + 1 saved via function call
		t.Errorf("expected 2 records in store (1 initial + 1 function call), got %d", store.Count())
	}
}

func TestChatEndpointNoApiKey(t *testing.T) {
	srv, cleanup := setupTestServer(t)
	defer cleanup()

	srv.cfg.GeminiApiKey = "" // Unset API key

	chatBody := ChatRequest{
		SessionID: "dummy-id",
		Message:   "Hello",
	}
	chatBytes, _ := json.Marshal(chatBody)
	req, _ := http.NewRequest("POST", "/api/chat", bytes.NewReader(chatBytes))

	rr := httptest.NewRecorder()
	srv.srv.Handler.ServeHTTP(rr, req)

	if status := rr.Code; status != http.StatusNotImplemented {
		t.Errorf("handler returned wrong status code: got %v want %v", status, http.StatusNotImplemented)
	}
}
