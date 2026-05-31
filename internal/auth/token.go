package auth

import (
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"time"
)

// Token holds the Slack OAuth credentials persisted to disk.
type Token struct {
	AccessToken  string    `json:"access_token"`
	RefreshToken string    `json:"refresh_token"`
	ExpiresAt    time.Time `json:"expires_at"`
}

var (
	ErrNoToken      = errors.New("not authenticated; run 'slack-wait auth login'")
	ErrTokenExpired = errors.New("refresh token expired; run 'slack-wait auth login'")
)

func tokenPath() (string, error) {
	dir, err := os.UserConfigDir()
	if err != nil {
		return "", err
	}
	return filepath.Join(dir, "slack-wait", "token.json"), nil
}

func Load() (*Token, error) {
	p, err := tokenPath()
	if err != nil {
		return nil, err
	}
	f, err := os.Open(p)
	if errors.Is(err, os.ErrNotExist) {
		return nil, ErrNoToken
	}
	if err != nil {
		return nil, err
	}
	defer f.Close()
	var t Token
	if err := json.NewDecoder(f).Decode(&t); err != nil {
		return nil, fmt.Errorf("corrupt token file: %w", err)
	}
	return &t, nil
}

func Save(t *Token) error {
	p, err := tokenPath()
	if err != nil {
		return err
	}
	if err := os.MkdirAll(filepath.Dir(p), 0700); err != nil {
		return err
	}
	// 0600: owner read/write only
	f, err := os.OpenFile(p, os.O_WRONLY|os.O_CREATE|os.O_TRUNC, 0600)
	if err != nil {
		return err
	}
	defer f.Close()
	return json.NewEncoder(f).Encode(t)
}

func Delete() error {
	p, err := tokenPath()
	if err != nil {
		return err
	}
	err = os.Remove(p)
	if errors.Is(err, os.ErrNotExist) {
		return nil
	}
	return err
}

// Valid reports whether the access token is usable (not expiring within 5 minutes).
// A zero ExpiresAt means the token has no known expiry (non-rotating app).
func (t *Token) Valid() bool {
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

func callTokenEndpoint(params url.Values) (*Token, error) {
	resp, err := http.PostForm("https://slack.com/api/oauth.v2.access", params)
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

// EnsureValid loads the stored token, refreshes it if needed, and returns it.
func EnsureValid(clientID string) (*Token, error) {
	t, err := Load()
	if err != nil {
		return nil, err
	}
	if t.Valid() {
		return t, nil
	}
	if t.RefreshToken == "" {
		return nil, ErrTokenExpired
	}
	t, err = Refresh(clientID, t.RefreshToken)
	if err != nil {
		return nil, err
	}
	if err := Save(t); err != nil {
		return nil, err
	}
	return t, nil
}
