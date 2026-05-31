package slack

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
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
