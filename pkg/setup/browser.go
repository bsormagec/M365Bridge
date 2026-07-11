package setup

import (
	"bufio"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/KilimcininKorOglu/M365Bridge/pkg/auth"
	"github.com/KilimcininKorOglu/M365Bridge/pkg/models"
	"github.com/gorilla/websocket"
)

const (
	m365URL                  = "https://m365.cloud.microsoft/"
	defaultBrowserProfileDir = "data/m365-browser-profile"
	browserCaptureTimeout    = 10 * time.Minute
)

// BrowserSetupOptions controls the interactive, local browser setup flow.
type BrowserSetupOptions struct {
	ProfileDir  string
	BrowserPath string
	Input       io.Reader
	Output      io.Writer
	Timeout     time.Duration
}

type browserCookie struct {
	Name     string `json:"name"`
	Value    string `json:"value"`
	Domain   string `json:"domain"`
	Path     string `json:"path"`
	Secure   bool   `json:"secure"`
	HTTPOnly bool   `json:"httpOnly"`
}

type browserSetupConfig struct {
	OID          string
	Tenant       string
	RefreshToken string
	SSOCookies   []auth.SSOCookie
}

type browserSession struct {
	command   *exec.Cmd
	port      int
	closeOnce sync.Once
}

type tokenObserver struct {
	connection   *websocket.Conn
	mu           sync.Mutex
	refreshToken string
	error        error
	done         chan struct{}
	closeOnce    sync.Once
}

// RunBrowser opens a dedicated Chrome profile. The account owner must finish
// signing in and trigger a normal M365 page interaction; no sign-in step is automated.
func RunBrowser(ctx context.Context, options BrowserSetupOptions) error {
	config, err := captureBrowserSetup(ctx, options)
	if err != nil {
		return err
	}
	return completeSetup(config.Tenant, config.OID, config.RefreshToken, config.SSOCookies)
}

func captureBrowserSetup(ctx context.Context, options BrowserSetupOptions) (browserSetupConfig, error) {
	if options.ProfileDir == "" {
		options.ProfileDir = defaultBrowserProfileDir
	}
	if options.Input == nil {
		options.Input = os.Stdin
	}
	if options.Output == nil {
		options.Output = os.Stdout
	}
	if options.Timeout <= 0 {
		options.Timeout = browserCaptureTimeout
	}
	if err := ensurePrivateProfileDir(options.ProfileDir); err != nil {
		return browserSetupConfig{}, err
	}

	captureCtx, cancel := context.WithTimeout(ctx, options.Timeout)
	defer cancel()
	session, err := startM365Browser(captureCtx, options.ProfileDir, options.BrowserPath)
	if err != nil {
		return browserSetupConfig{}, err
	}
	defer session.Close()

	target, err := m365DebugTarget(captureCtx, session.port)
	if err != nil {
		return browserSetupConfig{}, err
	}
	observer, err := startTokenObserver(captureCtx, target.WebSocketDebuggerURL)
	if err != nil {
		return browserSetupConfig{}, err
	}
	defer observer.Close()

	fmt.Fprintf(options.Output, "Chrome opened in the dedicated profile %s.\n", options.ProfileDir)
	fmt.Fprintln(options.Output, "Sign in to Microsoft 365, complete MFA or policy prompts, then use M365 once to trigger a token refresh.")
	fmt.Fprintln(options.Output, "Return here and press Enter only after M365 has loaded successfully.")
	if err := waitForConfirmation(captureCtx, options.Input); err != nil {
		return browserSetupConfig{}, err
	}

	config, err := observer.Config(captureCtx, target.WebSocketDebuggerURL)
	if err != nil {
		return browserSetupConfig{}, err
	}
	cookies, err := readBrowserCookies(captureCtx, target.WebSocketDebuggerURL)
	if err != nil {
		return browserSetupConfig{}, err
	}
	config.SSOCookies = filterBrowserCookies(cookies)
	loginCookies, _ := splitCookiesByDomain(config.SSOCookies)
	if len(loginCookies) == 0 {
		return browserSetupConfig{}, errors.New("no Microsoft SSO cookies found; visit login.microsoftonline.com in the opened browser and try again")
	}
	return config, nil
}

func startTokenObserver(ctx context.Context, websocketURL string) (*tokenObserver, error) {
	connection, _, err := websocket.DefaultDialer.DialContext(ctx, websocketURL, nil)
	if err != nil {
		return nil, fmt.Errorf("connect to Chrome DevTools: %w", err)
	}
	observer := &tokenObserver{connection: connection, done: make(chan struct{})}
	if err := connection.WriteJSON(map[string]any{"id": 1, "method": "Network.enable"}); err != nil {
		connection.Close()
		return nil, err
	}
	go observer.observe()
	return observer, nil
}

func (o *tokenObserver) observe() {
	defer close(o.done)
	requestIDs := make(map[string]struct{})
	for {
		var message struct {
			ID     int             `json:"id"`
			Method string          `json:"method"`
			Params json.RawMessage `json:"params"`
			Result json.RawMessage `json:"result"`
		}
		if err := o.connection.ReadJSON(&message); err != nil {
			o.setError(err)
			return
		}
		switch message.Method {
		case "Network.requestWillBeSent":
			var event struct {
				RequestID string `json:"requestId"`
				Request   struct {
					URL      string `json:"url"`
					PostData string `json:"postData"`
				} `json:"request"`
			}
			if json.Unmarshal(message.Params, &event) == nil && isTargetTokenRequest(event.Request.URL, event.Request.PostData) {
				requestIDs[event.RequestID] = struct{}{}
			}
		case "Network.responseReceived":
			var event struct {
				RequestID string `json:"requestId"`
			}
			_ = json.Unmarshal(message.Params, &event)
		case "Network.loadingFinished":
			var event struct {
				RequestID string `json:"requestId"`
			}
			if json.Unmarshal(message.Params, &event) != nil {
				continue
			}
			if _, ok := requestIDs[event.RequestID]; !ok {
				continue
			}
			delete(requestIDs, event.RequestID)
			if err := o.connection.WriteJSON(map[string]any{"id": 2, "method": "Network.getResponseBody", "params": map[string]any{"requestId": event.RequestID}}); err != nil {
				o.setError(err)
				return
			}
		default:
			if message.ID == 2 {
				var response struct {
					Body string `json:"body"`
				}
				if json.Unmarshal(message.Result, &response) == nil {
					var tokenResponse struct {
						RefreshToken string `json:"refresh_token"`
					}
					if json.Unmarshal([]byte(response.Body), &tokenResponse) == nil && tokenResponse.RefreshToken != "" {
						o.mu.Lock()
						o.refreshToken = tokenResponse.RefreshToken
						o.mu.Unlock()
					}
				}
			}
		}
	}
}

func (o *tokenObserver) Config(ctx context.Context, websocketURL string) (browserSetupConfig, error) {
	o.mu.Lock()
	refreshToken := o.refreshToken
	observerErr := o.error
	o.mu.Unlock()
	if observerErr != nil {
		return browserSetupConfig{}, fmt.Errorf("observe M365 token responses: %w", observerErr)
	}
	if refreshToken == "" {
		return browserSetupConfig{}, errors.New("no M365 refresh token captured; send a message in the opened M365 tab before pressing Enter")
	}
	account, err := readBrowserAccount(ctx, websocketURL)
	if err != nil {
		return browserSetupConfig{}, err
	}
	return browserSetupConfig{OID: account.OID, Tenant: account.Tenant, RefreshToken: refreshToken}, nil
}

func (o *tokenObserver) Close() {
	o.closeOnce.Do(func() {
		_ = o.connection.Close()
		<-o.done
	})
}

func (o *tokenObserver) setError(err error) {
	o.mu.Lock()
	defer o.mu.Unlock()
	if o.error == nil {
		o.error = err
	}
}

func isTokenEndpoint(value string) bool {
	lower := strings.ToLower(value)
	return strings.Contains(lower, "login.microsoftonline.com/") && strings.Contains(lower, "/oauth2/v2.0/token")
}

func isTargetTokenRequest(value, postData string) bool {
	return isTokenEndpoint(value) && strings.Contains(postData, "client_id="+models.DefaultClientID)
}

func completeSetup(tenant, oid, refreshToken string, ssoCookies []auth.SSOCookie) error {
	credentialFiles := []string{
		defaultRefreshTokenFile,
		defaultCacheFile,
		"data/tokens/sso_cookies.json",
		"data/tokens/m365_cookies.json",
		defaultEnvFile,
	}
	rollback, err := snapshotFiles(credentialFiles)
	if err != nil {
		return fmt.Errorf("snapshot existing credentials: %w", err)
	}
	succeeded := false
	defer func() {
		if !succeeded {
			_ = rollback()
		}
	}()

	if len(ssoCookies) > 0 {
		loginCookies, m365Cookies := splitCookiesByDomain(ssoCookies)
		if len(loginCookies) > 0 {
			if err := auth.SaveSSOCookies(loginCookies); err != nil {
				return fmt.Errorf("save encrypted SSO cookies: %w", err)
			}
			fmt.Printf("  SSO cookies encrypted and saved (%d captured)\n", len(loginCookies))
		}
		if len(m365Cookies) > 0 {
			if err := auth.SaveM365Cookies(m365Cookies); err != nil {
				return fmt.Errorf("save M365 web cookies: %w", err)
			}
			fmt.Printf("  M365 web cookies saved (%d captured)\n", len(m365Cookies))
		}
	}
	if err := verifyToken(tenant, oid, refreshToken); err != nil {
		return err
	}
	if err := saveEnv(tenant, oid); err != nil {
		return err
	}
	succeeded = true
	printSetupSuccess()
	return nil
}

type fileSnapshot struct {
	exists bool
	data   []byte
	mode   os.FileMode
}

func snapshotFiles(paths []string) (func() error, error) {
	snapshots := make(map[string]fileSnapshot, len(paths))
	for _, path := range paths {
		info, err := os.Stat(path)
		if os.IsNotExist(err) {
			snapshots[path] = fileSnapshot{}
			continue
		}
		if err != nil {
			return nil, err
		}
		data, err := os.ReadFile(path)
		if err != nil {
			return nil, err
		}
		snapshots[path] = fileSnapshot{exists: true, data: data, mode: info.Mode()}
	}
	return func() error {
		for path, snapshot := range snapshots {
			if !snapshot.exists {
				if err := os.Remove(path); err != nil && !os.IsNotExist(err) {
					return err
				}
				continue
			}
			if err := os.WriteFile(path, snapshot.data, snapshot.mode.Perm()); err != nil {
				return err
			}
		}
		return nil
	}, nil
}

func printSetupSuccess() {
	fmt.Println("=" + strings.Repeat("=", 58))
	fmt.Println("Setup Complete!")
	fmt.Println("=" + strings.Repeat("=", 58))
	fmt.Printf("Token storage: %s\n", filepath.Dir(defaultRefreshTokenFile))
	fmt.Printf("Config file:   %s\n", defaultEnvFile)
}

func ensurePrivateProfileDir(path string) error {
	info, err := os.Lstat(path)
	if err == nil {
		if info.Mode()&os.ModeSymlink != 0 || !info.IsDir() {
			return fmt.Errorf("browser profile must be a real directory: %s", path)
		}
		if runtime.GOOS != "windows" && info.Mode().Perm()&0o077 != 0 {
			return fmt.Errorf("browser profile permissions must not allow group or other access: %s", path)
		}
		return nil
	}
	if !os.IsNotExist(err) {
		return fmt.Errorf("inspect browser profile: %w", err)
	}
	if err := os.MkdirAll(path, 0o700); err != nil {
		return fmt.Errorf("create private browser profile: %w", err)
	}
	return os.Chmod(path, 0o700)
}

func startM365Browser(ctx context.Context, profileDir, configuredPath string) (*browserSession, error) {
	browserPath, err := findBrowser(configuredPath)
	if err != nil {
		return nil, err
	}
	portFile := filepath.Join(profileDir, "DevToolsActivePort")
	if err := os.Remove(portFile); err != nil && !os.IsNotExist(err) {
		return nil, fmt.Errorf("remove stale DevTools port file: %w", err)
	}
	args := []string{
		"--remote-debugging-port=0",
		"--remote-debugging-address=127.0.0.1",
		"--user-data-dir=" + profileDir,
		"--no-first-run",
		"--no-default-browser-check",
	}
	command := exec.CommandContext(ctx, browserPath, args...)
	startupLog, err := os.CreateTemp(profileDir, "chrome-startup-*.log")
	if err != nil {
		return nil, fmt.Errorf("create Chrome startup log: %w", err)
	}
	command.Stdout = startupLog
	command.Stderr = startupLog
	if err := command.Start(); err != nil {
		startupLog.Close()
		return nil, fmt.Errorf("start Chrome: %w", err)
	}
	if err := startupLog.Close(); err != nil {
		stopBrowser(command)
		return nil, fmt.Errorf("close Chrome startup log: %w", err)
	}
	readyCtx, cancel := context.WithTimeout(ctx, 45*time.Second)
	defer cancel()
	port, err := waitForDevTools(readyCtx, portFile)
	if err != nil {
		stopBrowser(command)
		return nil, fmt.Errorf("%w; Chrome startup log: %s", err, startupLog.Name())
	}
	_ = os.Remove(startupLog.Name())
	if err := createM365Target(ctx, port); err != nil {
		stopBrowser(command)
		return nil, err
	}
	return &browserSession{command: command, port: port}, nil
}

func (s *browserSession) Close() { s.closeOnce.Do(func() { stopBrowser(s.command) }) }

func findBrowser(configuredPath string) (string, error) {
	if configuredPath != "" {
		if info, err := os.Stat(configuredPath); err == nil && !info.IsDir() {
			return configuredPath, nil
		}
		return "", fmt.Errorf("browser executable not found: %s", configuredPath)
	}
	candidates := []string{}
	if runtime.GOOS == "darwin" {
		candidates = append(candidates, "/Applications/Google Chrome.app/Contents/MacOS/Google Chrome", "/Applications/Chromium.app/Contents/MacOS/Chromium")
	}
	for _, candidate := range candidates {
		if info, err := os.Stat(candidate); err == nil && !info.IsDir() {
			return candidate, nil
		}
	}
	for _, name := range []string{"google-chrome", "google-chrome-stable", "chromium", "chromium-browser", "chrome"} {
		if path, err := exec.LookPath(name); err == nil {
			return path, nil
		}
	}
	return "", errors.New("could not find Chrome; pass --browser-path to setup-wizard")
}

func waitForDevTools(ctx context.Context, portFile string) (int, error) {
	ticker := time.NewTicker(100 * time.Millisecond)
	defer ticker.Stop()
	for {
		if data, err := os.ReadFile(portFile); err == nil {
			if lines := strings.Split(strings.TrimSpace(string(data)), "\n"); len(lines) > 0 {
				if port, err := strconv.Atoi(strings.TrimSpace(lines[0])); err == nil && port > 0 {
					return port, nil
				}
			}
		}
		select {
		case <-ctx.Done():
			return 0, fmt.Errorf("wait for Chrome DevTools: %w", ctx.Err())
		case <-ticker.C:
		}
	}
}

func createM365Target(ctx context.Context, port int) error {
	request, err := http.NewRequestWithContext(ctx, http.MethodGet, fmt.Sprintf("http://127.0.0.1:%d/json/version", port), nil)
	if err != nil {
		return err
	}
	response, err := http.DefaultClient.Do(request)
	if err != nil {
		return fmt.Errorf("read Chrome DevTools version: %w", err)
	}
	defer response.Body.Close()
	var version struct {
		WebSocketDebuggerURL string `json:"webSocketDebuggerUrl"`
	}
	if response.StatusCode != http.StatusOK || json.NewDecoder(response.Body).Decode(&version) != nil || version.WebSocketDebuggerURL == "" {
		return errors.New("read Chrome browser DevTools endpoint")
	}
	_, err = cdpCall(ctx, version.WebSocketDebuggerURL, "Target.createTarget", map[string]any{"url": m365URL, "newWindow": true})
	if err != nil {
		return fmt.Errorf("open M365 tab in Chrome: %w", err)
	}
	return nil
}

func waitForConfirmation(ctx context.Context, input io.Reader) error {
	confirmed := make(chan error, 1)
	go func() {
		_, err := bufio.NewReader(input).ReadString('\n')
		if err == io.EOF {
			err = nil
		}
		confirmed <- err
	}()
	select {
	case err := <-confirmed:
		return err
	case <-ctx.Done():
		return fmt.Errorf("browser setup timed out: %w", ctx.Err())
	}
}

type debugTarget struct {
	Type                 string `json:"type"`
	URL                  string `json:"url"`
	WebSocketDebuggerURL string `json:"webSocketDebuggerUrl"`
}

func m365DebugTarget(ctx context.Context, port int) (debugTarget, error) {
	ticker := time.NewTicker(100 * time.Millisecond)
	defer ticker.Stop()
	for {
		target, err := findM365DebugTarget(ctx, port)
		if err == nil {
			return target, nil
		}
		select {
		case <-ctx.Done():
			return debugTarget{}, fmt.Errorf("wait for M365 page: %w", ctx.Err())
		case <-ticker.C:
		}
	}
}

func findM365DebugTarget(ctx context.Context, port int) (debugTarget, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, fmt.Sprintf("http://127.0.0.1:%d/json/list", port), nil)
	if err != nil {
		return debugTarget{}, err
	}
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return debugTarget{}, fmt.Errorf("list Chrome tabs: %w", err)
	}
	defer resp.Body.Close()
	var targets []debugTarget
	if resp.StatusCode != http.StatusOK {
		return debugTarget{}, fmt.Errorf("list Chrome tabs: unexpected status %s", resp.Status)
	}
	if err := json.NewDecoder(resp.Body).Decode(&targets); err != nil {
		return debugTarget{}, fmt.Errorf("read Chrome tabs: %w", err)
	}
	for _, target := range targets {
		if target.Type == "page" && strings.Contains(strings.ToLower(target.URL), "m365.cloud.microsoft") && target.WebSocketDebuggerURL != "" {
			return target, nil
		}
	}
	return debugTarget{}, errors.New("M365 page not ready")
}

func readBrowserAccount(ctx context.Context, websocketURL string) (browserSetupConfig, error) {
	const accountExpression = `(() => {
	for (const key of Object.keys(localStorage)) {
		try {
			const value = JSON.parse(localStorage.getItem(key));
			const id = value && value.homeAccountId;
			if (typeof id === 'string' && id.includes('.')) {
				const [oid, tenant] = id.split('.');
				return JSON.stringify({oid, tenant});
			}
		} catch (_) {}
		const part = key.startsWith('msal.') && key.includes('|') ? key.split('|')[1] : '';
		if (part.includes('.')) {
			const [oid, tenant] = part.split('.');
			return JSON.stringify({oid, tenant});
		}
	}
	return '{}';
})()`
	result, err := cdpCall(ctx, websocketURL, "Runtime.evaluate", map[string]any{"expression": accountExpression, "returnByValue": true})
	if err != nil {
		return browserSetupConfig{}, err
	}
	var response struct {
		Result struct {
			Result struct {
				Value string `json:"value"`
			} `json:"result"`
		} `json:"result"`
	}
	if err := json.Unmarshal(result, &response); err != nil {
		return browserSetupConfig{}, err
	}
	var account browserSetupConfig
	if err := json.Unmarshal([]byte(response.Result.Result.Value), &account); err != nil {
		return browserSetupConfig{}, err
	}
	if account.OID == "" || account.Tenant == "" {
		return browserSetupConfig{}, errors.New("could not determine tenant and user OID from the M365 browser session")
	}
	return account, nil
}

func readBrowserCookies(ctx context.Context, websocketURL string) ([]browserCookie, error) {
	result, err := cdpCall(ctx, websocketURL, "Network.getAllCookies", nil)
	if err != nil {
		return nil, err
	}
	var response struct {
		Result struct {
			Cookies []browserCookie `json:"cookies"`
		} `json:"result"`
	}
	if err := json.Unmarshal(result, &response); err != nil {
		return nil, err
	}
	return response.Result.Cookies, nil
}

func cdpCall(ctx context.Context, websocketURL, method string, params map[string]any) ([]byte, error) {
	conn, _, err := websocket.DefaultDialer.DialContext(ctx, websocketURL, nil)
	if err != nil {
		return nil, fmt.Errorf("connect to Chrome DevTools: %w", err)
	}
	defer conn.Close()
	if err := conn.WriteJSON(map[string]any{"id": 1, "method": method, "params": params}); err != nil {
		return nil, err
	}
	for {
		var message struct {
			ID    int `json:"id"`
			Error *struct {
				Message string `json:"message"`
			} `json:"error"`
			Result json.RawMessage `json:"result"`
		}
		if err := conn.ReadJSON(&message); err != nil {
			return nil, err
		}
		if message.ID != 1 {
			continue
		}
		if message.Error != nil {
			return nil, errors.New(message.Error.Message)
		}
		return json.Marshal(map[string]json.RawMessage{"result": message.Result})
	}
}

func filterBrowserCookies(cookies []browserCookie) []auth.SSOCookie {
	filtered := make([]auth.SSOCookie, 0, len(cookies))
	for _, cookie := range cookies {
		domain := strings.TrimPrefix(strings.ToLower(strings.TrimSpace(cookie.Domain)), ".")
		isLoginSSO := domain == "login.microsoftonline.com" && (cookie.Name == "ESTSAUTH" || cookie.Name == "ESTSAUTHPERSISTENT")
		isM365WebCookie := domain == "m365.cloud.microsoft"
		if !isLoginSSO && !isM365WebCookie {
			continue
		}
		filtered = append(filtered, auth.SSOCookie{Name: cookie.Name, Value: cookie.Value, Path: cookie.Path, Domain: cookie.Domain, Secure: cookie.Secure, HttpOnly: cookie.HTTPOnly})
	}
	return filtered
}

func stopBrowser(command *exec.Cmd) {
	if command != nil && command.Process != nil {
		_ = command.Process.Kill()
		_, _ = command.Process.Wait()
	}
}
