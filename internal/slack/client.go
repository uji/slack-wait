package slack

import (
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"slices"
	"time"
)

// Client wraps Slack Web API calls with a user token.
type Client struct {
	token      string
	httpClient *http.Client
	baseURL    string // override in tests; default is https://slack.com/api
}

func New(token string) *Client {
	return &Client{
		token:      token,
		httpClient: &http.Client{Timeout: 10 * time.Second},
		baseURL:    "https://slack.com/api",
	}
}

type apiResp struct {
	OK       bool              `json:"ok"`
	Error    string            `json:"error"`
	Messages []json.RawMessage `json:"messages"`
}

func (c *Client) get(method string, params url.Values) ([]json.RawMessage, error) {
	req, err := http.NewRequest("GET", c.baseURL+"/"+method+"?"+params.Encode(), nil)
	if err != nil {
		return nil, err
	}
	req.Header.Set("Authorization", "Bearer "+c.token)

	resp, err := c.httpClient.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, err
	}

	var r apiResp
	if err := json.Unmarshal(body, &r); err != nil {
		return nil, fmt.Errorf("parse %s response: %w", method, err)
	}
	if !r.OK {
		return nil, fmt.Errorf("%s: %s", method, r.Error)
	}
	return r.Messages, nil
}

func tsOf(m json.RawMessage) string {
	var v struct {
		TS string `json:"ts"`
	}
	json.Unmarshal(m, &v) //nolint:errcheck — best effort
	return v.TS
}

// History returns channel messages with ts > oldest, in chronological order.
// Slack returns history in reverse-chronological order; we reverse here.
func (c *Client) History(channel, oldest string) ([]json.RawMessage, error) {
	msgs, err := c.get("conversations.history", url.Values{
		"channel": {channel},
		"oldest":  {oldest},
		"limit":   {"200"},
	})
	if err != nil {
		return nil, err
	}
	// reverse: Slack returns newest-first, we want oldest-first
	slices.Reverse(msgs)
	return msgs, nil
}

// Replies returns thread replies with ts > oldest, in chronological order.
// The thread parent is always included by Slack; we filter it out.
func (c *Client) Replies(channel, threadTS, oldest string) ([]json.RawMessage, error) {
	msgs, err := c.get("conversations.replies", url.Values{
		"channel": {channel},
		"ts":      {threadTS},
		"oldest":  {oldest},
		"limit":   {"200"},
	})
	if err != nil {
		return nil, err
	}
	// conversations.replies returns chronological order already.
	// Filter the thread parent (ts == threadTS) which Slack always includes.
	out := msgs[:0]
	for _, m := range msgs {
		if ts := tsOf(m); ts != threadTS && ts > oldest {
			out = append(out, m)
		}
	}
	return out, nil
}
