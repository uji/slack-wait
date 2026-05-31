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

### 1. Install the CLI

```sh
go install github.com/uji/slack-wait@latest
```

### 2. Create a Slack App

1. Go to https://api.slack.com/apps and click **Create New App** → **From a manifest**
2. Select your workspace
3. Paste the following manifest:

```json
{
  "display_information": {
    "name": "Slack Wait"
  },
  "oauth_config": {
    "redirect_urls": [
      "http://localhost:49490/callback",
      "http://localhost:49491/callback",
      "http://localhost:49492/callback",
      "http://localhost:49493/callback",
      "http://localhost:49494/callback",
      "http://localhost:49495/callback",
      "http://localhost:49496/callback",
      "http://localhost:49497/callback",
      "http://localhost:49498/callback",
      "http://localhost:49499/callback"
    ],
    "scopes": {
      "user": [
        "channels:history",
        "groups:history",
        "im:history",
        "mpim:history"
      ]
    },
    "pkce_enabled": true
  },
  "settings": {
    "org_deploy_enabled": false,
    "socket_mode_enabled": false,
    "token_rotation_enabled": true,
    "is_mcp_enabled": false
  }
}
```

4. Click **Create** and then **Install to Workspace**
5. On the **Basic Information** page, copy the **Client ID**

### 3. Log in

```sh
slack-wait login --client-id <YOUR_CLIENT_ID>
```

A browser window opens to the Slack authorization page. After you click
**Allow**, the Client ID is saved to `~/.config/slack-wait/config.json` and
the token is saved to `~/.config/slack-wait/token.json` (both `0600`).

On subsequent runs, `--client-id` can be omitted:

```sh
slack-wait login
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

`slack-wait` uses **PKCE OAuth 2.0** (RFC 7636) with no `client_secret`.

### Logout

```sh
slack-wait logout
```

Removes the stored token.

### Token rotation

`slack-wait` automatically refreshes the access token using the stored refresh
token before each run. If the refresh token itself has expired (default: 30
days of inactivity), you will be prompted to run `slack-wait login` again.

### Scopes requested

| Scope | Purpose |
|-------|---------|
| `channels:history` | Public channels |
| `groups:history` | Private channels |
| `im:history` | Direct messages |
| `mpim:history` | Group direct messages |

These are **user token** scopes (`xoxp-`). No bot scopes are required.

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

## License

MIT — see [LICENSE](LICENSE).
