package slack

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

// fakeSlk writes a script that prints body and records its arguments.
func fakeSlk(t *testing.T, body string, exit int) (*Client, string) {
	t.Helper()
	dir := t.TempDir()
	argsFile := filepath.Join(dir, "args")
	script := "#!/bin/sh\nprintf '%s\\n' \"$@\" > " + argsFile + "\ncat <<'EOF'\n" + body + "\nEOF\nexit " + string(rune('0'+exit)) + "\n"
	bin := filepath.Join(dir, "slk")
	if err := os.WriteFile(bin, []byte(script), 0o755); err != nil {
		t.Fatal(err)
	}
	return &Client{Bin: bin}, argsFile
}

func TestHistoryEnvelope(t *testing.T) {
	c, argsFile := fakeSlk(t, `{"ok":true,"schema":1,"command":"messages","count":2,"truncated":true,"results":[
		{"channel":"C1","channel_name":"feedback","ts":"1790400000.000100","time":"x","user":"U1","user_name":"ana","text":"<@U2> it is broken","text_rendered":"@bob it is broken","reply_count":2},
		{"channel":"C1","ts":"1790400001.000200","thread_ts":"1790400000.000100","user":"U2","text":"same here"}]}`, 0)
	msgs, truncated, err := c.History(context.Background(), HistoryOptions{Channel: "C1", Since: "1790399999.000000", Limit: 500, ExcludeBots: true})
	if err != nil {
		t.Fatal(err)
	}
	if !truncated {
		t.Error("the truncated flag must survive")
	}
	if len(msgs) != 2 || msgs[0].Body() != "@bob it is broken" || msgs[0].Author() != "ana" || msgs[1].Author() != "U2" {
		t.Fatalf("messages = %+v", msgs)
	}
	if !msgs[1].IsReply() || msgs[0].IsReply() {
		t.Error("reply detection")
	}
	if got := msgs[0].Posted(); !got.Equal(time.Unix(1790400000, 100000).UTC()) {
		t.Errorf("posted = %v", got)
	}
	args, _ := os.ReadFile(argsFile)
	for _, want := range []string{"--json", "--exclude-bots", "--since\n1790399999.000000", "--max-total\n0"} {
		if !strings.Contains(string(args), want) {
			t.Errorf("args missing %q:\n%s", want, args)
		}
	}
}

func TestErrorEnvelope(t *testing.T) {
	c, _ := fakeSlk(t, `{"ok":false,"error":{"code":"rate_limited","message":"slow down","retry_after":30}}`, 1)
	_, _, err := c.History(context.Background(), HistoryOptions{Channel: "C1"})
	var se *Error
	if !errors.As(err, &se) || se.Code != "rate_limited" || se.RetryAfter != 30*time.Second {
		t.Fatalf("err = %v", err)
	}
}

func TestUnreadableOutput(t *testing.T) {
	c, _ := fakeSlk(t, `not json`, 2)
	if _, err := c.Conversations(context.Background(), "x"); err == nil {
		t.Fatal("expected an error")
	}
}
