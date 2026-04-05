// Package ratelimit provides rate limiting utilities for protecting API endpoints
// from abuse and ensuring fair resource allocation.
package ratelimit

import (
	"context"
	"net/http"
	"sync"
	"time"
)

// TokenBucket implements a thread-safe token bucket rate limiter.
type TokenBucket struct {
	rate       float64       // Tokens added per second
	burst      int           // Maximum tokens (bucket size)
	tokens     float64       // Current token count
	lastUpdate time.Time     // Last time tokens were updated
	mu         sync.Mutex    // Protects concurrent access
}

// NewTokenBucket creates a new token bucket rate limiter.
// rate: tokens added per second (requests per second)
// burst: maximum tokens (allows temporary burst up to this many requests)
func NewTokenBucket(rate float64, burst int) *TokenBucket {
	return &TokenBucket{
		rate:       rate,
		burst:      burst,
		tokens:     float64(burst), // Start full
		lastUpdate: time.Now(),
	}
}

// Allow attempts to take one token from the bucket.
// Returns true if successful, false if rate limited.
func (tb *TokenBucket) Allow() bool {
	return tb.AllowN(1)
}

// AllowN attempts to take n tokens from the bucket.
// Returns true if successful, false if rate limited.
func (tb *TokenBucket) AllowN(n int) bool {
	tb.mu.Lock()
	defer tb.mu.Unlock()

	// Update tokens based on time elapsed
	now := time.Now()
	elapsed := now.Sub(tb.lastUpdate).Seconds()
	tb.tokens += elapsed * tb.rate
	if tb.tokens > float64(tb.burst) {
		tb.tokens = float64(tb.burst)
	}
	tb.lastUpdate = now

	// Check if we have enough tokens
	if tb.tokens >= float64(n) {
		tb.tokens -= float64(n)
		return true
	}
	return false
}

// Wait blocks until a token is available or context is cancelled.
// Returns nil if token acquired, context error if cancelled.
func (tb *TokenBucket) Wait(ctx context.Context) error {
	return tb.WaitN(ctx, 1)
}

// WaitN blocks until n tokens are available or context is cancelled.
func (tb *TokenBucket) WaitN(ctx context.Context, n int) error {
	for {
		if tb.AllowN(n) {
			return nil
		}

		// Calculate wait time
		tb.mu.Lock()
		deficit := float64(n) - tb.tokens
		waitTime := time.Duration(deficit/tb.rate) * time.Second
		tb.mu.Unlock()

		// Don't wait too long at once
		if waitTime > 100*time.Millisecond {
			waitTime = 100 * time.Millisecond
		}

		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-time.After(waitTime):
			continue
		}
	}
}

// Remaining returns the current number of available tokens.
func (tb *TokenBucket) Remaining() float64 {
	tb.mu.Lock()
	defer tb.mu.Unlock()

	// Update tokens before returning
	now := time.Now()
	elapsed := now.Sub(tb.lastUpdate).Seconds()
	tokens := tb.tokens + elapsed*tb.rate
	if tokens > float64(tb.burst) {
		tokens = float64(tb.burst)
	}
	return tokens
}

// Reset resets the bucket to full capacity.
func (tb *TokenBucket) Reset() {
	tb.mu.Lock()
	defer tb.mu.Unlock()
	tb.tokens = float64(tb.burst)
	tb.lastUpdate = time.Now()
}

// KeyFunc extracts a rate limit key from an HTTP request.
// Common implementations use IP address, API key, or user ID.
type KeyFunc func(r *http.Request) string

// DefaultKeyFunc uses the client IP address as the rate limit key.
func DefaultKeyFunc(r *http.Request) string {
	// Check X-Forwarded-For header first (for proxies)
	if xff := r.Header.Get("X-Forwarded-For"); xff != "" {
		// Take the first IP in the chain
		for i, c := range xff {
			if c == ',' {
				return xff[:i]
			}
		}
		return xff
	}

	// Fall back to RemoteAddr
	return r.RemoteAddr
}

// Middleware provides HTTP middleware for rate limiting.
type Middleware struct {
	buckets   map[string]*TokenBucket
	rate      float64
	burst     int
	keyFunc   KeyFunc
	mu        sync.RWMutex
	onLimited http.HandlerFunc // Handler for rate limited requests
}

// MiddlewareOption configures the rate limit middleware.
type MiddlewareOption func(*Middleware)

// WithRate sets the rate (requests per second).
func WithRate(rate float64) MiddlewareOption {
	return func(m *Middleware) {
		m.rate = rate
	}
}

// WithBurst sets the burst capacity.
func WithBurst(burst int) MiddlewareOption {
	return func(m *Middleware) {
		m.burst = burst
	}
}

// WithKeyFunc sets the key extraction function.
func WithKeyFunc(fn KeyFunc) MiddlewareOption {
	return func(m *Middleware) {
		m.keyFunc = fn
	}
}

// WithLimitedHandler sets a custom handler for rate limited requests.
func WithLimitedHandler(handler http.HandlerFunc) MiddlewareOption {
	return func(m *Middleware) {
		m.onLimited = handler
	}
}

// NewMiddleware creates a new rate limiting middleware.
func NewMiddleware(opts ...MiddlewareOption) *Middleware {
	m := &Middleware{
		buckets: make(map[string]*TokenBucket),
		rate:    10,  // Default: 10 requests per second
		burst:   20,  // Default: allow burst of 20
		keyFunc: DefaultKeyFunc,
		onLimited: func(w http.ResponseWriter, r *http.Request) {
			w.Header().Set("Retry-After", "1")
			http.Error(w, `{"error":"rate limit exceeded","retry_after":1}`, http.StatusTooManyRequests)
		},
	}

	for _, opt := range opts {
		opt(m)
	}

	return m
}

// Handler wraps an http.Handler with rate limiting.
func (m *Middleware) Handler(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		key := m.keyFunc(r)

		m.mu.RLock()
		bucket, exists := m.buckets[key]
		m.mu.RUnlock()

		if !exists {
			m.mu.Lock()
			// Double-check after acquiring write lock
			bucket, exists = m.buckets[key]
			if !exists {
				bucket = NewTokenBucket(m.rate, m.burst)
				m.buckets[key] = bucket
			}
			m.mu.Unlock()
		}

		if !bucket.Allow() {
			m.onLimited(w, r)
			return
		}

		next.ServeHTTP(w, r)
	})
}

// Cleanup removes buckets that haven't been used recently.
// Call this periodically to prevent memory leaks.
func (m *Middleware) Cleanup(maxAge time.Duration) {
	m.mu.Lock()
	defer m.mu.Unlock()

	now := time.Now()
	for key, bucket := range m.buckets {
		bucket.mu.Lock()
		age := now.Sub(bucket.lastUpdate)
		bucket.mu.Unlock()

		if age > maxAge {
			delete(m.buckets, key)
		}
	}
}

// Stats returns statistics about the rate limiter.
type Stats struct {
	TotalBuckets int
	TotalTokens  float64
}

// GetStats returns current statistics.
func (m *Middleware) GetStats() Stats {
	m.mu.RLock()
	defer m.mu.RUnlock()

	stats := Stats{
		TotalBuckets: len(m.buckets),
	}

	for _, bucket := range m.buckets {
		stats.TotalTokens += bucket.Remaining()
	}

	return stats
}

// PerClientRateLimit creates a middleware that limits requests per client IP.
// rate: requests per second per client
// burst: maximum burst per client
func PerClientRateLimit(rate float64, burst int) *Middleware {
	return NewMiddleware(
		WithRate(rate),
		WithBurst(burst),
		WithKeyFunc(DefaultKeyFunc),
	)
}

// GlobalRateLimit creates a middleware that limits total requests across all clients.
// rate: total requests per second
// burst: maximum burst
func GlobalRateLimit(rate float64, burst int) *Middleware {
	return NewMiddleware(
		WithKeyFunc(func(r *http.Request) string {
			return "global" // Same key for all requests
		}),
		WithRate(rate),
		WithBurst(burst),
	)
}

// APIKeyRateLimit creates a middleware that limits requests per API key.
// Looks for API key in X-API-Key header.
// Falls back to IP-based limiting if no API key is present.
func APIKeyRateLimit(rate float64, burst int) *Middleware {
	return NewMiddleware(
		WithRate(rate),
		WithBurst(burst),
		WithKeyFunc(func(r *http.Request) string {
			if apiKey := r.Header.Get("X-API-Key"); apiKey != "" {
				return "apikey:" + apiKey
			}
			return "ip:" + DefaultKeyFunc(r)
		}),
	)
}
