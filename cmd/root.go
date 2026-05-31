package cmd

import (
	"encoding/json"
	"fmt"
	"io"
	"os"
	"time"

	"github.com/spf13/cobra"
	"github.com/uji/slack-wait/internal/auth"
	"github.com/uji/slack-wait/internal/slack"
)

// clientID is the Slack app's Client ID, embedded at build time:
//
//	go build -ldflags "-X github.com/uji/slack-wait/cmd.clientID=<YOUR_CLIENT_ID>"
//
// The app is registered once by the tool author; users only need browser consent.
// No client_secret is used (public-client PKCE flow).
var clientID = "YOUR_SLACK_CLIENT_ID"

// userScopes are the user-token scopes requested during OAuth.
const userScopes = "channels:history,groups:history,im:history,mpim:history"

// exitCodeTimeout matches the convention used by GNU coreutils `timeout`.
const exitCodeTimeout = 124

var (
	flagChannel  string
	flagSince    string
	flagThread   string
	flagInterval time.Duration
	flagTimeout  time.Duration
)

var rootCmd = &cobra.Command{
	Use:   "slack-wait",
	Short: "Wait for new Slack messages and emit them as NDJSON",
	Long: `slack-wait polls a Slack channel (or thread) for messages newer than --since.
When at least one new message arrives it prints all of them as NDJSON and exits 0.
On timeout it exits 124. No state is stored; the caller tracks position via --since.`,
	SilenceUsage: true,
	RunE:         runWait,
}

func init() {
	rootCmd.AddCommand(authCmd)

	f := rootCmd.Flags()
	f.StringVar(&flagChannel, "channel", "", "Slack channel ID, e.g. C01234ABCDE (required)")
	f.StringVar(&flagSince, "since", "", "Slack timestamp; wait for messages strictly newer than this (required)")
	f.StringVar(&flagThread, "thread", "", "Thread timestamp; poll replies instead of channel history")
	f.DurationVar(&flagInterval, "interval", 5*time.Second, "Polling interval")
	f.DurationVar(&flagTimeout, "timeout", 5*time.Minute, "Maximum wait time before exiting 124")
}

func Execute() {
	if err := rootCmd.Execute(); err != nil {
		os.Exit(1)
	}
}

func runWait(cmd *cobra.Command, _ []string) error {
	if flagChannel == "" || flagSince == "" {
		return cmd.Help()
	}
	token, err := auth.EnsureValid(clientID)
	if err != nil {
		return err
	}
	client := slack.New(token.AccessToken)
	fetch := func() ([]json.RawMessage, error) {
		if flagThread != "" {
			return client.Replies(flagChannel, flagThread, flagSince)
		}
		return client.History(flagChannel, flagSince)
	}
	if code := pollLoop(fetch, flagInterval, flagTimeout, os.Stdout, os.Stderr); code != 0 {
		os.Exit(code)
	}
	return nil
}

// pollLoop is the testable core of the wait command.
// It polls fetch immediately and then on each interval tick.
// Returns 0 when messages are found, exitCodeTimeout on timeout.
func pollLoop(
	fetch func() ([]json.RawMessage, error),
	interval, timeout time.Duration,
	stdout, stderr io.Writer,
) int {
	emit := func(msgs []json.RawMessage) {
		enc := json.NewEncoder(stdout)
		for _, m := range msgs {
			enc.Encode(m) //nolint:errcheck
		}
	}

	// Immediate first poll: avoid waiting a full interval when messages are ready.
	if msgs, err := fetch(); err != nil {
		fmt.Fprintf(stderr, "slack-wait: %v\n", err)
	} else if len(msgs) > 0 {
		emit(msgs)
		return 0
	}

	timeoutCh := time.After(timeout)
	ticker := time.NewTicker(interval)
	defer ticker.Stop()

	for {
		select {
		case <-timeoutCh:
			return exitCodeTimeout
		case <-ticker.C:
			msgs, err := fetch()
			if err != nil {
				fmt.Fprintf(stderr, "slack-wait: %v\n", err)
				continue
			}
			if len(msgs) > 0 {
				emit(msgs)
				return 0
			}
		}
	}
}
