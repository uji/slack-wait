package cmd

import (
	"context"
	"crypto/subtle"
	"flag"
	"fmt"
	"net"
	"net/http"
	"net/url"
	"os"
	"os/exec"
	"runtime"
	"time"

	"github.com/google/subcommands"
	"github.com/uji/slack-wait/internal/auth"
	"github.com/uji/slack-wait/internal/config"
)

// LoginCommand authenticates via browser using PKCE.
type LoginCommand struct {
	clientIDFlag string
}

func (*LoginCommand) Name() string     { return "login" }
func (*LoginCommand) Synopsis() string { return "authenticate via browser (PKCE, no client secret)" }
func (*LoginCommand) Usage() string    { return "login\n\n" }
func (l *LoginCommand) SetFlags(f *flag.FlagSet) {
	f.StringVar(&l.clientIDFlag, "client-id", "", "Slack App Client ID to save and use for authentication")
}

func (l *LoginCommand) Execute(_ context.Context, _ *flag.FlagSet, _ ...any) subcommands.ExitStatus {
	if err := runLogin(l.clientIDFlag); err != nil {
		fmt.Fprintf(os.Stderr, "slack-wait: %v\n", err)
		return subcommands.ExitFailure
	}
	return subcommands.ExitSuccess
}

func runLogin(clientIDArg string) error {
	if clientIDArg != "" {
		if err := config.Save(&config.Config{ClientID: clientIDArg}); err != nil {
			return fmt.Errorf("save client ID: %w", err)
		}
	}
	id, err := resolveClientID()
	if err != nil {
		return err
	}

	verifier, err := auth.GenerateVerifier()
	if err != nil {
		return err
	}
	challenge := auth.Challenge(verifier)

	state, err := auth.GenerateVerifier()
	if err != nil {
		return err
	}

	// Try ports 49490-49499 in order; all must be registered in the Slack app's redirect URIs.
	var ln net.Listener
	for p := 49490; p <= 49499; p++ {
		var err error
		ln, err = net.Listen("tcp", fmt.Sprintf("127.0.0.1:%d", p))
		if err == nil {
			break
		}
		if p == 49499 {
			return fmt.Errorf("cannot open local port (tried 49490-49499): %w", err)
		}
	}
	port := ln.Addr().(*net.TCPAddr).Port
	redirectURI := fmt.Sprintf("http://localhost:%d/callback", port)

	authURL := "https://slack.com/oauth/v2/authorize?" + url.Values{
		"client_id":             {id},
		"scope":                 {""}, // no bot scopes
		"user_scope":            {userScopes},
		"redirect_uri":          {redirectURI},
		"code_challenge":        {challenge},
		"code_challenge_method": {"S256"},
		"state":                 {state},
	}.Encode()

	codeCh := make(chan string, 1)
	errCh := make(chan error, 1)

	mux := http.NewServeMux()
	srv := &http.Server{Handler: mux}

	mux.HandleFunc("/callback", func(w http.ResponseWriter, r *http.Request) {
		host, _, _ := net.SplitHostPort(r.Host)
		if host != "127.0.0.1" && host != "localhost" {
			http.Error(w, "invalid host", http.StatusBadRequest)
			return
		}
		q := r.URL.Query()
		if e := q.Get("error"); e != "" {
			fmt.Fprintf(w, "Authentication failed: %s. You can close this window.", e)
			errCh <- fmt.Errorf("auth denied: %s", e)
			return
		}
		if subtle.ConstantTimeCompare([]byte(q.Get("state")), []byte(state)) != 1 {
			http.Error(w, "state mismatch", http.StatusBadRequest)
			errCh <- fmt.Errorf("state mismatch (possible CSRF)")
			return
		}
		code := q.Get("code")
		if code == "" {
			http.Error(w, "missing code", http.StatusBadRequest)
			return
		}
		fmt.Fprint(w, "Authentication successful! You can close this window.")
		codeCh <- code
	})

	go func() {
		if err := srv.Serve(ln); err != nil && err != http.ErrServerClosed {
			errCh <- err
		}
	}()

	fmt.Fprintln(os.Stderr, "Opening browser for Slack authentication…")
	fmt.Fprintln(os.Stderr, "If the browser does not open, visit:")
	fmt.Fprintln(os.Stderr, authURL)
	openBrowser(authURL)

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Minute)
	defer cancel()

	var code string
	select {
	case code = <-codeCh:
	case err := <-errCh:
		srv.Shutdown(context.Background()) //nolint:errcheck
		return err
	case <-ctx.Done():
		srv.Shutdown(context.Background()) //nolint:errcheck
		return fmt.Errorf("timed out waiting for browser authentication")
	}
	srv.Shutdown(context.Background()) //nolint:errcheck

	token, err := auth.ExchangeCode(id, code, verifier, redirectURI)
	if err != nil {
		return fmt.Errorf("token exchange failed: %w", err)
	}
	if err := auth.Save(token); err != nil {
		return fmt.Errorf("save token: %w", err)
	}
	fmt.Fprintln(os.Stderr, "Authenticated. Token saved to the OS keyring.")
	return nil
}

func openBrowser(rawURL string) {
	var cmd *exec.Cmd
	switch runtime.GOOS {
	case "darwin":
		cmd = exec.Command("open", rawURL)
	case "windows":
		cmd = exec.Command("rundll32", "url.dll,FileProtocolHandler", rawURL)
	default:
		cmd = exec.Command("xdg-open", rawURL)
	}
	_ = cmd.Start()
}
