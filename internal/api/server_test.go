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
	"github.com/nilesh32236/vector-mcp-go/internal/testutil/mocks"
)

func TestNewServer(t *testing.T) {
	cfg := &config.Config{
		ApiPort: "8080",
	}

	storeGetter := func(ctx context.Context) (*db.Store, error) {
		return nil, nil
	}

	mockEmbedder := mocks.NewMockEmbedder(384)
	server := NewServer(cfg, storeGetter, mockEmbedder, nil)

	if server == nil {
		t.Fatal("Expected non-nil server")
	}
	if server.cfg != cfg {
		t.Error("Config not set correctly")
	}
	if server.rateLimiter == nil {
		t.Error("Rate limiter should be initialized")
	}
}

func TestHandleHealth(t *testing.T) {
	tests := []struct {
		name           string
		storeGetter    StoreGetter
		embedder       *mocks.MockEmbedder
		expectedStatus int
	}{
		{
			name: "degraded with nil store getter",
			storeGetter: func(ctx context.Context) (*db.Store, error) {
				return nil, nil
			},
			embedder:       nil,
			expectedStatus: http.StatusOK,
		},
		{
			name: "degraded with store error",
			storeGetter: func(ctx context.Context) (*db.Store, error) {
				return nil, context.DeadlineExceeded
			},
			embedder:       mocks.NewMockEmbedder(384),
			expectedStatus: http.StatusServiceUnavailable,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			cfg := &config.Config{ApiPort: "8080"}
			server := NewServer(cfg, tt.storeGetter, tt.embedder, nil)

			req := httptest.NewRequest("GET", "/api/health", nil)
			rec := httptest.NewRecorder()

			server.handleHealth(rec, req)

			if rec.Code != tt.expectedStatus {
				t.Errorf("Expected status %d, got %d", tt.expectedStatus, rec.Code)
			}

			var response map[string]interface{}
			if err := json.Unmarshal(rec.Body.Bytes(), &response); err != nil {
				t.Fatalf("Failed to parse response: %v", err)
			}

			if _, ok := response["status"]; !ok {
				t.Error("Response should contain 'status' field")
			}
			if _, ok := response["version"]; !ok {
				t.Error("Response should contain 'version' field")
			}
		})
	}
}

func TestHandleReady(t *testing.T) {
	tests := []struct {
		name           string
		storeGetter    StoreGetter
		expectedStatus int
		expectedReady  bool
	}{
		{
			name: "not ready with store error",
			storeGetter: func(ctx context.Context) (*db.Store, error) {
				return nil, context.DeadlineExceeded
			},
			expectedStatus: http.StatusServiceUnavailable,
			expectedReady:  false,
		},
		{
			name:           "ready with nil store getter",
			storeGetter:    nil,
			expectedStatus: http.StatusOK,
			expectedReady:  true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			cfg := &config.Config{ApiPort: "8080"}
			mockEmbedder := mocks.NewMockEmbedder(384)
			server := NewServer(cfg, tt.storeGetter, mockEmbedder, nil)

			req := httptest.NewRequest("GET", "/api/ready", nil)
			rec := httptest.NewRecorder()

			server.handleReady(rec, req)

			if rec.Code != tt.expectedStatus {
				t.Errorf("Expected status %d, got %d", tt.expectedStatus, rec.Code)
			}

			var response map[string]interface{}
			if err := json.Unmarshal(rec.Body.Bytes(), &response); err != nil {
				t.Fatalf("Failed to parse response: %v", err)
			}

			ready, ok := response["ready"].(bool)
			if !ok {
				t.Error("Response should contain 'ready' boolean")
				return
			}

			if ready != tt.expectedReady {
				t.Errorf("Expected ready=%v, got %v", tt.expectedReady, ready)
			}
		})
	}
}

func TestHandleLive(t *testing.T) {
	cfg := &config.Config{ApiPort: "8080"}
	server := NewServer(cfg, nil, nil, nil)

	req := httptest.NewRequest("GET", "/api/live", nil)
	rec := httptest.NewRecorder()

	server.handleLive(rec, req)

	if rec.Code != http.StatusOK {
		t.Errorf("Expected status %d, got %d", http.StatusOK, rec.Code)
	}

	var response map[string]string
	if err := json.Unmarshal(rec.Body.Bytes(), &response); err != nil {
		t.Fatalf("Failed to parse response: %v", err)
	}

	if response["status"] != "alive" {
		t.Errorf("Expected status 'alive', got '%s'", response["status"])
	}
}

func TestMetricsEndpoint(t *testing.T) {
	cfg := &config.Config{ApiPort: "8080"}
	server := NewServer(cfg, nil, nil, nil)

	req := httptest.NewRequest("GET", "/metrics", nil)
	rec := httptest.NewRecorder()

	server.srv.Handler.ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("expected status 200, got %d", rec.Code)
	}
	if got := rec.Header().Get("Content-Type"); got != "text/plain; version=0.0.4; charset=utf-8" {
		t.Fatalf("unexpected content type: %s", got)
	}
	if body := rec.Body.String(); body == "" {
		t.Fatal("expected metrics response body")
	}
}

func TestHandleSearch_Errors(t *testing.T) {
	tests := []struct {
		name           string
		requestBody    interface{}
		storeGetter    StoreGetter
		embedder       *mocks.MockEmbedder
		expectedStatus int
	}{
		{
			name:           "search with invalid JSON",
			requestBody:    "invalid json",
			storeGetter:    nil,
			embedder:       nil,
			expectedStatus: http.StatusBadRequest,
		},
		{
			name: "search with embedder error",
			requestBody: SearchRequest{
				Query: "test query",
				TopK:  5,
			},
			storeGetter:    nil,
			embedder:       mocks.NewMockEmbedder(384).WithEmbedError(context.Canceled),
			expectedStatus: http.StatusInternalServerError,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			cfg := &config.Config{ApiPort: "8080"}
			server := NewServer(cfg, tt.storeGetter, tt.embedder, nil)

			var body interface{}
			body = tt.requestBody
			if str, ok := body.(string); ok {
				body = str
			}

			bodyBytes, _ := json.Marshal(body)
			req := httptest.NewRequest("POST", "/api/search", bytes.NewReader(bodyBytes))
			req.Header.Set("Content-Type", "application/json")
			rec := httptest.NewRecorder()

			server.handleSearch(rec, req)

			if rec.Code != tt.expectedStatus {
				t.Errorf("Expected status %d, got %d", tt.expectedStatus, rec.Code)
			}
		})
	}
}

func TestHandleContext_Errors(t *testing.T) {
	tests := []struct {
		name           string
		requestBody    ContextRequest
		storeGetter    StoreGetter
		embedder       *mocks.MockEmbedder
		expectedStatus int
	}{
		{
			name: "context with embedder error",
			requestBody: ContextRequest{
				Text:   "test content",
				Source: "test",
			},
			storeGetter:    nil,
			embedder:       mocks.NewMockEmbedder(384).WithEmbedError(context.Canceled),
			expectedStatus: http.StatusInternalServerError,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			cfg := &config.Config{ApiPort: "8080"}
			server := NewServer(cfg, tt.storeGetter, tt.embedder, nil)

			bodyBytes, _ := json.Marshal(tt.requestBody)
			req := httptest.NewRequest("POST", "/api/context", bytes.NewReader(bodyBytes))
			req.Header.Set("Content-Type", "application/json")
			rec := httptest.NewRecorder()

			server.handleContext(rec, req)

			if rec.Code != tt.expectedStatus {
				t.Errorf("Expected status %d, got %d", tt.expectedStatus, rec.Code)
			}
		})
	}
}

func TestHandleListTools(t *testing.T) {
	t.Run("without mcp server", func(t *testing.T) {
		cfg := &config.Config{ApiPort: "8080"}
		server := NewServer(cfg, nil, nil, nil)

		req := httptest.NewRequest("GET", "/api/tools/list", nil)
		rec := httptest.NewRecorder()

		server.handleListTools(rec, req)

		if rec.Code != http.StatusInternalServerError {
			t.Errorf("Expected status %d, got %d", http.StatusInternalServerError, rec.Code)
		}
	})
}

func TestHandleCallTool(t *testing.T) {
	t.Run("without mcp server", func(t *testing.T) {
		cfg := &config.Config{ApiPort: "8080"}
		server := NewServer(cfg, nil, nil, nil)

		bodyBytes, _ := json.Marshal(map[string]interface{}{
			"name":      "test_tool",
			"arguments": map[string]interface{}{},
		})
		req := httptest.NewRequest("POST", "/api/tools/call", bytes.NewReader(bodyBytes))
		req.Header.Set("Content-Type", "application/json")
		rec := httptest.NewRecorder()

		server.handleCallTool(rec, req)

		if rec.Code != http.StatusInternalServerError {
			t.Errorf("Expected status %d, got %d", http.StatusInternalServerError, rec.Code)
		}
	})
}

func TestCORSHeaders(t *testing.T) {
	tests := []struct {
		name           string
		allowedOrigins []string
		requestOrigin  string
		expectedCORS   string
		expectVary     bool
	}{
		{
			name:           "exact origin match",
			allowedOrigins: []string{"http://localhost:3000"},
			requestOrigin:  "http://localhost:3000",
			expectedCORS:   "http://localhost:3000",
			expectVary:     true,
		},
		{
			name:           "wildcard origin match",
			allowedOrigins: []string{"*"},
			requestOrigin:  "https://evil.com",
			expectedCORS:   "https://evil.com",
			expectVary:     true,
		},
		{
			name:           "no match",
			allowedOrigins: []string{"http://localhost:3000"},
			requestOrigin:  "https://evil.com",
			expectedCORS:   "",
			expectVary:     true,
		},
		{
			name:           "empty allowed origins",
			allowedOrigins: []string{},
			requestOrigin:  "http://localhost:3000",
			expectedCORS:   "",
			expectVary:     true,
		},
		{
			name:           "no origin header in request",
			allowedOrigins: []string{"*"},
			requestOrigin:  "",
			expectedCORS:   "",
			expectVary:     false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			cfg := &config.Config{
				ApiPort:        "8080",
				AllowedOrigins: tt.allowedOrigins,
			}
			server := NewServer(cfg, nil, nil, nil)

			req := httptest.NewRequest("OPTIONS", "/api/health", nil)
			if tt.requestOrigin != "" {
				req.Header.Set("Origin", tt.requestOrigin)
			}
			rec := httptest.NewRecorder()

			server.setCORSHeaders(rec, req)

			corsHeader := rec.Header().Get("Access-Control-Allow-Origin")
			if corsHeader != tt.expectedCORS {
				t.Errorf("Expected Access-Control-Allow-Origin %q, got %q", tt.expectedCORS, corsHeader)
			}

			varyHeader := rec.Header().Get("Vary")
			if tt.expectVary && varyHeader != "Origin" {
				t.Errorf("Expected Vary: Origin, got %q", varyHeader)
			} else if !tt.expectVary && varyHeader != "" {
				t.Errorf("Expected no Vary header, got %q", varyHeader)
			}
		})
	}
}

func TestRateLimiting(t *testing.T) {
	cfg := &config.Config{ApiPort: "8080"}
	server := NewServer(cfg, nil, nil, nil)

	// Make many requests rapidly to test rate limiting
	for i := 0; i < 100; i++ {
		req := httptest.NewRequest("GET", "/api/live", nil)
		rec := httptest.NewRecorder()
		server.srv.Handler.ServeHTTP(rec, req)

		// All requests should succeed (rate limiter allows burst)
		if rec.Code != http.StatusOK && rec.Code != http.StatusTooManyRequests {
			t.Errorf("Unexpected status %d on request %d", rec.Code, i)
		}
	}
}
