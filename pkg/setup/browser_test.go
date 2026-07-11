package setup

import (
	"os"
	"path/filepath"
	"testing"
)

func TestFilterBrowserCookiesSeparatesAllowedDomains(t *testing.T) {
	cookies := filterBrowserCookies([]browserCookie{
		{Name: "ESTSAUTH", Value: "first", Domain: ".login.microsoftonline.com", Secure: true, HTTPOnly: true},
		{Name: "ESTSAUTHPERSISTENT", Value: "second", Domain: "login.microsoftonline.com"},
		{Name: "OtherMicrosoftCookie", Value: "exclude", Domain: "login.microsoftonline.com"},
		{Name: "M365Session", Value: "third", Domain: "m365.cloud.microsoft"},
		{Name: "ESTSAUTH", Value: "exclude", Domain: "office.com"},
	})

	if len(cookies) != 3 {
		t.Fatalf("expected three allowed browser cookies, got %d", len(cookies))
	}
	if cookies[0].Name != "ESTSAUTH" || cookies[1].Name != "ESTSAUTHPERSISTENT" || cookies[2].Name != "M365Session" {
		t.Fatalf("unexpected cookies: %#v", cookies)
	}
}

func TestEnsurePrivateProfileDirCreatesOwnerOnlyDirectory(t *testing.T) {
	path := filepath.Join(t.TempDir(), "profile")
	if err := ensurePrivateProfileDir(path); err != nil {
		t.Fatalf("ensurePrivateProfileDir returned error: %v", err)
	}
	info, err := os.Stat(path)
	if err != nil {
		t.Fatalf("stat profile: %v", err)
	}
	if !info.IsDir() {
		t.Fatal("expected profile path to be a directory")
	}
	if info.Mode().Perm()&0o077 != 0 {
		t.Fatalf("expected no group/other permissions, got %o", info.Mode().Perm())
	}
}

func TestEnsurePrivateProfileDirRejectsSymlink(t *testing.T) {
	root := t.TempDir()
	target := filepath.Join(root, "target")
	if err := os.Mkdir(target, 0o700); err != nil {
		t.Fatalf("create target: %v", err)
	}
	link := filepath.Join(root, "profile-link")
	if err := os.Symlink(target, link); err != nil {
		t.Fatalf("create symlink: %v", err)
	}
	if err := ensurePrivateProfileDir(link); err == nil {
		t.Fatal("expected symlink profile to be rejected")
	}
}

func TestIsTargetTokenRequestRequiresM365ClientID(t *testing.T) {
	endpoint := "https://login.microsoftonline.com/tenant/oauth2/v2.0/token"
	if !isTargetTokenRequest(endpoint, "client_id=4765445b-32c6-49b0-83e6-1d93765276ca&grant_type=refresh_token") {
		t.Fatal("expected target M365 token request to match")
	}
	if isTargetTokenRequest(endpoint, "client_id=some-other-client") {
		t.Fatal("expected non-M365 client request to be excluded")
	}
	if isTargetTokenRequest("https://example.test/oauth2/v2.0/token", "client_id=4765445b-32c6-49b0-83e6-1d93765276ca") {
		t.Fatal("expected non-Microsoft endpoint to be excluded")
	}
}

func TestFilterBrowserCookiesRejectsMicrosoftComDomain(t *testing.T) {
	cookies := filterBrowserCookies([]browserCookie{
		{Name: "M365Session", Value: "keep", Domain: "m365.cloud.microsoft"},
		{Name: "Unrelated", Value: "exclude", Domain: "microsoft.com"},
	})
	if len(cookies) != 1 || cookies[0].Domain != "m365.cloud.microsoft" {
		t.Fatalf("expected only the M365 host cookie, got %#v", cookies)
	}
}
