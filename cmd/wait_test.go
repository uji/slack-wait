package cmd

import (
	"bytes"
	"encoding/json"
	"errors"
	"io"
	"strings"
	"testing"
	"time"
)

// shortIntervals reduces real-time waits in tests.
const (
	testInterval = 20 * time.Millisecond
	testTimeout  = 500 * time.Millisecond
)

func rawMsg(ts, text string) json.RawMessage {
	b, _ := json.Marshal(map[string]string{"ts": ts, "text": text})
	return json.RawMessage(b)
}

func ndjsonLines(t *testing.T, out *bytes.Buffer) []map[string]string {
	t.Helper()
	var lines []map[string]string
	for _, line := range strings.Split(strings.TrimSpace(out.String()), "\n") {
		if line == "" {
			continue
		}
		var m map[string]string
		if err := json.Unmarshal([]byte(line), &m); err != nil {
			t.Fatalf("invalid NDJSON line %q: %v", line, err)
		}
		lines = append(lines, m)
	}
	return lines
}

// ---- Immediate message on first poll ----

func TestPollLoop_ImmediateMessages(t *testing.T) {
	msgs := []json.RawMessage{rawMsg("1700000001.000000", "hello")}
	fetch := func() ([]json.RawMessage, error) { return msgs, nil }

	var out bytes.Buffer
	code := pollLoop(fetch, testInterval, testTimeout, &out, io.Discard)

	if code != 0 {
		t.Fatalf("exit code: got %d, want 0", code)
	}
	lines := ndjsonLines(t, &out)
	if len(lines) != 1 {
		t.Fatalf("output lines: got %d, want 1", len(lines))
	}
	if lines[0]["ts"] != "1700000001.000000" {
		t.Errorf("ts: got %q, want 1700000001.000000", lines[0]["ts"])
	}
}

// ---- Multiple messages output as NDJSON ----

func TestPollLoop_MultipleMessages(t *testing.T) {
	msgs := []json.RawMessage{
		rawMsg("1700000001.000000", "first"),
		rawMsg("1700000002.000000", "second"),
		rawMsg("1700000003.000000", "third"),
	}
	fetch := func() ([]json.RawMessage, error) { return msgs, nil }

	var out bytes.Buffer
	code := pollLoop(fetch, testInterval, testTimeout, &out, io.Discard)

	if code != 0 {
		t.Fatalf("exit code: got %d, want 0", code)
	}
	lines := ndjsonLines(t, &out)
	if len(lines) != 3 {
		t.Fatalf("output lines: got %d, want 3", len(lines))
	}
	// Chronological order preserved.
	for i, want := range []string{"first", "second", "third"} {
		if lines[i]["text"] != want {
			t.Errorf("line[%d].text: got %q, want %q", i, lines[i]["text"], want)
		}
	}
}

// ---- Timeout when no messages arrive ----

func TestPollLoop_Timeout(t *testing.T) {
	fetch := func() ([]json.RawMessage, error) { return nil, nil }

	var out bytes.Buffer
	code := pollLoop(fetch, testInterval, 60*time.Millisecond, &out, io.Discard)

	if code != exitCodeTimeout {
		t.Fatalf("exit code: got %d, want %d", code, exitCodeTimeout)
	}
	if out.Len() > 0 {
		t.Error("stdout should be empty on timeout")
	}
}

// ---- Messages arrive after several empty polls ----

func TestPollLoop_MessagesAfterDelay(t *testing.T) {
	calls := 0
	msgs := []json.RawMessage{rawMsg("1700000002.000000", "delayed")}
	fetch := func() ([]json.RawMessage, error) {
		calls++
		if calls < 4 {
			return nil, nil
		}
		return msgs, nil
	}

	var out bytes.Buffer
	code := pollLoop(fetch, testInterval, testTimeout, &out, io.Discard)

	if code != 0 {
		t.Fatalf("exit code: got %d, want 0", code)
	}
	if calls < 4 {
		t.Errorf("fetch called %d times, want at least 4", calls)
	}
	lines := ndjsonLines(t, &out)
	if len(lines) != 1 {
		t.Fatalf("output lines: got %d, want 1", len(lines))
	}
}

// ---- Transient errors do not abort the loop ----

func TestPollLoop_TransientError(t *testing.T) {
	calls := 0
	msgs := []json.RawMessage{rawMsg("1700000001.000000", "recovered")}
	fetch := func() ([]json.RawMessage, error) {
		calls++
		if calls < 3 {
			return nil, errors.New("network hiccup")
		}
		return msgs, nil
	}

	var out, errOut bytes.Buffer
	code := pollLoop(fetch, testInterval, testTimeout, &out, &errOut)

	if code != 0 {
		t.Fatalf("exit code: got %d, want 0", code)
	}
	if !strings.Contains(errOut.String(), "network hiccup") {
		t.Errorf("stderr should contain error text; got %q", errOut.String())
	}
	if ndjsonLines(t, &out) == nil {
		t.Error("should have output a message after recovery")
	}
}

// ---- Error on first poll does not prevent subsequent polls ----

func TestPollLoop_FirstPollError_ThenSuccess(t *testing.T) {
	calls := 0
	msgs := []json.RawMessage{rawMsg("1700000001.000000", "hello")}
	fetch := func() ([]json.RawMessage, error) {
		calls++
		if calls == 1 {
			return nil, errors.New("first poll error")
		}
		return msgs, nil
	}

	var out, errOut bytes.Buffer
	code := pollLoop(fetch, testInterval, testTimeout, &out, &errOut)

	if code != 0 {
		t.Fatalf("exit code: got %d, want 0", code)
	}
	if !strings.Contains(errOut.String(), "first poll error") {
		t.Error("first poll error should appear in stderr")
	}
}

// ---- Persistent errors eventually cause timeout ----

func TestPollLoop_PersistentErrors_Timeout(t *testing.T) {
	fetch := func() ([]json.RawMessage, error) {
		return nil, errors.New("always failing")
	}

	var errOut bytes.Buffer
	code := pollLoop(fetch, testInterval, 60*time.Millisecond, io.Discard, &errOut)

	if code != exitCodeTimeout {
		t.Fatalf("exit code: got %d, want %d", code, exitCodeTimeout)
	}
	if !strings.Contains(errOut.String(), "always failing") {
		t.Error("error messages should appear in stderr")
	}
}

// ---- NDJSON validity: each line must be valid JSON ----

func TestPollLoop_NDJSONValid(t *testing.T) {
	msgs := []json.RawMessage{
		rawMsg("1700000001.000000", `line with "quotes" and \backslash`),
		rawMsg("1700000002.000000", "unicode: 日本語"),
	}
	fetch := func() ([]json.RawMessage, error) { return msgs, nil }

	var out bytes.Buffer
	pollLoop(fetch, testInterval, testTimeout, &out, io.Discard)

	for i, line := range strings.Split(strings.TrimRight(out.String(), "\n"), "\n") {
		if !json.Valid([]byte(line)) {
			t.Errorf("line %d is not valid JSON: %q", i, line)
		}
	}
}
