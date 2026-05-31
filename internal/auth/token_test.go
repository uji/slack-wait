package auth

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"
)

// ---- Token.Valid ----

func TestValid_ZeroExpiry(t *testing.T) {
	// Zero ExpiresAt means non-rotating token; always valid.
	tok := &Token{AccessToken: "xoxp-test"}
	if !tok.Valid() {
		t.Error("zero ExpiresAt should be valid (non-rotating token)")
	}
}

func TestValid_FarFuture(t *testing.T) {
	tok := &Token{AccessToken: "xoxp-test", ExpiresAt: time.Now().Add(1 * time.Hour)}
	if !tok.Valid() {
		t.Error("token expiring in 1h should be valid")
	}
}

func TestValid_WithinGrace(t *testing.T) {
	// Expiring in 3 min is within the 5-min grace period → not valid.
	tok := &Token{AccessToken: "xoxp-test", ExpiresAt: time.Now().Add(3 * time.Minute)}
	if tok.Valid() {
		t.Error("token expiring in 3 minutes should not be valid (within grace period)")
	}
}

func TestValid_Expired(t *testing.T) {
	tok := &Token{AccessToken: "xoxp-test", ExpiresAt: time.Now().Add(-1 * time.Hour)}
	if tok.Valid() {
		t.Error("expired token should not be valid")
	}
}

// ---- Save / Load / Delete ----

func isolateConfigDir(t *testing.T) {
	t.Helper()
	dir := t.TempDir()
	t.Setenv("HOME", dir)
	t.Setenv("XDG_CONFIG_HOME", dir)
}

func TestSaveLoad(t *testing.T) {
	isolateConfigDir(t)

	want := &Token{
		AccessToken:  "xoxp-access",
		RefreshToken: "xoxe-refresh",
		// Truncate to second so JSON round-trip matches exactly.
		ExpiresAt: time.Now().Add(1 * time.Hour).Truncate(time.Second),
	}
	if err := Save(want); err != nil {
		t.Fatalf("Save: %v", err)
	}

	got, err := Load()
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	if got.AccessToken != want.AccessToken {
		t.Errorf("AccessToken: got %q, want %q", got.AccessToken, want.AccessToken)
	}
	if got.RefreshToken != want.RefreshToken {
		t.Errorf("RefreshToken: got %q, want %q", got.RefreshToken, want.RefreshToken)
	}
	if !got.ExpiresAt.Equal(want.ExpiresAt) {
		t.Errorf("ExpiresAt: got %v, want %v", got.ExpiresAt, want.ExpiresAt)
	}
}

func TestLoad_Missing(t *testing.T) {
	isolateConfigDir(t)

	_, err := Load()
	if err != ErrNoToken {
		t.Fatalf("want ErrNoToken, got %v", err)
	}
}

func TestDelete_RemovesFile(t *testing.T) {
	isolateConfigDir(t)

	if err := Save(&Token{AccessToken: "xoxp-test"}); err != nil {
		t.Fatal(err)
	}
	if err := Delete(); err != nil {
		t.Fatalf("Delete: %v", err)
	}
	_, err := Load()
	if err != ErrNoToken {
		t.Fatalf("want ErrNoToken after delete, got %v", err)
	}
}

func TestDelete_Idempotent(t *testing.T) {
	isolateConfigDir(t)

	// Deleting a non-existent file should not return an error.
	if err := Delete(); err != nil {
		t.Fatalf("Delete on missing file: %v", err)
	}
}

// ---- helper: mock token server ----

func mockTokenServer(t *testing.T, body any) *httptest.Server {
	t.Helper()
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(body) //nolint:errcheck
	}))
	t.Cleanup(srv.Close)
	return srv
}

func withMockOAuth(t *testing.T, srv *httptest.Server) {
	t.Helper()
	orig := oauthBaseURL
	oauthBaseURL = srv.URL
	t.Cleanup(func() { oauthBaseURL = orig })
}

// ---- ExchangeCode ----

func TestExchangeCode_Success_AuthedUser(t *testing.T) {
	// Initial exchange: credentials are nested under authed_user.
	srv := mockTokenServer(t, map[string]any{
		"ok": true,
		"authed_user": map[string]any{
			"access_token":  "xoxp-fresh",
			"refresh_token": "xoxe-refresh",
			"expires_in":    43200,
		},
	})
	withMockOAuth(t, srv)

	tok, err := ExchangeCode("CLIENT", "code123", "verifier", "http://localhost/cb")
	if err != nil {
		t.Fatalf("ExchangeCode: %v", err)
	}
	if tok.AccessToken != "xoxp-fresh" {
		t.Errorf("AccessToken: got %q, want xoxp-fresh", tok.AccessToken)
	}
	if tok.RefreshToken != "xoxe-refresh" {
		t.Errorf("RefreshToken: got %q, want xoxe-refresh", tok.RefreshToken)
	}
	if tok.ExpiresAt.IsZero() {
		t.Error("ExpiresAt should not be zero when expires_in > 0")
	}
}

func TestExchangeCode_Success_TopLevel(t *testing.T) {
	// Some responses put the token at the top level (refresh flow format).
	srv := mockTokenServer(t, map[string]any{
		"ok":            true,
		"access_token":  "xoxe.xoxp-top",
		"refresh_token": "xoxe-top-rt",
		"expires_in":    3600,
	})
	withMockOAuth(t, srv)

	tok, err := ExchangeCode("CLIENT", "code456", "verifier", "http://localhost/cb")
	if err != nil {
		t.Fatalf("ExchangeCode: %v", err)
	}
	if tok.AccessToken != "xoxe.xoxp-top" {
		t.Errorf("AccessToken: got %q, want xoxe.xoxp-top", tok.AccessToken)
	}
}

func TestExchangeCode_OAuthError(t *testing.T) {
	srv := mockTokenServer(t, map[string]any{"ok": false, "error": "invalid_code"})
	withMockOAuth(t, srv)

	_, err := ExchangeCode("CLIENT", "bad-code", "verifier", "http://localhost/cb")
	if err == nil {
		t.Fatal("expected error for invalid_code")
	}
}

func TestExchangeCode_NoExpiry(t *testing.T) {
	// expires_in == 0 → ExpiresAt stays zero (non-rotating token).
	srv := mockTokenServer(t, map[string]any{
		"ok":           true,
		"access_token": "xoxp-noexpiry",
	})
	withMockOAuth(t, srv)

	tok, err := ExchangeCode("CLIENT", "code", "verifier", "http://localhost/cb")
	if err != nil {
		t.Fatal(err)
	}
	if !tok.ExpiresAt.IsZero() {
		t.Error("ExpiresAt should be zero when expires_in is 0")
	}
}

// ---- Refresh ----

func TestRefresh_Success(t *testing.T) {
	srv := mockTokenServer(t, map[string]any{
		"ok":            true,
		"access_token":  "xoxe.xoxp-rotated",
		"refresh_token": "xoxe-new-rt",
		"expires_in":    43200,
	})
	withMockOAuth(t, srv)

	tok, err := Refresh("CLIENT", "xoxe-old-rt")
	if err != nil {
		t.Fatalf("Refresh: %v", err)
	}
	if tok.AccessToken != "xoxe.xoxp-rotated" {
		t.Errorf("AccessToken: got %q, want xoxe.xoxp-rotated", tok.AccessToken)
	}
	if tok.RefreshToken != "xoxe-new-rt" {
		t.Errorf("RefreshToken: got %q, want xoxe-new-rt", tok.RefreshToken)
	}
}

func TestRefresh_InvalidToken(t *testing.T) {
	srv := mockTokenServer(t, map[string]any{"ok": false, "error": "invalid_refresh_token"})
	withMockOAuth(t, srv)

	_, err := Refresh("CLIENT", "xoxe-expired")
	if err != ErrTokenExpired {
		t.Fatalf("want ErrTokenExpired, got %v", err)
	}
}

func TestRefresh_TokenRevoked(t *testing.T) {
	srv := mockTokenServer(t, map[string]any{"ok": false, "error": "token_revoked"})
	withMockOAuth(t, srv)

	_, err := Refresh("CLIENT", "xoxe-revoked")
	if err != ErrTokenExpired {
		t.Fatalf("want ErrTokenExpired, got %v", err)
	}
}

func TestRefresh_TokenExpiredError(t *testing.T) {
	srv := mockTokenServer(t, map[string]any{"ok": false, "error": "token_expired"})
	withMockOAuth(t, srv)

	_, err := Refresh("CLIENT", "xoxe-expired")
	if err != ErrTokenExpired {
		t.Fatalf("want ErrTokenExpired, got %v", err)
	}
}

func TestRefresh_UnknownError(t *testing.T) {
	srv := mockTokenServer(t, map[string]any{"ok": false, "error": "not_authed"})
	withMockOAuth(t, srv)

	_, err := Refresh("CLIENT", "xoxe-bad")
	if err == nil || err == ErrTokenExpired {
		t.Fatalf("want generic error, got %v", err)
	}
}

func TestCallTokenEndpoint_InvalidJSON(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Write([]byte("not json")) //nolint:errcheck
	}))
	t.Cleanup(srv.Close)
	withMockOAuth(t, srv)

	_, err := ExchangeCode("CLIENT", "code", "verifier", "http://localhost/cb")
	if err == nil {
		t.Fatal("expected error for malformed JSON response")
	}
}

// ---- EnsureValid ----

func TestEnsureValid_AlreadyValid(t *testing.T) {
	isolateConfigDir(t)

	stored := &Token{
		AccessToken:  "xoxp-still-valid",
		RefreshToken: "xoxe-rt",
		ExpiresAt:    time.Now().Add(1 * time.Hour),
	}
	if err := Save(stored); err != nil {
		t.Fatal(err)
	}

	// Point OAuth at an unreachable address — EnsureValid must not call it.
	orig := oauthBaseURL
	oauthBaseURL = "http://127.0.0.1:1" // closed port
	defer func() { oauthBaseURL = orig }()

	tok, err := EnsureValid("CLIENT")
	if err != nil {
		t.Fatalf("EnsureValid: %v", err)
	}
	if tok.AccessToken != "xoxp-still-valid" {
		t.Errorf("AccessToken: got %q, want xoxp-still-valid", tok.AccessToken)
	}
}

func TestEnsureValid_Refreshes(t *testing.T) {
	isolateConfigDir(t)

	expired := &Token{
		AccessToken:  "xoxp-old",
		RefreshToken: "xoxe-old-rt",
		ExpiresAt:    time.Now().Add(-1 * time.Hour),
	}
	if err := Save(expired); err != nil {
		t.Fatal(err)
	}

	srv := mockTokenServer(t, map[string]any{
		"ok":            true,
		"access_token":  "xoxe.xoxp-refreshed",
		"refresh_token": "xoxe-new-rt",
		"expires_in":    43200,
	})
	withMockOAuth(t, srv)

	tok, err := EnsureValid("CLIENT")
	if err != nil {
		t.Fatalf("EnsureValid: %v", err)
	}
	if tok.AccessToken != "xoxe.xoxp-refreshed" {
		t.Errorf("AccessToken: got %q, want xoxe.xoxp-refreshed", tok.AccessToken)
	}

	// Verify the refreshed token was persisted.
	persisted, err := Load()
	if err != nil {
		t.Fatal(err)
	}
	if persisted.AccessToken != "xoxe.xoxp-refreshed" {
		t.Error("refreshed token should be persisted to disk")
	}
}

func TestEnsureValid_NoToken(t *testing.T) {
	isolateConfigDir(t)

	_, err := EnsureValid("CLIENT")
	if err != ErrNoToken {
		t.Fatalf("want ErrNoToken, got %v", err)
	}
}

func TestEnsureValid_NoRefreshToken(t *testing.T) {
	isolateConfigDir(t)

	// Expired token with no refresh_token.
	if err := Save(&Token{
		AccessToken: "xoxp-expired",
		ExpiresAt:   time.Now().Add(-1 * time.Hour),
	}); err != nil {
		t.Fatal(err)
	}

	_, err := EnsureValid("CLIENT")
	if err != ErrTokenExpired {
		t.Fatalf("want ErrTokenExpired, got %v", err)
	}
}
