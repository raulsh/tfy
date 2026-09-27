package claude

import (
	"bufio"
	"bytes"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// loadFixture parses a recorded stream (see docs/spike.md).
func loadFixture(t *testing.T, name string) []Event {
	t.Helper()
	data, err := os.ReadFile(filepath.Join("testdata", name+".jsonl"))
	if err != nil {
		t.Fatal(err)
	}
	var evs []Event
	sc := bufio.NewScanner(bytes.NewReader(data))
	sc.Buffer(make([]byte, 64<<10), 32<<20)
	for sc.Scan() {
		line := bytes.TrimSpace(sc.Bytes())
		if len(line) == 0 {
			continue
		}
		evs = append(evs, ParseLine(len(evs)+1, line))
	}
	if len(evs) == 0 {
		t.Fatalf("fixture %s is empty", name)
	}
	return evs
}

func find[T any](evs []Event, get func(Event) (*T, bool)) *T {
	for _, e := range evs {
		if v, ok := get(e); ok {
			return v
		}
	}
	return nil
}

func TestParseLineNonJSON(t *testing.T) {
	e := ParseLine(1, []byte("Error: something happened"))
	if e.Type != TypeLog {
		t.Fatalf("type = %q, want log", e.Type)
	}
	if got := e.Summary(); got != "Error: something happened" {
		t.Fatalf("summary = %q", got)
	}
}

func TestFixturesParse(t *testing.T) {
	names, _ := filepath.Glob("testdata/*.jsonl")
	for _, path := range names {
		name := strings.TrimSuffix(filepath.Base(path), ".jsonl")
		t.Run(name, func(t *testing.T) {
			evs := loadFixture(t, name)
			init := find(evs, Event.Init)
			if init == nil || init.SessionID == "" || init.PermissionMode == "" {
				t.Fatalf("no usable init event: %+v", init)
			}
			for _, e := range evs {
				if e.Type == TypeLog {
					t.Errorf("unexpected non-JSON line %d", e.Seq)
				}
				_ = e.Summary() // must not panic on any recorded shape
			}
			if res := find(evs, Event.Result); res == nil {
				t.Fatal("no result event")
			}
		})
	}
}

func TestInitAndResult(t *testing.T) {
	evs := loadFixture(t, "q5-schema-notools")
	init := find(evs, Event.Init)
	if init.Model != "claude-sonnet-5" || init.ClaudeCodeVersion != "2.1.283" {
		t.Errorf("init = %+v", init)
	}
	if len(init.Tools) != 1 || init.Tools[0] != "StructuredOutput" {
		t.Errorf("tools = %v, want [StructuredOutput]", init.Tools)
	}
	res := find(evs, Event.Result)
	if res.Subtype != "success" || res.IsError || res.TotalCostUSD <= 0 || res.NumTurns != 2 {
		t.Errorf("result = %+v", res)
	}
	raw, ok := res.Structured()
	if !ok {
		t.Fatal("no structured output")
	}
	var out struct{ Title, Kind string }
	if err := json.Unmarshal(raw, &out); err != nil || out.Kind != "bugfix" || out.Title == "" {
		t.Errorf("structured = %s (%v)", raw, err)
	}
}

func TestStructuredFallsBackToResultText(t *testing.T) {
	r := Result{Result: ` {"title":"x","kind":"feature"} `}
	raw, ok := r.Structured()
	if !ok || !strings.Contains(string(raw), `"feature"`) {
		t.Fatalf("got %s, %v", raw, ok)
	}
	if _, ok := (&Result{Result: "plain text"}).Structured(); ok {
		t.Fatal("plain text is not structured output")
	}
}

func TestRateLimit(t *testing.T) {
	rl := find(loadFixture(t, "q1-empty"), Event.RateLimit)
	if rl == nil {
		t.Fatal("no rate limit event")
	}
	if rl.Status != "allowed" || rl.Limited() {
		t.Errorf("status %q limited=%v; every run reports an allowed status", rl.Status, rl.Limited())
	}
	w, ok := rl.UnifiedWindows["seven_day"]
	if !ok || w.ResetsAt == 0 {
		t.Errorf("windows = %+v", rl.UnifiedWindows)
	}
	if !(&RateLimit{Status: "rejected"}).Limited() {
		t.Error("rejected must count as limited")
	}
}

func TestHookAndDenialEvents(t *testing.T) {
	evs := loadFixture(t, "q2-deny")
	var responses, denied int
	for _, e := range evs {
		if h, ok := e.Hook(); ok && e.Subtype == SubHookResponse {
			responses++
			if h.Tool() != "Bash" && h.Tool() != "Monitor" {
				t.Errorf("hook tool = %q", h.Tool())
			}
			if h.ExitCode == nil {
				t.Error("hook response without exit code")
			} else if *h.ExitCode == 2 {
				denied++
			}
		}
	}
	if responses != 5 || denied != 4 {
		t.Errorf("responses=%d denied=%d, want 5 and 4", responses, denied)
	}

	d := find(loadFixture(t, "q2-edit"), Event.PermissionDenied)
	if d == nil || d.ToolName != "Write" || d.DecisionReasonType != "mode" {
		t.Errorf("permission denied = %+v", d)
	}
	v := find(loadFixture(t, "q2-denyonly"), Event.VCSStateChanged)
	if v == nil || v.Kind != "push" {
		t.Errorf("vcs state = %+v", v)
	}
}

func TestSummaries(t *testing.T) {
	evs := loadFixture(t, "q2-deny")
	var all []string
	for _, e := range evs {
		if s := e.Summary(); s != "" {
			all = append(all, s)
		}
	}
	joined := strings.Join(all, "\n")
	for _, want := range []string{"Bash: git status --short", "hook PreToolUse:Bash exit 2", "result: success", "mode dontAsk"} {
		if !strings.Contains(joined, want) {
			t.Errorf("summaries missing %q:\n%s", want, joined)
		}
	}
	if evs[0].ToolName() != "" {
		t.Error("init has no tool")
	}
}
