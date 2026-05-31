package slack

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func makeMsg(ts string) json.RawMessage {
	b, _ := json.Marshal(map[string]string{"ts": ts, "text": "hello"})
	return json.RawMessage(b)
}

func newTestClient(t *testing.T, msgs []json.RawMessage) (*Client, *httptest.Server) {
	t.Helper()
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		json.NewEncoder(w).Encode(map[string]any{ //nolint:errcheck
			"ok":       true,
			"messages": msgs,
		})
	}))
	c := &Client{
		token:      "xoxp-test",
		httpClient: srv.Client(),
		baseURL:    srv.URL,
	}
	return c, srv
}

func TestHistoryReversal(t *testing.T) {
	// Slack sends newest-first; History must return oldest-first.
	slackOrder := []json.RawMessage{
		makeMsg("1700000003.000000"),
		makeMsg("1700000002.000000"),
		makeMsg("1700000001.000000"),
	}
	c, srv := newTestClient(t, slackOrder)
	defer srv.Close()

	msgs, err := c.History("C123", "1700000000.000000")
	if err != nil {
		t.Fatal(err)
	}
	if len(msgs) != 3 {
		t.Fatalf("want 3 messages, got %d", len(msgs))
	}
	if ts := tsOf(msgs[0]); ts != "1700000001.000000" {
		t.Fatalf("first message should be oldest; got ts=%s", ts)
	}
	if ts := tsOf(msgs[2]); ts != "1700000003.000000" {
		t.Fatalf("last message should be newest; got ts=%s", ts)
	}
}

func TestRepliesFiltering(t *testing.T) {
	threadTS := "1700000000.000000"
	oldest := "1700000001.000000"

	// Slack always returns the parent as first element.
	slackOrder := []json.RawMessage{
		makeMsg(threadTS),            // parent — must be filtered out
		makeMsg("1700000001.000000"), // == oldest — not strictly newer, filtered
		makeMsg("1700000002.000000"), // new reply — kept
		makeMsg("1700000003.000000"), // new reply — kept
	}
	c, srv := newTestClient(t, slackOrder)
	defer srv.Close()

	msgs, err := c.Replies("C123", threadTS, oldest)
	if err != nil {
		t.Fatal(err)
	}
	if len(msgs) != 2 {
		t.Fatalf("want 2 replies, got %d", len(msgs))
	}
	if ts := tsOf(msgs[0]); ts != "1700000002.000000" {
		t.Fatalf("unexpected first reply ts: %s", ts)
	}
}

func TestHistoryEmpty(t *testing.T) {
	c, srv := newTestClient(t, nil)
	defer srv.Close()

	msgs, err := c.History("C123", "1700000000.000000")
	if err != nil {
		t.Fatal(err)
	}
	if len(msgs) != 0 {
		t.Fatalf("expected empty result, got %d messages", len(msgs))
	}
}

func TestNew(t *testing.T) {
	c := New("xoxp-token")
	if c == nil {
		t.Fatal("New returned nil")
	}
	if c.token != "xoxp-token" {
		t.Errorf("token: got %q, want xoxp-token", c.token)
	}
	if c.baseURL == "" {
		t.Error("baseURL should be set")
	}
	if c.httpClient == nil {
		t.Error("httpClient should be set")
	}
}

func TestHistory_APIError(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		json.NewEncoder(w).Encode(map[string]any{"ok": false, "error": "channel_not_found"})
	}))
	defer srv.Close()

	c := &Client{token: "xoxp-test", httpClient: srv.Client(), baseURL: srv.URL}
	_, err := c.History("C_MISSING", "1700000000.000000")
	if err == nil {
		t.Fatal("expected error for API error response")
	}
	if !strings.Contains(err.Error(), "channel_not_found") {
		t.Errorf("error should mention channel_not_found; got %v", err)
	}
}

func TestHistory_InvalidJSON(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Write([]byte("not json")) //nolint:errcheck
	}))
	defer srv.Close()

	c := &Client{token: "xoxp-test", httpClient: srv.Client(), baseURL: srv.URL}
	_, err := c.History("C123", "1700000000.000000")
	if err == nil {
		t.Fatal("expected error for invalid JSON response")
	}
}

func TestReplies_APIError(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		json.NewEncoder(w).Encode(map[string]any{"ok": false, "error": "thread_not_found"})
	}))
	defer srv.Close()

	c := &Client{token: "xoxp-test", httpClient: srv.Client(), baseURL: srv.URL}
	_, err := c.Replies("C123", "1700000000.000000", "1700000001.000000")
	if err == nil {
		t.Fatal("expected error for API error response")
	}
	if !strings.Contains(err.Error(), "thread_not_found") {
		t.Errorf("error should mention thread_not_found; got %v", err)
	}
}
