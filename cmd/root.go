package cmd

import (
	"context"
	"encoding/json"
	"flag"
	"fmt"
	"io"
	"os"
	"time"

	"github.com/google/subcommands"
	"github.com/uji/slack-wait/internal/auth"
	"github.com/uji/slack-wait/internal/config"
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

// resolveClientID returns the client ID to use for OAuth, in priority order:
// 1. config.json (written by `slack-wait login --client-id`)
// 2. ldflags-embedded clientID (for self-distributed builds)
func resolveClientID() (string, error) {
	cfg, err := config.Load()
	if err != nil {
		return "", fmt.Errorf("load config: %w", err)
	}
	if cfg.ClientID != "" {
		return cfg.ClientID, nil
	}
	if clientID != "" && clientID != "YOUR_SLACK_CLIENT_ID" {
		return clientID, nil
	}
	return "", fmt.Errorf("no Slack client ID configured — run: slack-wait login --client-id <YOUR_CLIENT_ID>")
}

// WaitCommand waits for new Slack messages and emits them as NDJSON.
type WaitCommand struct {
	channel  string
	since    string
	thread_ts string
	interval time.Duration
	timeout  time.Duration
}

func (*WaitCommand) Name() string { return "wait" }
func (*WaitCommand) Synopsis() string {
	return "wait for new Slack messages and emit them as NDJSON"
}
func (*WaitCommand) Usage() string {
	return `slack-wait --channel <ID> [--since <ts>] [--thread_ts <ts>] [--interval <dur>] [--timeout <dur>]

Polls a Slack channel (or thread) for messages newer than --since.
When at least one new message arrives it prints all of them as NDJSON and exits 0.
On timeout it exits 124. No state is stored; the caller tracks position via --since.

`
}

func (w *WaitCommand) SetFlags(f *flag.FlagSet) {
	f.StringVar(&w.channel, "channel", "", "Slack channel ID, e.g. C01234ABCDE (required)")
	f.StringVar(&w.since, "since", "", "Slack timestamp; wait for messages strictly newer than this")
	f.StringVar(&w.thread_ts, "thread_ts", "", "Thread timestamp; poll replies instead of channel history")
	f.DurationVar(&w.interval, "interval", 5*time.Second, "Polling interval")
	f.DurationVar(&w.timeout, "timeout", 0, "Maximum wait time before exiting 124 (0,default = wait forever)")
}

func (w *WaitCommand) Execute(_ context.Context, f *flag.FlagSet, _ ...any) subcommands.ExitStatus {
	if w.channel == "" {
		fmt.Fprint(os.Stderr, w.Usage())
		f.PrintDefaults()
		return subcommands.ExitUsageError
	}
	if w.since == "" {
		w.since = fmt.Sprintf("%.6f", float64(time.Now().UnixNano())/1e9)
	}
	id, err := resolveClientID()
	if err != nil {
		fmt.Fprintf(os.Stderr, "slack-wait: %v\n", err)
		return subcommands.ExitFailure
	}
	token, err := auth.EnsureValid(id)
	if err != nil {
		fmt.Fprintf(os.Stderr, "slack-wait: %v\n", err)
		return subcommands.ExitFailure
	}
	client := slack.New(token.AccessToken)
	fetch := func() ([]json.RawMessage, error) {
		if w.thread_ts != "" {
			return client.Replies(w.channel, w.thread_ts, w.since)
		}
		return client.History(w.channel, w.since)
	}
	if code := pollLoop(fetch, w.interval, w.timeout, os.Stdout, os.Stderr); code != 0 {
		os.Exit(code)
	}
	return subcommands.ExitSuccess
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

	// A nil channel blocks forever in select, giving us infinite-wait when timeout==0.
	var timeoutCh <-chan time.Time
	if timeout > 0 {
		timeoutCh = time.After(timeout)
	}
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
