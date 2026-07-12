package servers

import (
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"
)

type persistedSession struct {
	ID             string             `json:"id"`
	Title          string             `json:"title"`
	Model          string             `json:"model"`
	Source         string             `json:"source,omitempty"`
	ConversationID string             `json:"conversation_id"`
	Preview        string             `json:"preview,omitempty"`
	MessageCount   int                `json:"message_count,omitempty"`
	Messages       []persistedMessage `json:"messages"`
	UpdatedAt      time.Time          `json:"updated_at"`
}

type persistedMessage struct {
	Role    string `json:"role"`
	Content string `json:"content"`
}

func toPersistedMessages(messages []chatMessage) []persistedMessage {
	result := make([]persistedMessage, 0, len(messages))
	for _, message := range messages {
		result = append(result, persistedMessage{Role: message.role, Content: message.content})
	}
	return result
}

func fromPersistedMessages(messages []persistedMessage) []chatMessage {
	result := make([]chatMessage, 0, len(messages))
	for _, message := range messages {
		result = append(result, chatMessage{role: message.Role, content: message.Content})
	}
	return result
}

type sessionStore struct {
	dir string
}

func newSessionStore(dir string) sessionStore {
	return sessionStore{dir: dir}
}

func defaultSessionStore() sessionStore {
	if dir := os.Getenv("M365_BRIDGE_SESSION_DIR"); dir != "" {
		return newSessionStore(dir)
	}
	configDir, err := os.UserConfigDir()
	if err != nil || configDir == "" {
		return newSessionStore(filepath.Join(".", ".m365bridge", "sessions"))
	}
	return newSessionStore(filepath.Join(configDir, "m365bridge", "sessions"))
}

func (s sessionStore) Save(session persistedSession) error {
	if strings.TrimSpace(session.ID) == "" {
		return errors.New("session ID is required")
	}
	if session.UpdatedAt.IsZero() {
		session.UpdatedAt = time.Now().UTC()
	}
	if err := os.MkdirAll(s.dir, 0700); err != nil {
		return fmt.Errorf("create session directory: %w", err)
	}

	data, err := json.MarshalIndent(session, "", "  ")
	if err != nil {
		return fmt.Errorf("marshal session: %w", err)
	}
	path := filepath.Join(s.dir, session.ID+".json")
	tmp, err := os.CreateTemp(s.dir, ".session-*.tmp")
	if err != nil {
		return fmt.Errorf("create session temp file: %w", err)
	}
	tmpName := tmp.Name()
	defer os.Remove(tmpName)
	if err := tmp.Chmod(0600); err != nil {
		_ = tmp.Close()
		return fmt.Errorf("secure session temp file: %w", err)
	}
	if _, err := tmp.Write(data); err != nil {
		_ = tmp.Close()
		return fmt.Errorf("write session: %w", err)
	}
	if err := tmp.Close(); err != nil {
		return fmt.Errorf("close session temp file: %w", err)
	}
	if err := os.Rename(tmpName, path); err != nil {
		return fmt.Errorf("replace session file: %w", err)
	}
	return nil
}

func (s sessionStore) Load(id string) (persistedSession, error) {
	data, err := os.ReadFile(filepath.Join(s.dir, id+".json"))
	if err != nil {
		return persistedSession{}, fmt.Errorf("read session: %w", err)
	}
	var session persistedSession
	if err := json.Unmarshal(data, &session); err != nil {
		return persistedSession{}, fmt.Errorf("parse session: %w", err)
	}
	return session, nil
}

func (s sessionStore) List() ([]persistedSession, error) {
	entries, err := os.ReadDir(s.dir)
	if errors.Is(err, os.ErrNotExist) {
		return []persistedSession{}, nil
	}
	if err != nil {
		return nil, fmt.Errorf("read session directory: %w", err)
	}

	sessions := make([]persistedSession, 0, len(entries))
	for _, entry := range entries {
		if entry.IsDir() || filepath.Ext(entry.Name()) != ".json" {
			continue
		}
		session, err := s.Load(strings.TrimSuffix(entry.Name(), ".json"))
		if err != nil || strings.TrimSpace(session.ID) == "" {
			continue
		}
		sessions = append(sessions, session)
	}
	sort.SliceStable(sessions, func(i, j int) bool {
		return sessions[i].UpdatedAt.After(sessions[j].UpdatedAt)
	})
	return sessions, nil
}

func (s sessionStore) Delete(id string) error {
	err := os.Remove(filepath.Join(s.dir, id+".json"))
	if errors.Is(err, os.ErrNotExist) {
		return nil
	}
	if err != nil {
		return fmt.Errorf("delete session: %w", err)
	}
	return nil
}
