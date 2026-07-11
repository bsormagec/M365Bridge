package auth

import (
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"sync"
	"sync/atomic"
	"testing"
)

func TestRefreshSingleFlightUsesOneTokenRequest(t *testing.T) {
	directory := t.TempDir()
	refreshFile := filepath.Join(directory, "refresh-token")
	cacheFile := filepath.Join(directory, "token-cache.json")

	var requests atomic.Int32
	server := httptest.NewServer(http.HandlerFunc(func(response http.ResponseWriter, request *http.Request) {
		requests.Add(1)
		response.Header().Set("Content-Type", "application/json")
		_, _ = fmt.Fprint(response, `{"access_token":"access-token","refresh_token":"rotated-refresh-token","expires_in":3600}`)
	}))
	defer server.Close()

	manager := NewTokenManager("tenant", "client", "scope", refreshFile, cacheFile)
	manager.tokenURL = server.URL
	if err := manager.writeRefreshToken("initial-refresh-token"); err != nil {
		t.Fatalf("write refresh token: %v", err)
	}

	const callers = 12
	results := make(chan string, callers)
	errors := make(chan error, callers)
	var start sync.WaitGroup
	start.Add(1)
	var calls sync.WaitGroup
	for range callers {
		calls.Add(1)
		go func() {
			defer calls.Done()
			start.Wait()
			token, err := manager.Refresh()
			if err != nil {
				errors <- err
				return
			}
			results <- token
		}()
	}
	start.Done()
	calls.Wait()
	close(results)
	close(errors)

	for err := range errors {
		t.Fatalf("Refresh returned error: %v", err)
	}
	for token := range results {
		if token != "access-token" {
			t.Fatalf("unexpected token: %q", token)
		}
	}
	if got := requests.Load(); got != 1 {
		t.Fatalf("expected one token request, got %d", got)
	}
}

func TestWritePrivateFileReplacesContentWithOwnerOnlyPermissions(t *testing.T) {
	path := filepath.Join(t.TempDir(), "credentials", "token")
	if err := writePrivateFile(path, []byte("first")); err != nil {
		t.Fatalf("write first credential: %v", err)
	}
	if err := writePrivateFile(path, []byte("second")); err != nil {
		t.Fatalf("replace credential: %v", err)
	}

	info, err := os.Stat(path)
	if err != nil {
		t.Fatalf("stat credential: %v", err)
	}
	if got := info.Mode().Perm(); got != 0600 {
		t.Fatalf("expected credential permissions 0600, got %o", got)
	}
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read credential: %v", err)
	}
	if string(data) != "second" {
		t.Fatalf("expected replaced credential content, got %q", data)
	}
}
