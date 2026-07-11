// Package auth provides token management and OAuth2 authentication for M365 Copilot.
// It handles access token caching, refresh token storage, and token refresh logic.
package auth

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"

	"github.com/KilimcininKorOglu/M365Bridge/pkg/crypto"
	"github.com/KilimcininKorOglu/M365Bridge/pkg/logging"
)

var (
	// ErrTokenNotFound is returned when the refresh token file is empty or missing.
	ErrTokenNotFound = errors.New("refresh token not found")
	// ErrRefreshFailed is returned when token refresh fails.
	ErrRefreshFailed = errors.New("token refresh failed")
)

const (
	// tokenURLTemplate is the OAuth2 token endpoint URL template.
	tokenURLTemplate = "https://login.microsoftonline.com/%s/oauth2/v2.0/token"
	// cacheExpiryBuffer is the time buffer before token expiry to trigger refresh.
	cacheExpiryBuffer = 60 * time.Second
)

// TokenCache represents the cached access token data.
type TokenCache struct {
	AccessToken string `json:"access_token"`
	ExpiresAt   int64  `json:"expires_at"`
}

// TokenManager handles OAuth2 token lifecycle management.
type TokenManager struct {
	refreshMu              sync.Mutex
	refreshInFlight        bool
	refreshDone            chan struct{}
	lastRefreshToken       string
	lastRefreshErr         error
	tenant                 string
	clientID               string
	scope                  string
	refreshFile            string
	cacheFile              string
	tokenURL               string
	userOID                string
	designerTokenRequest   func(string) (string, int, error)
	brokerTokenAcquisition func() (string, error)
}

// NewTokenManager creates a new TokenManager instance.
func NewTokenManager(tenant, clientID, scope, refreshFile, cacheFile string) *TokenManager {
	return &TokenManager{
		tenant:      tenant,
		clientID:    clientID,
		scope:       scope,
		refreshFile: refreshFile,
		cacheFile:   cacheFile,
		tokenURL:    fmt.Sprintf(tokenURLTemplate, tenant),
	}
}

// SetUserOID sets the user object ID for broker token requests.
func (tm *TokenManager) SetUserOID(oid string) {
	tm.userOID = oid
}

// Get returns a valid access token, refreshing if necessary.
// Returns cached token if valid, otherwise performs token refresh.
func (tm *TokenManager) Get() (string, error) {
	// Try to load from cache first
	if token, err := tm.loadFromCache(); err == nil {
		logging.Debug("TokenManager.Get: cache hit")
		return token, nil
	}

	logging.Debug("TokenManager.Get: cache miss, refreshing")
	// Cache miss or expired, perform refresh
	return tm.Refresh()
}

// Refresh exchanges the refresh token for a new access token.
// Updates both the refresh token file and cache file.
func (tm *TokenManager) Refresh() (string, error) {
	tm.refreshMu.Lock()
	if tm.refreshInFlight {
		done := tm.refreshDone
		tm.refreshMu.Unlock()
		<-done
		tm.refreshMu.Lock()
		token, err := tm.lastRefreshToken, tm.lastRefreshErr
		tm.refreshMu.Unlock()
		return token, err
	}
	tm.refreshInFlight = true
	tm.refreshDone = make(chan struct{})
	tm.refreshMu.Unlock()

	token, err := tm.refresh()

	tm.refreshMu.Lock()
	tm.lastRefreshToken = token
	tm.lastRefreshErr = err
	tm.refreshInFlight = false
	close(tm.refreshDone)
	tm.refreshMu.Unlock()
	return token, err
}

func (tm *TokenManager) refresh() (string, error) {
	startedAt := time.Now()
	defer func() {
		logging.Debugf("TokenManager.Refresh: completed in %s", time.Since(startedAt).Round(time.Millisecond))
	}()
	logging.Info("TokenManager.Refresh: starting token refresh")
	refreshToken, err := tm.readRefreshToken()
	if err != nil {
		logging.Errorf("TokenManager.Refresh: failed to read refresh token: %v", err)
		return "", err
	}

	data := url.Values{}
	data.Set("client_id", tm.clientID)
	data.Set("refresh_token", refreshToken)
	data.Set("grant_type", "refresh_token")
	data.Set("scope", tm.scope)

	req, err := http.NewRequest("POST", tm.tokenURL, bytes.NewBufferString(data.Encode()))
	if err != nil {
		return "", fmt.Errorf("%w: failed to create request", ErrRefreshFailed)
	}

	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	req.Header.Set("Origin", "https://m365.cloud.microsoft")
	req.Header.Set("User-Agent", "Mozilla/5.0")

	client := &http.Client{}
	resp, err := client.Do(req)
	if err != nil {
		return "", fmt.Errorf("%w: %v", ErrRefreshFailed, err)
	}
	defer resp.Body.Close()

	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return "", fmt.Errorf("%w: failed to read response", ErrRefreshFailed)
	}

	if resp.StatusCode != http.StatusOK {
		errMsg := string(body)
		// If refresh token expired (AADSTS700084), try SSO cookie re-auth as fallback
		if strings.Contains(errMsg, "AADSTS700084") && hasSSOCookies() {
			logging.Warn("TokenManager.Refresh: refresh token expired (AADSTS700084), falling back to SSO cookie re-auth")
			return tm.reauthWithSSO()
		}
		logging.Errorf("TokenManager.Refresh: token refresh failed status=%d: %s", resp.StatusCode, errMsg[:min(200, len(errMsg))])
		return "", fmt.Errorf("%w: status %d: %s", ErrRefreshFailed, resp.StatusCode, errMsg)
	}

	var result struct {
		AccessToken  string `json:"access_token"`
		RefreshToken string `json:"refresh_token"`
		ExpiresIn    int    `json:"expires_in"`
	}

	if err := json.Unmarshal(body, &result); err != nil {
		logging.Errorf("TokenManager.Refresh: failed to parse response: %v", err)
		return "", fmt.Errorf("%w: failed to parse response", ErrRefreshFailed)
	}

	// Save new refresh token if provided
	if result.RefreshToken != "" {
		if err := tm.writeRefreshToken(result.RefreshToken); err != nil {
			logging.Errorf("TokenManager.Refresh: failed to save refresh token: %v", err)
			return "", fmt.Errorf("%w: failed to save refresh token", ErrRefreshFailed)
		}
	}

	// Cache access token
	expiresAt := time.Now().Add(time.Duration(result.ExpiresIn) * time.Second)
	cache := TokenCache{
		AccessToken: result.AccessToken,
		ExpiresAt:   expiresAt.Unix(),
	}

	if err := tm.writeCache(cache); err != nil {
		logging.Errorf("TokenManager.Refresh: failed to write cache: %v", err)
		return "", fmt.Errorf("%w: failed to write cache", ErrRefreshFailed)
	}

	logging.Infof("TokenManager.Refresh: success, expires_in=%d expires_at=%s", result.ExpiresIn, expiresAt.Format(time.RFC3339))
	return result.AccessToken, nil
}

// GetTokenForScope exchanges the refresh token for an access token with a
// different scope (e.g. PowerPlatform or BAP). Does NOT cache or rotate the
// refresh token — use Refresh() for the primary M365 scope.
func (tm *TokenManager) GetTokenForScope(scope string) (string, error) {
	return tm.GetTokenForScopeAndClient(scope, tm.clientID)
}

// GetTokenForScopeAndClient exchanges the refresh token for an access token
// with a different scope AND a different client ID (RT exchange). Used for
// APIs that require a token issued to a specific SPA client (e.g. Copilot
// Studio's Island Gateway requires client_id 96ff4394-9197-43aa-b393-6a41652e21f8).
func (tm *TokenManager) GetTokenForScopeAndClient(scope, clientID string) (string, error) {
	refreshToken, err := tm.readRefreshToken()
	if err != nil {
		return "", err
	}

	data := url.Values{}
	data.Set("client_id", clientID)
	data.Set("refresh_token", refreshToken)
	data.Set("grant_type", "refresh_token")
	data.Set("scope", scope)

	req, err := http.NewRequest("POST", tm.tokenURL, bytes.NewBufferString(data.Encode()))
	if err != nil {
		return "", fmt.Errorf("%w: failed to create request", ErrRefreshFailed)
	}

	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	req.Header.Set("Origin", "https://m365.cloud.microsoft")
	req.Header.Set("User-Agent", "Mozilla/5.0")

	client := &http.Client{}
	resp, err := client.Do(req)
	if err != nil {
		return "", fmt.Errorf("%w: %v", ErrRefreshFailed, err)
	}
	defer resp.Body.Close()

	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return "", fmt.Errorf("%w: failed to read response", ErrRefreshFailed)
	}

	if resp.StatusCode != http.StatusOK {
		return "", fmt.Errorf("%w: status %d: %s", ErrRefreshFailed, resp.StatusCode, string(body))
	}

	var result struct {
		AccessToken string `json:"access_token"`
	}
	if err := json.Unmarshal(body, &result); err != nil {
		return "", fmt.Errorf("%w: failed to parse response", ErrRefreshFailed)
	}

	return result.AccessToken, nil
}

// readRefreshToken reads and decrypts the refresh token from file.
func (tm *TokenManager) readRefreshToken() (string, error) {
	data, err := os.ReadFile(tm.refreshFile)
	if err != nil {
		return "", fmt.Errorf("%w: %s", ErrTokenNotFound, tm.refreshFile)
	}

	encrypted := string(data)
	if encrypted == "" {
		return "", ErrTokenNotFound
	}

	// Try to decrypt
	decrypted, err := crypto.Decrypt(encrypted)
	if err != nil {
		// If decryption fails, assume it's plaintext (legacy support)
		return encrypted, nil
	}

	return decrypted, nil
}

// writeRefreshToken encrypts and writes the refresh token to file.
func (tm *TokenManager) writeRefreshToken(token string) error {
	encrypted, err := crypto.Encrypt(token)
	if err != nil {
		return fmt.Errorf("failed to encrypt token: %w", err)
	}

	// Ensure directory exists
	dir := filepath.Dir(tm.refreshFile)
	if dir != "" && dir != "." {
		if err := os.MkdirAll(dir, 0700); err != nil {
			return fmt.Errorf("failed to create directory: %w", err)
		}
	}

	return writePrivateFile(tm.refreshFile, []byte(encrypted))
}

// loadFromCache attempts to load a valid access token from cache.
func (tm *TokenManager) loadFromCache() (string, error) {
	data, err := os.ReadFile(tm.cacheFile)
	if err != nil {
		return "", err
	}

	var cache TokenCache
	if err := json.Unmarshal(data, &cache); err != nil {
		return "", err
	}

	// Check if token is still valid
	if cache.ExpiresAt > time.Now().Add(cacheExpiryBuffer).Unix() {
		return cache.AccessToken, nil
	}

	return "", errors.New("token expired")
}

// writeCache writes the access token cache to file.
func (tm *TokenManager) writeCache(cache TokenCache) error {
	data, err := json.Marshal(cache)
	if err != nil {
		return err
	}

	// Ensure directory exists
	dir := filepath.Dir(tm.cacheFile)
	if dir != "" && dir != "." {
		if err := os.MkdirAll(dir, 0700); err != nil {
			return fmt.Errorf("failed to create directory: %w", err)
		}
	}

	return writePrivateFile(tm.cacheFile, data)
}

// writePrivateFile atomically replaces a credential file with owner-only permissions.
func writePrivateFile(path string, data []byte) error {
	dir := filepath.Dir(path)
	if dir == "" {
		dir = "."
	}
	if err := os.MkdirAll(dir, 0700); err != nil {
		return err
	}
	temporary, err := os.CreateTemp(dir, ".m365bridge-*")
	if err != nil {
		return err
	}
	temporaryPath := temporary.Name()
	defer os.Remove(temporaryPath)
	if err := temporary.Chmod(0600); err != nil {
		_ = temporary.Close()
		return err
	}
	if _, err := temporary.Write(data); err != nil {
		_ = temporary.Close()
		return err
	}
	if err := temporary.Close(); err != nil {
		return err
	}
	return os.Rename(temporaryPath, path)
}
