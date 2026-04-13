package ratelimit

import (
	"context"
	"net/http"
	"net/http/httptest"
	"sync"
	"testing"
	"time"
)

const DefaultTestRemoteAddr = "192.0.2.1:1234"

func TestTokenBucket_Allow(t *testing.T) {
	tb := NewTokenBucket(1, 3) // 1 request/second, burst of 3

	// Should allow burst of 3 immediately
	for i := 0; i < 3; i++ {
		if !tb.Allow() {
			t.Errorf("expected Allow() to return true for request %d", i+1)
		}
	}

	// Should be rate limited
	if tb.Allow() {
		t.Error("expected Allow() to return false after burst exhausted")
	}
}

func TestTokenBucket_Refill(t *testing.T) {
	tb := NewTokenBucket(10, 2) // 10 requests/second, burst of 2

	// Use all tokens
	tb.Allow()
	tb.Allow()

	// Should be empty
	if tb.Allow() {
		t.Error("expected bucket to be empty")
	}

	// Wait for refill (100ms should give us 1 token at 10/s)
	time.Sleep(110 * time.Millisecond)

	// Should have 1 token now
	if !tb.Allow() {
		t.Error("expected bucket to have refilled 1 token")
	}

	// But not 2 tokens yet
	if tb.Allow() {
		t.Error("expected bucket to not have 2 tokens yet")
	}
}

func TestTokenBucket_Remaining(t *testing.T) {
	tb := NewTokenBucket(1, 5)

	remaining := tb.Remaining()
	if remaining < 4.9 || remaining > 5.1 {
		t.Errorf("expected remaining to be ~5.0, got %f", remaining)
	}

	tb.Allow()

	remaining = tb.Remaining()
	if remaining < 3.9 || remaining > 4.1 {
		t.Errorf("expected remaining to be ~4.0, got %f", remaining)
	}
}

func TestTokenBucket_Reset(t *testing.T) {
	tb := NewTokenBucket(1, 5)

	// Use some tokens
	tb.Allow()
	tb.Allow()

	// Reset
	tb.Reset()

	remaining := tb.Remaining()
	if remaining < 4.9 || remaining > 5.0 {
		t.Errorf("expected remaining to be ~5.0 after reset, got %f", remaining)
	}
}

func TestTokenBucket_Wait(t *testing.T) {
	tb := NewTokenBucket(100, 1) // 100/s, burst 1

	// Use the token
	tb.Allow()

	// Wait should block until token available
	start := time.Now()
	ctx := context.Background()
	err := tb.Wait(ctx)
	elapsed := time.Since(start)

	if err != nil {
		t.Errorf("unexpected error: %v", err)
	}
	// Should have waited ~10ms for refill at 100/s
	if elapsed < 5*time.Millisecond {
		t.Errorf("expected to wait at least 5ms, waited %v", elapsed)
	}
}

func TestTokenBucket_WaitContextCancel(t *testing.T) {
	tb := NewTokenBucket(1, 0) // Very slow, no burst

	ctx, cancel := context.WithTimeout(context.Background(), 50*time.Millisecond)
	defer cancel()

	start := time.Now()
	err := tb.Wait(ctx)
	elapsed := time.Since(start)

	if err != context.DeadlineExceeded {
		t.Errorf("expected context.DeadlineExceeded, got %v", err)
	}
	if elapsed > 100*time.Millisecond {
		t.Errorf("should have returned quickly after context cancel, took %v", elapsed)
	}
}

func TestTokenBucket_Concurrent(t *testing.T) {
	// Extremely low refill rate to ensure deterministic burst depletion
	tb := NewTokenBucket(0.0001, 100)

	var wg sync.WaitGroup
	successCount := make(chan bool, 200)

	for i := 0; i < 200; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			successCount <- tb.Allow()
		}()
	}

	wg.Wait()
	close(successCount)

	allowed := 0
	denied := 0
	for ok := range successCount {
		if ok {
			allowed++
		} else {
			denied++
		}
	}

	// Initial burst of 100 should all succeed
	if allowed != 100 {
		t.Errorf("expected 100 allowed, got %d", allowed)
	}
	if denied != 100 {
		t.Errorf("expected 100 denied, got %d", denied)
	}
}

func TestMiddleware_Handler(t *testing.T) {
	middleware := NewMiddleware(
		WithRate(1),
		WithBurst(2),
	)

	handler := middleware.Handler(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte("OK"))
	}))

	// First 2 requests should succeed
	for i := 0; i < 2; i++ {
		req := httptest.NewRequest("GET", "/", nil)
		req.RemoteAddr = DefaultTestRemoteAddr
		rec := httptest.NewRecorder()
		handler.ServeHTTP(rec, req)

		if rec.Code != http.StatusOK {
			t.Errorf("request %d: expected 200, got %d", i+1, rec.Code)
		}
	}

	// Third request should be rate limited
	req := httptest.NewRequest("GET", "/", nil)
	req.RemoteAddr = DefaultTestRemoteAddr
	rec := httptest.NewRecorder()
	handler.ServeHTTP(rec, req)

	if rec.Code != http.StatusTooManyRequests {
		t.Errorf("expected 429, got %d", rec.Code)
	}
}

func TestMiddleware_DifferentClients(t *testing.T) {
	middleware := NewMiddleware(
		WithRate(1),
		WithBurst(1),
	)

	handler := middleware.Handler(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
	}))

	// First client
	req1 := httptest.NewRequest("GET", "/", nil)
	req1.RemoteAddr = DefaultTestRemoteAddr
	rec1 := httptest.NewRecorder()
	handler.ServeHTTP(rec1, req1)

	if rec1.Code != http.StatusOK {
		t.Errorf("client 1 first request: expected 200, got %d", rec1.Code)
	}

	// Second client (should have separate limit)
	req2 := httptest.NewRequest("GET", "/", nil)
	req2.RemoteAddr = "192.0.2.2:1234"
	rec2 := httptest.NewRecorder()
	handler.ServeHTTP(rec2, req2)

	if rec2.Code != http.StatusOK {
		t.Errorf("client 2 first request: expected 200, got %d", rec2.Code)
	}
}

func TestMiddleware_XForwardedFor(t *testing.T) {
	middleware := NewMiddleware(
		WithRate(1),
		WithBurst(1),
	)

	handler := middleware.Handler(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
	}))

	// Request with X-Forwarded-For
	req := httptest.NewRequest("GET", "/", nil)
	req.Header.Set("X-Forwarded-For", "203.0.113.1, 198.51.100.1")
	req.RemoteAddr = DefaultTestRemoteAddr
	rec := httptest.NewRecorder()
	handler.ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Errorf("expected 200, got %d", rec.Code)
	}

	// Same XFF should be rate limited
	req2 := httptest.NewRequest("GET", "/", nil)
	req2.Header.Set("X-Forwarded-For", "203.0.113.1, 198.51.100.1")
	req2.RemoteAddr = DefaultTestRemoteAddr
	rec2 := httptest.NewRecorder()
	handler.ServeHTTP(rec2, req2)

	if rec2.Code != http.StatusTooManyRequests {
		t.Errorf("expected 429 for same XFF, got %d", rec2.Code)
	}
}

func TestMiddleware_Cleanup(t *testing.T) {
	middleware := NewMiddleware(
		WithRate(1),
		WithBurst(1),
	)

	// Create some buckets
	for i := 0; i < 5; i++ {
		req := httptest.NewRequest("GET", "/", nil)
		req.RemoteAddr = "192.0.2." + string(rune('1'+i)) + ":1234"
		rec := httptest.NewRecorder()
		handler := middleware.Handler(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {}))
		handler.ServeHTTP(rec, req)
	}

	stats := middleware.GetStats()
	if stats.TotalBuckets != 5 {
		t.Errorf("expected 5 buckets, got %d", stats.TotalBuckets)
	}

	// Wait and cleanup
	time.Sleep(10 * time.Millisecond)
	middleware.Cleanup(5 * time.Millisecond)

	stats = middleware.GetStats()
	if stats.TotalBuckets != 0 {
		t.Errorf("expected 0 buckets after cleanup, got %d", stats.TotalBuckets)
	}
}

func TestPerClientRateLimit(t *testing.T) {
	middleware := PerClientRateLimit(1, 2)
	if middleware == nil {
		t.Fatal("expected non-nil middleware")
	}

	handler := middleware.Handler(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
	}))

	// Should allow burst
	for i := 0; i < 2; i++ {
		req := httptest.NewRequest("GET", "/", nil)
		req.RemoteAddr = DefaultTestRemoteAddr
		rec := httptest.NewRecorder()
		handler.ServeHTTP(rec, req)

		if rec.Code != http.StatusOK {
			t.Errorf("request %d: expected 200, got %d", i+1, rec.Code)
		}
	}
}

func TestGlobalRateLimit(t *testing.T) {
	middleware := GlobalRateLimit(1, 2)
	if middleware == nil {
		t.Fatal("expected non-nil middleware")
	}

	handler := middleware.Handler(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
	}))

	// Two requests from different IPs, but shared global limit
	for i := 0; i < 2; i++ {
		req := httptest.NewRequest("GET", "/", nil)
		req.RemoteAddr = "192.0.2." + string(rune('1'+i)) + ":1234"
		rec := httptest.NewRecorder()
		handler.ServeHTTP(rec, req)

		if rec.Code != http.StatusOK {
			t.Errorf("request %d: expected 200, got %d", i+1, rec.Code)
		}
	}

	// Third request should be denied even from different IP
	req := httptest.NewRequest("GET", "/", nil)
	req.RemoteAddr = "192.0.2.99:1234"
	rec := httptest.NewRecorder()
	handler.ServeHTTP(rec, req)

	if rec.Code != http.StatusTooManyRequests {
		t.Errorf("expected 429, got %d", rec.Code)
	}
}

func TestAPIKeyRateLimit(t *testing.T) {
	middleware := APIKeyRateLimit(1, 1)
	handler := middleware.Handler(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
	}))

	// Request with API key
	req := httptest.NewRequest("GET", "/", nil)
	req.Header.Set("X-API-Key", "test-key-123")
	req.RemoteAddr = DefaultTestRemoteAddr
	rec := httptest.NewRecorder()
	handler.ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Errorf("expected 200, got %d", rec.Code)
	}

	// Same API key should be rate limited
	req2 := httptest.NewRequest("GET", "/", nil)
	req2.Header.Set("X-API-Key", "test-key-123")
	req2.RemoteAddr = "192.0.2.2:1234" // Different IP, same key
	rec2 := httptest.NewRecorder()
	handler.ServeHTTP(rec2, req2)

	if rec2.Code != http.StatusTooManyRequests {
		t.Errorf("expected 429 for same API key, got %d", rec2.Code)
	}

	// Different API key should work
	req3 := httptest.NewRequest("GET", "/", nil)
	req3.Header.Set("X-API-Key", "different-key")
	req3.RemoteAddr = DefaultTestRemoteAddr
	rec3 := httptest.NewRecorder()
	handler.ServeHTTP(rec3, req3)

	if rec3.Code != http.StatusOK {
		t.Errorf("expected 200 for different API key, got %d", rec3.Code)
	}
}
