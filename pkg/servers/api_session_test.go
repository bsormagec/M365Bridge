package servers

import (
	"net/http/httptest"
	"testing"

	"github.com/KilimcininKorOglu/M365Bridge/pkg/payload"
)

func TestSessionIDForMessagesPersistsAcrossTurns(t *testing.T) {
	api := &APIServer{}
	req := httptest.NewRequest("POST", "/v1/messages", nil)
	messages := []payload.Message{
		{Role: "user", Content: "Selam"},
		{Role: "assistant", Content: "Merhaba"},
		{Role: "user", Content: "Sen kimsin?"},
	}

	firstTurnID := api.sessionIDForMessages(req, messages[:1])
	secondTurnID := api.sessionIDForMessages(req, messages)
	if firstTurnID == "" {
		t.Fatal("expected a fallback session ID")
	}
	if secondTurnID != firstTurnID {
		t.Fatalf("session ID changed between turns: first=%q second=%q", firstTurnID, secondTurnID)
	}
}

func TestSessionIDForRequestHonorsExplicitIdentity(t *testing.T) {
	api := &APIServer{}
	req := httptest.NewRequest("POST", "/v1/messages", nil)
	messages := []payload.Message{{Role: "user", Content: "Selam"}}

	if got := api.sessionIDForRequest(req, "body-session", "body-user", messages); got != "body-session" {
		t.Fatalf("explicit session ID was not preferred: got %q", got)
	}
	if got := api.sessionIDForRequest(req, "", "body-user", messages); got != "body-user" {
		t.Fatalf("user identity was not used: got %q", got)
	}

	req.Header.Set("X-Session-Id", "header-session")
	if got := api.sessionIDForMessages(req, messages); got != "header-session" {
		t.Fatalf("header session ID was not preferred: got %q", got)
	}
}
