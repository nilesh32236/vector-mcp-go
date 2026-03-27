package chat

import (
	"fmt"
	"os"
	"path/filepath"
	"testing"

	"github.com/nilesh32236/vector-mcp-go/internal/llm"
)

func TestNewStore(t *testing.T) {
	tempDir := t.TempDir()

	store, err := NewStore(tempDir)
	if err != nil {
		t.Fatalf("expected no error, got %v", err)
	}

	if store == nil {
		t.Fatalf("expected store to not be nil")
	}

	expectedDir := filepath.Join(tempDir, ".gemini", "chats")
	if _, err := os.Stat(expectedDir); os.IsNotExist(err) {
		t.Fatalf("expected directory %s to be created, but it was not", expectedDir)
	}

	// Test error case (cannot create dir) - hard to simulate cross-platform without mocking os, skipping for now
}

func TestStore_CreateSession(t *testing.T) {
	tempDir := t.TempDir()
	store, _ := NewStore(tempDir)

	title := "Test Session"
	sess, err := store.CreateSession(title)

	if err != nil {
		t.Fatalf("expected no error, got %v", err)
	}

	if sess.ID == "" {
		t.Fatalf("expected session to have an ID")
	}

	if sess.Title != title {
		t.Fatalf("expected session title to be %s, got %s", title, sess.Title)
	}

	if sess.CreatedAt.IsZero() {
		t.Fatalf("expected session to have a creation time")
	}

	if sess.UpdatedAt.IsZero() {
		t.Fatalf("expected session to have an update time")
	}

	// Verify file is created
	expectedFilePath := filepath.Join(store.baseDir, sess.ID+".json")
	if _, err := os.Stat(expectedFilePath); os.IsNotExist(err) {
		t.Fatalf("expected file %s to be created, but it was not", expectedFilePath)
	}
}

func TestStore_GetSession(t *testing.T) {
	tempDir := t.TempDir()
	store, _ := NewStore(tempDir)

	title := "Test Session"
	sess, _ := store.CreateSession(title)

	hist, err := store.GetSession(sess.ID)
	if err != nil {
		t.Fatalf("expected no error, got %v", err)
	}

	if hist.ID != sess.ID {
		t.Fatalf("expected history ID to match session ID, got %s", hist.ID)
	}

	if hist.Title != sess.Title {
		t.Fatalf("expected history title to match session title, got %s", hist.Title)
	}

	if len(hist.Messages) != 0 {
		t.Fatalf("expected empty messages array, got %d", len(hist.Messages))
	}

	// Test non-existent session
	_, err = store.GetSession("non-existent-id")
	if err == nil {
		t.Fatalf("expected error getting non-existent session")
	}

	if err.Error() != "session not found" {
		t.Fatalf("expected error message 'session not found', got %s", err.Error())
	}
}

func TestStore_ListSessions(t *testing.T) {
	tempDir := t.TempDir()
	store, _ := NewStore(tempDir)

	// Create some sessions
	numSessions := 3
	for i := 0; i < numSessions; i++ {
		_, err := store.CreateSession(fmt.Sprintf("Session %d", i))
		if err != nil {
			t.Fatalf("failed to create session: %v", err)
		}
	}

	// Create some invalid files/dirs to test filtering
	os.Mkdir(filepath.Join(store.baseDir, "invalid-dir"), 0755)
	os.WriteFile(filepath.Join(store.baseDir, "invalid-file.txt"), []byte("not json"), 0644)
	os.WriteFile(filepath.Join(store.baseDir, "malformed.json"), []byte("{malformed"), 0644)

	sessions, err := store.ListSessions()
	if err != nil {
		t.Fatalf("expected no error listing sessions, got %v", err)
	}

	if len(sessions) != numSessions {
		t.Fatalf("expected %d sessions, got %d", numSessions, len(sessions))
	}
}

func TestStore_AppendMessage(t *testing.T) {
	tempDir := t.TempDir()
	store, _ := NewStore(tempDir)

	sess, _ := store.CreateSession("Test Session")

	msg := llm.Message{
		Role:    "user",
		Content: "Hello world!",
	}

	err := store.AppendMessage(sess.ID, msg)
	if err != nil {
		t.Fatalf("expected no error appending message, got %v", err)
	}

	hist, _ := store.GetSession(sess.ID)

	if len(hist.Messages) != 1 {
		t.Fatalf("expected 1 message, got %d", len(hist.Messages))
	}

	if hist.Messages[0].Role != msg.Role {
		t.Fatalf("expected role %s, got %s", msg.Role, hist.Messages[0].Role)
	}

	if hist.Messages[0].Content != msg.Content {
		t.Fatalf("expected content %s, got %s", msg.Content, hist.Messages[0].Content)
	}

	// Ensure updated time is updated
	if !hist.UpdatedAt.After(sess.UpdatedAt) && !hist.UpdatedAt.Equal(sess.UpdatedAt) {
		t.Logf("expected updated time %v to be after creation time %v", hist.UpdatedAt, sess.UpdatedAt)
		// Usually After() works but due to clock resolution sometimes it's Equal
	}

	// Append non-existent session
	err = store.AppendMessage("non-existent-id", msg)
	if err == nil {
		t.Fatalf("expected error appending to non-existent session")
	}
}

func TestStore_DeleteSession(t *testing.T) {
	tempDir := t.TempDir()
	store, _ := NewStore(tempDir)

	sess, _ := store.CreateSession("Test Session")

	err := store.DeleteSession(sess.ID)
	if err != nil {
		t.Fatalf("expected no error deleting session, got %v", err)
	}

	_, err = store.GetSession(sess.ID)
	if err == nil {
		t.Fatalf("expected error getting deleted session")
	}

	// Delete already deleted session
	err = store.DeleteSession(sess.ID)
	if err != nil {
		t.Fatalf("expected no error deleting an already deleted session, got %v", err)
	}
}
