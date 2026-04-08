package watcher

import (
	"context"
	"log/slog"
	"os"
	"testing"
	"time"

	"github.com/nilesh32236/vector-mcp-go/internal/config"
)

func TestNewFileWatcher(t *testing.T) {
	cfg := &config.Config{
		ProjectRoot: "/test/path",
	}

	// Note: fsnotify.NewWatcher may fail in some environments
	watcher, err := NewFileWatcher(cfg, nil, nil, nil, nil, nil, nil, nil)

	// In most environments this should succeed
	if err != nil {
		// Skip if watcher creation fails (e.g., in containerized environments)
		t.Skipf("Could not create file watcher: %v", err)
	}

	if watcher == nil {
		t.Fatal("Expected non-nil FileWatcher")
	}

	if watcher.cfg != cfg {
		t.Error("Config not set correctly")
	}

	if watcher.eventChan == nil {
		t.Error("Event channel should be initialized")
	}

	// Cleanup
	_ = watcher.tracker.Close()
}

func TestFileWatcher_EventChannel(t *testing.T) {
	cfg := &config.Config{
		ProjectRoot: "/test/path",
	}

	watcher, err := NewFileWatcher(cfg, nil, nil, nil, nil, nil, nil, nil)
	if err != nil {
		t.Skipf("Could not create file watcher: %v", err)
	}
	defer func() { _ = watcher.tracker.Close() }()

	// Verify event channel is buffered
	if cap(watcher.eventChan) != 1000 {
		t.Errorf("Expected event channel buffer size 1000, got %d", cap(watcher.eventChan))
	}
}

func TestFileWatcher_ResetChan(t *testing.T) {
	cfg := &config.Config{
		ProjectRoot: "/test/path",
	}
	resetChan := make(chan string, 1)

	watcher, err := NewFileWatcher(cfg, nil, resetChan, nil, nil, nil, nil, nil)
	if err != nil {
		t.Skipf("Could not create file watcher: %v", err)
	}
	defer func() { _ = watcher.tracker.Close() }()

	if watcher.watcherResetChan == nil {
		t.Error("Watcher reset channel should be set")
	}

	// Test sending to reset channel
	select {
	case watcher.watcherResetChan <- "/new/path":
		// Success
	default:
		t.Error("Should be able to send to reset channel")
	}
}

func TestFileWatcher_ContextCancellation(t *testing.T) {
	cfg := &config.Config{
		ProjectRoot: "/test/path",
	}

	logger := slog.New(slog.NewTextHandler(os.Stderr, nil))

	watcher, err := NewFileWatcher(cfg, logger, nil, nil, nil, nil, nil, nil)
	if err != nil {
		t.Skipf("Could not create file watcher: %v", err)
	}
	defer func() { _ = watcher.tracker.Close() }()

	ctx, cancel := context.WithCancel(context.Background())

	// Start the watcher in a goroutine
	done := make(chan struct{})
	go func() {
		watcher.Start(ctx)
		close(done)
	}()

	// Cancel the context
	cancel()

	// Wait for the watcher to stop
	select {
	case <-done:
		// Success - watcher stopped
	case <-time.After(2 * time.Second):
		t.Error("Watcher did not stop after context cancellation")
	}
}
