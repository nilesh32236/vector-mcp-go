package chat

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"sync"
	"time"

	"github.com/google/uuid"
	"github.com/nilesh32236/vector-mcp-go/internal/llm"
)

type Session struct {
	ID        string    `json:"id"`
	Title     string    `json:"title"`
	CreatedAt time.Time `json:"created_at"`
	UpdatedAt time.Time `json:"updated_at"`
}

type MessageHistory struct {
	Session
	Messages []llm.Message `json:"messages"`
}

type Store struct {
	baseDir string
	mu      sync.RWMutex
}

func NewStore(dataDir string) (*Store, error) {
	baseDir := filepath.Join(dataDir, ".gemini", "chats")
	if err := os.MkdirAll(baseDir, 0755); err != nil {
		return nil, fmt.Errorf("failed to create chat directory: %w", err)
	}
	return &Store{
		baseDir: baseDir,
	}, nil
}

func (s *Store) getFilePath(id string) string {
	return filepath.Join(s.baseDir, id+".json")
}

func (s *Store) CreateSession(title string) (Session, error) {
	s.mu.Lock()
	defer s.mu.Unlock()

	id := uuid.New().String()
	now := time.Now()

	sess := Session{
		ID:        id,
		Title:     title,
		CreatedAt: now,
		UpdatedAt: now,
	}

	hist := MessageHistory{
		Session:  sess,
		Messages: []llm.Message{},
	}

	data, err := json.MarshalIndent(hist, "", "  ")
	if err != nil {
		return Session{}, fmt.Errorf("failed to marshal new session: %w", err)
	}

	if err := os.WriteFile(s.getFilePath(id), data, 0644); err != nil {
		return Session{}, fmt.Errorf("failed to write session file: %w", err)
	}

	return sess, nil
}

func (s *Store) GetSession(id string) (MessageHistory, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()

	data, err := os.ReadFile(s.getFilePath(id))
	if err != nil {
		if os.IsNotExist(err) {
			return MessageHistory{}, fmt.Errorf("session not found")
		}
		return MessageHistory{}, fmt.Errorf("failed to read session file: %w", err)
	}

	var hist MessageHistory
	if err := json.Unmarshal(data, &hist); err != nil {
		return MessageHistory{}, fmt.Errorf("failed to unmarshal session: %w", err)
	}

	return hist, nil
}

func (s *Store) ListSessions() ([]Session, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()

	entries, err := os.ReadDir(s.baseDir)
	if err != nil {
		return nil, fmt.Errorf("failed to read chat directory: %w", err)
	}

	var sessions []Session
	for _, entry := range entries {
		if entry.IsDir() || filepath.Ext(entry.Name()) != ".json" {
			continue
		}

		data, err := os.ReadFile(filepath.Join(s.baseDir, entry.Name()))
		if err != nil {
			continue // Skip unreadable files
		}

		var hist MessageHistory
		if err := json.Unmarshal(data, &hist); err != nil {
			continue // Skip malformed files
		}

		sessions = append(sessions, hist.Session)
	}

	return sessions, nil
}

func (s *Store) DeleteSession(id string) error {
	s.mu.Lock()
	defer s.mu.Unlock()

	err := os.Remove(s.getFilePath(id))
	if err != nil {
		if os.IsNotExist(err) {
			return nil // Already deleted
		}
		return fmt.Errorf("failed to delete session: %w", err)
	}
	return nil
}

func (s *Store) AppendMessage(id string, msg llm.Message) error {
	s.mu.Lock()
	defer s.mu.Unlock()

	path := s.getFilePath(id)
	data, err := os.ReadFile(path)
	if err != nil {
		return fmt.Errorf("failed to read session file: %w", err)
	}

	var hist MessageHistory
	if err := json.Unmarshal(data, &hist); err != nil {
		return fmt.Errorf("failed to unmarshal session: %w", err)
	}

	hist.Messages = append(hist.Messages, msg)
	hist.UpdatedAt = time.Now()

	newData, err := json.MarshalIndent(hist, "", "  ")
	if err != nil {
		return fmt.Errorf("failed to marshal updated session: %w", err)
	}

	if err := os.WriteFile(path, newData, 0644); err != nil {
		return fmt.Errorf("failed to write updated session file: %w", err)
	}

	return nil
}
