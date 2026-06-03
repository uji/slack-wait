package auth

import (
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"sync"
	"time"

	"github.com/99designs/keyring"
)

// Token holds the Slack OAuth credentials. The access token and its expiry are
// kept in process memory only; see persisted for what is actually written to
// the keyring.
type Token struct {
	AccessToken  string    `json:"access_token"`
	RefreshToken string    `json:"refresh_token"`
	ExpiresAt    time.Time `json:"expires_at"`
}

// persisted is the keyring representation of a Token.
//
// For rotating apps (token_rotation_enabled) the short-lived access token is
// deliberately NOT stored: it lives only in process memory and is recreated
// from the refresh token on the next run. Only the long-lived, rotating
// refresh token is persisted.
//
// For non-rotating apps there is no refresh token and the access token never
// expires, so it is the only credential available and must be stored.
type persisted struct {
	AccessToken  string `json:"access_token,omitempty"`
	RefreshToken string `json:"refresh_token,omitempty"`
}

var (
	ErrNoToken      = errors.New("not authenticated; run 'slack-wait login'")
	ErrTokenExpired = errors.New("refresh token expired; run 'slack-wait login'")
)

const (
	keyringService = "slack-wait"
	keyringKey     = "token"
)

// keyringOpen is the factory used to obtain the keyring; replaced in tests.
var keyringOpen = func() (keyring.Keyring, error) {
	return keyring.Open(keyring.Config{
		ServiceName:              keyringService,
		KeychainTrustApplication: true,
	})
}

func Load() (*Token, error) {
	ring, err := keyringOpen()
	if err != nil {
		return nil, fmt.Errorf("keyring: %w", err)
	}
	item, err := ring.Get(keyringKey)
	if errors.Is(err, keyring.ErrKeyNotFound) {
		return nil, ErrNoToken
	}
	if err != nil {
		return nil, fmt.Errorf("keyring get: %w", err)
	}
	var p persisted
	if err := json.Unmarshal(item.Data, &p); err != nil {
		return nil, fmt.Errorf("corrupt keyring entry: %w", err)
	}
	return &Token{AccessToken: p.AccessToken, RefreshToken: p.RefreshToken}, nil
}

func Save(t *Token) error {
	ring, err := keyringOpen()
	if err != nil {
		return fmt.Errorf("keyring: %w", err)
	}
	pt := persisted{RefreshToken: t.RefreshToken}
	if t.RefreshToken == "" {
		pt.AccessToken = t.AccessToken
	}
	data, err := json.Marshal(pt)
	if err != nil {
		return err
	}
	return ring.Set(keyring.Item{
		Key:   keyringKey,
		Data:  data,
		Label: "slack-wait Slack token",
	})
}

func Delete() error {
	ring, err := keyringOpen()
	if err != nil {
		return fmt.Errorf("keyring: %w", err)
	}
	err = ring.Remove(keyringKey)
	if errors.Is(err, keyring.ErrKeyNotFound) {
		return nil
	}
	return err
}

// Valid reports whether the access token is usable (present and not expiring
// within 5 minutes). A zero ExpiresAt means the token has no known expiry
// (non-rotating app). An empty AccessToken is never valid — this is the state
// of a freshly loaded rotating token, which must be refreshed before use.
func (t *Token) Valid() bool {
	if t.AccessToken == "" {
		return false
	}
	if t.ExpiresAt.IsZero() {
		return true
	}
	return time.Now().Add(5 * time.Minute).Before(t.ExpiresAt)
}

// tokenResponse covers both initial exchange and refresh responses from Slack.
// Initial user-token exchange nests credentials under authed_user;
// refresh responses place them at the top level.
type tokenResponse struct {
	OK           bool   `json:"ok"`
	Error        string `json:"error"`
	AccessToken  string `json:"access_token"`
	RefreshToken string `json:"refresh_token"`
	ExpiresIn    int    `json:"expires_in"`
	AuthedUser   struct {
		AccessToken  string `json:"access_token"`
		RefreshToken string `json:"refresh_token"`
		ExpiresIn    int    `json:"expires_in"`
	} `json:"authed_user"`
}

func (r *tokenResponse) toToken() *Token {
	at, rt, ei := r.AccessToken, r.RefreshToken, r.ExpiresIn
	if r.AuthedUser.AccessToken != "" {
		at = r.AuthedUser.AccessToken
		rt = r.AuthedUser.RefreshToken
		ei = r.AuthedUser.ExpiresIn
	}
	var expiresAt time.Time
	if ei > 0 {
		expiresAt = time.Now().Add(time.Duration(ei) * time.Second)
	}
	return &Token{AccessToken: at, RefreshToken: rt, ExpiresAt: expiresAt}
}

// ExchangeCode trades an authorization code (plus PKCE verifier) for a token.
// No client_secret is sent; this is a public-client flow.
func ExchangeCode(clientID, code, verifier, redirectURI string) (*Token, error) {
	return callTokenEndpoint(url.Values{
		"client_id":     {clientID},
		"code":          {code},
		"redirect_uri":  {redirectURI},
		"code_verifier": {verifier},
	})
}

// Refresh exchanges a refresh_token for a new token pair.
func Refresh(clientID, refreshToken string) (*Token, error) {
	return callTokenEndpoint(url.Values{
		"client_id":     {clientID},
		"grant_type":    {"refresh_token"},
		"refresh_token": {refreshToken},
	})
}

// oauthBaseURL is the Slack API base; overridden in tests to point at a mock server.
var oauthBaseURL = "https://slack.com"

func callTokenEndpoint(params url.Values) (*Token, error) {
	resp, err := http.PostForm(oauthBaseURL+"/api/oauth.v2.access", params)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, err
	}
	var r tokenResponse
	if err := json.Unmarshal(body, &r); err != nil {
		return nil, fmt.Errorf("unexpected token response: %w", err)
	}
	if !r.OK {
		if r.Error == "invalid_refresh_token" || r.Error == "token_expired" || r.Error == "token_revoked" {
			return nil, ErrTokenExpired
		}
		return nil, fmt.Errorf("slack oauth error: %s", r.Error)
	}
	return r.toToken(), nil
}

// Session keeps the active token in process memory and refreshes it on demand.
// The access token never touches the keyring for rotating apps; only the
// rotating refresh token is persisted, so a long-running `wait` can keep
// refreshing the access token transparently while it waits.
type Session struct {
	clientID string
	mu       sync.Mutex
	token    *Token
}

// NewSession loads the stored credentials and prepares an in-memory session.
func NewSession(clientID string) (*Session, error) {
	t, err := Load()
	if err != nil {
		return nil, err
	}
	return &Session{clientID: clientID, token: t}, nil
}

// Token returns a currently-valid access token, refreshing transparently when
// the cached one is missing or close to expiry. The rotated refresh token is
// persisted on every refresh so the next run can authenticate without a
// browser. Safe for concurrent use.
func (s *Session) Token() (string, error) {
	s.mu.Lock()
	defer s.mu.Unlock()

	if s.token.Valid() {
		return s.token.AccessToken, nil
	}
	if s.token.RefreshToken == "" {
		return "", ErrTokenExpired
	}
	t, err := Refresh(s.clientID, s.token.RefreshToken)
	if err != nil {
		return "", err
	}
	s.token = t
	if err := Save(t); err != nil {
		return "", err
	}
	return t.AccessToken, nil
}
