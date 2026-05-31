package cmd

import (
	"context"
	"fmt"
	"net"
	"net/http"
	"net/url"
	"os"
	"os/exec"
	"runtime"
	"time"

	"github.com/spf13/cobra"
	"github.com/uji/slack-wait/internal/auth"
)

var authCmd = &cobra.Command{
	Use:   "auth",
	Short: "Manage Slack authentication",
}

var authLoginCmd = &cobra.Command{
	Use:          "login",
	Short:        "Authenticate via browser (PKCE, no client secret)",
	SilenceUsage: true,
	RunE:         runAuthLogin,
}

var authLogoutCmd = &cobra.Command{
	Use:          "logout",
	Short:        "Remove stored token",
	SilenceUsage: true,
	RunE:         runAuthLogout,
}

func init() {
	authCmd.AddCommand(authLoginCmd, authLogoutCmd)
}

func runAuthLogin(_ *cobra.Command, _ []string) error {
	verifier, err := auth.GenerateVerifier()
	if err != nil {
		return err
	}
	challenge := auth.Challenge(verifier)

	// Grab a free port without a race: keep the listener and pass it to Serve.
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		return fmt.Errorf("cannot open local port: %w", err)
	}
	port := ln.Addr().(*net.TCPAddr).Port
	redirectURI := fmt.Sprintf("http://localhost:%d/callback", port)

	authURL := "https://slack.com/oauth/v2/authorize?" + url.Values{
		"client_id":             {clientID},
		"scope":                 {""}, // no bot scopes
		"user_scope":            {userScopes},
		"redirect_uri":          {redirectURI},
		"code_challenge":        {challenge},
		"code_challenge_method": {"S256"},
	}.Encode()

	codeCh := make(chan string, 1)
	errCh := make(chan error, 1)

	mux := http.NewServeMux()
	srv := &http.Server{Handler: mux}

	mux.HandleFunc("/callback", func(w http.ResponseWriter, r *http.Request) {
		if e := r.URL.Query().Get("error"); e != "" {
			fmt.Fprintf(w, "Authentication failed: %s. You can close this window.", e)
			errCh <- fmt.Errorf("auth denied: %s", e)
			return
		}
		fmt.Fprint(w, "Authentication successful! You can close this window.")
		codeCh <- r.URL.Query().Get("code")
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

	token, err := auth.ExchangeCode(clientID, code, verifier, redirectURI)
	if err != nil {
		return fmt.Errorf("token exchange failed: %w", err)
	}
	if err := auth.Save(token); err != nil {
		return fmt.Errorf("save token: %w", err)
	}
	fmt.Fprintln(os.Stderr, "Authenticated. Token saved to ~/.config/slack-wait/token.json")
	return nil
}

func runAuthLogout(_ *cobra.Command, _ []string) error {
	if err := auth.Delete(); err != nil {
		return err
	}
	fmt.Fprintln(os.Stderr, "Logged out.")
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
