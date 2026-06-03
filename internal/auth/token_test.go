package auth

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/99designs/keyring"
)

// ---- in-memory keyring for tests ----

type memKeyring struct {
	mu    sync.Mutex
	items map[string]keyring.Item
}

func newMemKeyring() *memKeyring {
	return &memKeyring{items: make(map[string]keyring.Item)}
}

func (m *memKeyring) Get(key string) (keyring.Item, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	item, ok := m.items[key]
	if !ok {
		return keyring.Item{}, keyring.ErrKeyNotFound
	}
	return item, nil
}

func (m *memKeyring) GetMetadata(key string) (keyring.Metadata, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	if _, ok := m.items[key]; !ok {
		return keyring.Metadata{}, keyring.ErrKeyNotFound
	}
	return keyring.Metadata{}, nil
}

func (m *memKeyring) Set(item keyring.Item) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.items[item.Key] = item
	return nil
}

func (m *memKeyring) Remove(key string) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	if _, ok := m.items[key]; !ok {
		return keyring.ErrKeyNotFound
	}
	delete(m.items, key)
	return nil
}

func (m *memKeyring) Keys() ([]string, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	keys := make([]string, 0, len(m.items))
	for k := range m.items {
		keys = append(keys, k)
	}
	return keys, nil
}

// ---- test helpers ----

// isolateConfigDir wires up a fresh in-memory keyring for each test and
// returns it so callers can inspect raw contents.
func isolateConfigDir(t *testing.T) *memKeyring {
	t.Helper()
	ring := newMemKeyring()
	orig := keyringOpen
	keyringOpen = func() (keyring.Keyring, error) { return ring, nil }
	t.Cleanup(func() { keyringOpen = orig })
	return ring
}

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

func TestValid_EmptyAccessToken(t *testing.T) {
	// A freshly loaded rotating token has no access token; it must refresh.
	tok := &Token{RefreshToken: "xoxe-rt"}
	if tok.Valid() {
		t.Error("token with empty AccessToken should not be valid")
	}
}

// ---- Save / Load / Delete ----

func TestSaveLoad_Rotating(t *testing.T) {
	isolateConfigDir(t)

	// A rotating token: the access token and expiry are kept in memory only,
	// so Save must persist only the refresh token.
	if err := Save(&Token{
		AccessToken:  "xoxp-access",
		RefreshToken: "xoxe-refresh",
		ExpiresAt:    time.Now().Add(1 * time.Hour),
	}); err != nil {
		t.Fatalf("Save: %v", err)
	}

	got, err := Load()
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	if got.AccessToken != "" {
		t.Errorf("AccessToken should not be persisted for rotating tokens; got %q", got.AccessToken)
	}
	if got.RefreshToken != "xoxe-refresh" {
		t.Errorf("RefreshToken: got %q, want xoxe-refresh", got.RefreshToken)
	}
	if !got.ExpiresAt.IsZero() {
		t.Errorf("ExpiresAt should not be persisted; got %v", got.ExpiresAt)
	}
}

func TestSaveLoad_NonRotating(t *testing.T) {
	isolateConfigDir(t)

	// No refresh token (token rotation disabled): the access token is the only
	// credential and never expires, so it must be persisted.
	if err := Save(&Token{AccessToken: "xoxp-access"}); err != nil {
		t.Fatalf("Save: %v", err)
	}

	got, err := Load()
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	if got.AccessToken != "xoxp-access" {
		t.Errorf("AccessToken: got %q, want xoxp-access", got.AccessToken)
	}
	if got.RefreshToken != "" {
		t.Errorf("RefreshToken: got %q, want empty", got.RefreshToken)
	}
}

func TestSaveDoesNotStoreAccessToken(t *testing.T) {
	ring := isolateConfigDir(t)

	if err := Save(&Token{
		AccessToken:  "xoxp-secret",
		RefreshToken: "xoxe-refresh",
		ExpiresAt:    time.Now().Add(1 * time.Hour),
	}); err != nil {
		t.Fatalf("Save: %v", err)
	}

	item, err := ring.Get(keyringKey)
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(item.Data), "xoxp-secret") {
		t.Errorf("access token must not be stored in keyring; data: %s", item.Data)
	}
}

func TestLoad_Missing(t *testing.T) {
	isolateConfigDir(t)

	_, err := Load()
	if err != ErrNoToken {
		t.Fatalf("want ErrNoToken, got %v", err)
	}
}

func TestDelete_RemovesEntry(t *testing.T) {
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

	// Deleting a non-existent entry should not return an error.
	if err := Delete(); err != nil {
		t.Fatalf("Delete on missing entry: %v", err)
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

// ---- Session ----

func TestSession_AlreadyValid(t *testing.T) {
	// A still-valid in-memory access token is returned without contacting Slack.
	s := &Session{
		clientID: "CLIENT",
		token: &Token{
			AccessToken:  "xoxp-still-valid",
			RefreshToken: "xoxe-rt",
			ExpiresAt:    time.Now().Add(1 * time.Hour),
		},
	}

	orig := oauthBaseURL
	oauthBaseURL = "http://127.0.0.1:1" // closed port — must not be called
	defer func() { oauthBaseURL = orig }()

	tok, err := s.Token()
	if err != nil {
		t.Fatalf("Token: %v", err)
	}
	if tok != "xoxp-still-valid" {
		t.Errorf("token: got %q, want xoxp-still-valid", tok)
	}
}

func TestSession_RefreshesExpired(t *testing.T) {
	isolateConfigDir(t)

	srv := mockTokenServer(t, map[string]any{
		"ok":            true,
		"access_token":  "xoxe.xoxp-refreshed",
		"refresh_token": "xoxe-new-rt",
		"expires_in":    43200,
	})
	withMockOAuth(t, srv)

	s := &Session{
		clientID: "CLIENT",
		token: &Token{
			AccessToken:  "xoxp-old",
			RefreshToken: "xoxe-old-rt",
			ExpiresAt:    time.Now().Add(-1 * time.Hour),
		},
	}

	tok, err := s.Token()
	if err != nil {
		t.Fatalf("Token: %v", err)
	}
	if tok != "xoxe.xoxp-refreshed" {
		t.Errorf("token: got %q, want xoxe.xoxp-refreshed", tok)
	}

	// The rotated refresh token must be persisted; the access token must not.
	stored, err := Load()
	if err != nil {
		t.Fatal(err)
	}
	if stored.RefreshToken != "xoxe-new-rt" {
		t.Errorf("persisted RefreshToken: got %q, want xoxe-new-rt", stored.RefreshToken)
	}
	if stored.AccessToken != "" {
		t.Errorf("access token must not be persisted; got %q", stored.AccessToken)
	}
}

func TestSession_RefreshesWhenAccessTokenEmpty(t *testing.T) {
	isolateConfigDir(t)

	// Simulate a fresh start: only the refresh token is in the keyring.
	if err := Save(&Token{
		AccessToken:  "xoxp-not-persisted",
		RefreshToken: "xoxe-rt",
		ExpiresAt:    time.Now().Add(1 * time.Hour),
	}); err != nil {
		t.Fatal(err)
	}

	srv := mockTokenServer(t, map[string]any{
		"ok":            true,
		"access_token":  "xoxe.xoxp-fresh",
		"refresh_token": "xoxe-new-rt",
		"expires_in":    43200,
	})
	withMockOAuth(t, srv)

	s, err := NewSession("CLIENT")
	if err != nil {
		t.Fatalf("NewSession: %v", err)
	}
	tok, err := s.Token()
	if err != nil {
		t.Fatalf("Token: %v", err)
	}
	if tok != "xoxe.xoxp-fresh" {
		t.Errorf("token: got %q, want xoxe.xoxp-fresh", tok)
	}
}

func TestNewSession_NoToken(t *testing.T) {
	isolateConfigDir(t)

	_, err := NewSession("CLIENT")
	if err != ErrNoToken {
		t.Fatalf("want ErrNoToken, got %v", err)
	}
}

func TestSession_NoRefreshToken(t *testing.T) {
	// Expired access token with no refresh token cannot be refreshed.
	s := &Session{
		clientID: "CLIENT",
		token: &Token{
			AccessToken: "xoxp-expired",
			ExpiresAt:   time.Now().Add(-1 * time.Hour),
		},
	}

	_, err := s.Token()
	if err != ErrTokenExpired {
		t.Fatalf("want ErrTokenExpired, got %v", err)
	}
}
