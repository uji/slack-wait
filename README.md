# slack-wait

A one-shot CLI that waits for new messages in a Slack channel or thread and
prints them as NDJSON to stdout, then exits. Designed for agent loops where
the caller—not the tool—owns state.

```
slack-wait --channel C01234ABCDE --since 1748000000.000000
```

## How it works

`slack-wait` polls the Slack API (`conversations.history` or
`conversations.replies`) until at least one message newer than `--since`
appears. When messages arrive it prints every one of them—one raw Slack
message object per line—and exits. It never stores state; the caller tracks
position by saving the latest `ts` and passing it back as `--since` on the
next invocation.

```
┌─ agent ──────────────────────────────────────────────────┐
│  ts = "1748000000.000000"   # last seen message           │
│                                                           │
│  loop:                                                    │
│    msgs = $(slack-wait --channel C… --since $ts)          │
│    process $msgs                                          │
│    ts = last ts in $msgs                                  │
└───────────────────────────────────────────────────────────┘
```

## Exit codes

| Code | Meaning |
|------|---------|
| `0`  | One or more new messages were found and printed |
| `124`| Timed out with no new messages (matches `coreutils timeout`) |
| `1`  | Misconfiguration or authentication error |

## Installation

```sh
go install github.com/uji/slack-wait@latest
```

Or build from source with your Client ID embedded (see [Authentication](#authentication)):

```sh
go build -ldflags "-X github.com/uji/slack-wait/cmd.clientID=YOUR_CLIENT_ID" \
  -o slack-wait .
```

## Usage

### Wait for channel messages

```sh
slack-wait --channel C01234ABCDE --since 1748000000.000000
```

### Wait for thread replies

```sh
slack-wait --channel C01234ABCDE \
           --since  1748000001.000000 \
           --thread 1748000000.000000
```

### Flags

| Flag | Default | Description |
|------|---------|-------------|
| `--channel` | *(required)* | Slack channel ID (`C…`) |
| `--since` | *(required)* | Slack timestamp; only messages strictly newer than this are returned |
| `--thread` | — | Thread timestamp (`ts` of the parent); switches to `conversations.replies` |
| `--interval` | `5s` | How often to poll |
| `--timeout` | `5m` | Give up and exit 124 after this duration |

### Output format

Each message is printed as a single JSON object (raw Slack message payload)
followed by a newline—standard [NDJSON](https://ndjson.org/). Messages are
ordered oldest-first.

```jsonl
{"type":"message","ts":"1748000001.000100","user":"U04ABC","text":"hello"}
{"type":"message","ts":"1748000002.000200","user":"U04ABC","text":"world","thread_ts":"1748000001.000100"}
```

Fields depend on the Slack message type; `slack-wait` passes them through
without modification. Refer to the
[Slack message object reference](https://api.slack.com/events/message) for the
full schema.

## Authentication

`slack-wait` uses **PKCE OAuth 2.0** (RFC 7636) with no `client_secret`. The
app is registered once by the tool author; end-users only click "Allow" in
their browser.

### First-time login

```sh
slack-wait login
```

A browser window opens to the Slack authorization page. After you allow
access, the token is saved to `~/.config/slack-wait/token.json` with
permissions `0600`.

### Logout

```sh
slack-wait logout
```

Removes the stored token.

### Token rotation

When token rotation is enabled on the Slack app, `slack-wait` automatically
refreshes the access token using the stored refresh token before each run. If
the refresh token itself has expired (default: 30 days of inactivity), you will
be prompted to run `slack-wait login` again.

### Scopes requested

| Scope | Purpose |
|-------|---------|
| `channels:history` | Public channels |
| `groups:history` | Private channels |
| `im:history` | Direct messages |
| `mpim:history` | Group direct messages |

These are **user token** scopes (`xoxp-`). No bot scopes are required.

## Building with your own Client ID

The Slack app's Client ID is baked into the binary at build time. If you are
distributing your own build, register a Slack app with:

- **Redirect URL**: `http://localhost` (any port; the CLI picks a free port at runtime)
- **User token scopes**: the four listed above
- **Token rotation**: enabled (recommended)
- **No client secret distribution**—this is intentional; PKCE provides the
  code integrity without needing a secret

Then build:

```sh
go build \
  -ldflags "-X github.com/uji/slack-wait/cmd.clientID=<YOUR_CLIENT_ID>" \
  -o slack-wait .
```

## Design notes

**Stateless by design.** `slack-wait` does not record what it has seen. The
caller is responsible for saving and advancing `--since`. This makes it trivial
to use from shell scripts, agents, or any orchestration layer without worrying
about hidden state files or deduplication bugs.

**Polling, not WebSocket.** Socket Mode requires a bot token and a different
auth flow. PKCE user tokens cannot drive Socket Mode, and the polling approach
works well for the `conversations.history` Tier-3 rate limit (~50 req/min).

**Unix-composable.** The output is plain NDJSON on stdout; the exit code
signals outcome. Filter, transform, and pipe as needed:

```sh
# Extract only the text of new messages
slack-wait --channel C… --since $ts | jq -r '.text'

# Collect messages into a JSON array
slack-wait --channel C… --since $ts | jq -s '.'
```

**Future: MCP interface.** The tool is designed so that a thin MCP wrapper
can expose `slack-wait` as a tool callable by MCP clients without changing
the core polling logic.

## Development

```sh
# Run tests
go test ./...

# Run tests with coverage
go test -coverprofile=cover.out ./... && go tool cover -html=cover.out

# Vet
go vet ./...
```

## License

MIT — see [LICENSE](LICENSE).
