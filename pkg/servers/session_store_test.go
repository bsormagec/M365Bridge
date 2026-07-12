package servers

import (
	"os"
	"path/filepath"
	"testing"
	"time"
)

func TestSessionStoreSavesListsLoadsAndDeletes(t *testing.T) {
	dir := t.TempDir()
	store := newSessionStore(dir)
	want := persistedSession{
		ID:             "session-1",
		Title:          "Hello world",
		Model:          "auto",
		ConversationID: "conversation-1",
		Messages: []persistedMessage{
			{Role: "You", Content: "Hello world"},
			{Role: "Copilot", Content: "Hi!"},
		},
		UpdatedAt: time.Now().UTC().Truncate(time.Second),
	}

	if err := store.Save(want); err != nil {
		t.Fatalf("Save() error = %v", err)
	}

	got, err := store.Load(want.ID)
	if err != nil {
		t.Fatalf("Load() error = %v", err)
	}
	if got.ID != want.ID || got.Title != want.Title || got.ConversationID != want.ConversationID || len(got.Messages) != 2 {
		t.Fatalf("Load() = %#v, want %#v", got, want)
	}
	if got.Messages[0].Role != "You" || got.Messages[0].Content != "Hello world" {
		t.Fatalf("Load() messages = %#v, want exported role/content", got.Messages)
	}

	items, err := store.List()
	if err != nil {
		t.Fatalf("List() error = %v", err)
	}
	if len(items) != 1 || items[0].ID != want.ID {
		t.Fatalf("List() = %#v, want one session %q", items, want.ID)
	}

	if err := store.Delete(want.ID); err != nil {
		t.Fatalf("Delete() error = %v", err)
	}
	items, err = store.List()
	if err != nil {
		t.Fatalf("List() after Delete() error = %v", err)
	}
	if len(items) != 0 {
		t.Fatalf("List() after Delete() = %#v, want empty", items)
	}
}

func TestSessionStoreRejectsUnsafeIDs(t *testing.T) {
	store := newSessionStore(t.TempDir())
	for _, id := range []string{"../escape", "/absolute", "nested/path", "", "."} {
		if err := store.Save(persistedSession{ID: id}); err == nil {
			t.Fatalf("Save(%q) accepted unsafe ID", id)
		}
		if _, err := store.Load(id); err == nil {
			t.Fatalf("Load(%q) accepted unsafe ID", id)
		}
		if err := store.Delete(id); err == nil {
			t.Fatalf("Delete(%q) accepted unsafe ID", id)
		}
	}
	if err := validateSessionID("safe_session-123"); err != nil {
		t.Fatalf("safe ID rejected: %v", err)
	}
	if err := validateSessionID("../escape"); err == nil {
		t.Fatal("expected unsafe ID error")
	}
}

func TestSessionStoreIgnoresMalformedJSON(t *testing.T) {
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "broken.json"), []byte("{"), 0600); err != nil {
		t.Fatalf("WriteFile() error = %v", err)
	}

	items, err := newSessionStore(dir).List()
	if err != nil {
		t.Fatalf("List() error = %v", err)
	}
	if len(items) != 0 {
		t.Fatalf("List() = %#v, want empty", items)
	}
}
