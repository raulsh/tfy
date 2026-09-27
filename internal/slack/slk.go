// Package slack reads Slack through slk (github.com/raulsh/slk), which uses
// the user's own browser credentials.
//
// It always asks for slk's --json envelope: --jsonl drops the "truncated"
// flag and the warnings, so a partial read would look complete.
package slack

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"strconv"
	"strings"
	"time"
)

// Client runs slk.
type Client struct {
	Bin string
}

// Message is slk's compact message record.
type Message struct {
	Channel      string `json:"channel"`
	ChannelName  string `json:"channel_name"`
	TS           string `json:"ts"`
	Time         string `json:"time"`
	User         string `json:"user"`
	UserName     string `json:"user_name"`
	Text         string `json:"text"`
	TextRendered string `json:"text_rendered"`
	ThreadTS     string `json:"thread_ts"`
	ReplyCount   int    `json:"reply_count"`
	Edited       bool   `json:"edited"`
	Subtype      string `json:"subtype"`
	Bot          bool   `json:"bot"`
	Permalink    string `json:"permalink"`
}

// Body is the text people read: entities resolved where slk resolved them.
func (m Message) Body() string {
	if m.TextRendered != "" {
		return m.TextRendered
	}
	return m.Text
}

// Author is the best available name for the sender.
func (m Message) Author() string {
	if m.UserName != "" {
		return m.UserName
	}
	return m.User
}

// IsReply reports whether the message is a reply inside a thread.
func (m Message) IsReply() bool { return m.ThreadTS != "" && m.ThreadTS != m.TS }

// Posted is when the message was sent, from its ts.
func (m Message) Posted() time.Time {
	sec, frac, _ := strings.Cut(m.TS, ".")
	s, err := strconv.ParseInt(sec, 10, 64)
	if err != nil {
		return time.Time{}
	}
	ns, _ := strconv.ParseInt((frac + "000000000")[:9], 10, 64)
	return time.Unix(s, ns).UTC()
}

// Conversation is a channel or DM the user belongs to.
type Conversation struct {
	ID      string `json:"id"`
	Name    string `json:"name"`
	Type    string `json:"type"` // channel, private, mpim, dm
	Topic   string `json:"topic"`
	Purpose string `json:"purpose"`
}

// Error is a failure slk reported, possibly with a back-off.
type Error struct {
	Code       string        `json:"code"`
	Message    string        `json:"message"`
	Hint       string        `json:"hint"`
	RetryAfter time.Duration `json:"-"`
}

func (e *Error) Error() string {
	msg := "slk: " + e.Message
	if e.Code != "" {
		msg += " (" + e.Code + ")"
	}
	if e.Hint != "" {
		msg += ": " + e.Hint
	}
	return msg
}

type envelope struct {
	OK        bool            `json:"ok"`
	Results   json.RawMessage `json:"results"`
	Truncated bool            `json:"truncated"`
	Warnings  []string        `json:"warnings"`
	Error     *struct {
		Code       string `json:"code"`
		Message    string `json:"message"`
		Hint       string `json:"hint"`
		RetryAfter int    `json:"retry_after"`
	} `json:"error"`
}

func (c *Client) bin() string {
	if c.Bin != "" {
		return c.Bin
	}
	return "slk"
}

// run executes slk and decodes the results into v, returning whether slk
// cut the result set short.
func (c *Client) run(ctx context.Context, v any, args ...string) (bool, error) {
	args = append(args, "--json", "--no-color")
	cmd := exec.CommandContext(ctx, c.bin(), args...)
	cmd.Env = append(os.Environ(), "NO_COLOR=1")
	var stdout, stderr bytes.Buffer
	cmd.Stdout, cmd.Stderr = &stdout, &stderr
	runErr := cmd.Run()
	var env envelope
	if err := json.Unmarshal(stdout.Bytes(), &env); err != nil {
		if runErr != nil {
			return false, fmt.Errorf("slk %s: %v: %s", args[0], runErr, strings.TrimSpace(stderr.String()))
		}
		return false, fmt.Errorf("slk %s: unreadable output: %w", args[0], err)
	}
	if !env.OK || env.Error != nil {
		e := &Error{Message: "request failed"}
		if env.Error != nil {
			e.Code, e.Message, e.Hint = env.Error.Code, env.Error.Message, env.Error.Hint
			e.RetryAfter = time.Duration(env.Error.RetryAfter) * time.Second
		}
		return false, e
	}
	if v != nil && len(env.Results) > 0 {
		if err := json.Unmarshal(env.Results, v); err != nil {
			return false, fmt.Errorf("slk %s: decode results: %w", args[0], err)
		}
	}
	return env.Truncated, nil
}

// Conversations lists the channels (and optionally DMs) the user is in whose
// name contains query.
func (c *Client) Conversations(ctx context.Context, query string) ([]Conversation, error) {
	args := []string{"conversations", "list", "--limit", "0", "--type", "channel", "--type", "private"}
	if query != "" {
		args = append(args, "--name", query)
	}
	var out []Conversation
	_, err := c.run(ctx, &out, args...)
	return out, err
}

// HistoryOptions select a window of a channel's history.
type HistoryOptions struct {
	Channel     string
	Since       string // a Slack ts, or anything slk accepts (72h, 2026-09-01)
	Until       string
	Limit       int
	ExcludeBots bool
}

// History reads messages in a window, oldest first. truncated means older
// messages in the window were left out.
func (c *Client) History(ctx context.Context, o HistoryOptions) (msgs []Message, truncated bool, err error) {
	args := []string{"messages", "--channel", o.Channel, "--max-total", "0", "--oldest-first"}
	if o.Since != "" {
		args = append(args, "--since", o.Since)
	}
	if o.Until != "" {
		args = append(args, "--until", o.Until)
	}
	if o.Limit > 0 {
		args = append(args, "--limit", strconv.Itoa(o.Limit))
	}
	if o.ExcludeBots {
		args = append(args, "--exclude-bots")
	}
	truncated, err = c.run(ctx, &msgs, args...)
	return msgs, truncated, err
}

// Thread reads a thread: the parent and its replies.
func (c *Client) Thread(ctx context.Context, channel, ts string) ([]Message, error) {
	var out []Message
	_, err := c.run(ctx, &out, "thread", channel, ts, "--limit", "500")
	return out, err
}

// ThreadByPermalink reads the thread a Slack message link points into.
func (c *Client) ThreadByPermalink(ctx context.Context, permalink string) ([]Message, error) {
	var out []Message
	_, err := c.run(ctx, &out, "thread", permalink, "--limit", "500")
	return out, err
}
