package claude

import (
	"context"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"
	"time"
)

type fakeRun struct {
	spec     *Spec
	runner   *Runner
	argsOut  string
	stdinOut string
}

func newFakeRun(t *testing.T, mode, fixture string) *fakeRun {
	t.Helper()
	dir := t.TempDir()
	bin, err := filepath.Abs("testdata/fake-claude")
	if err != nil {
		t.Fatal(err)
	}
	fx, _ := filepath.Abs(filepath.Join("testdata", fixture+".jsonl"))
	f := &fakeRun{
		argsOut:  filepath.Join(dir, "args"),
		stdinOut: filepath.Join(dir, "stdin"),
	}
	f.spec = &Spec{
		Prompt: "do the thing",
		Cwd:    dir,
		Env: []string{
			"PATH=" + os.Getenv("PATH"),
			"FAKE_MODE=" + mode,
			"FAKE_FIXTURE=" + fx,
			"FAKE_ARGS_OUT=" + f.argsOut,
			"FAKE_STDIN_OUT=" + f.stdinOut,
		},
	}
	f.runner = &Runner{
		Bin:             bin,
		PostResultGrace: 200 * time.Millisecond,
		InterruptGrace:  300 * time.Millisecond,
		TerminateGrace:  300 * time.Millisecond,
	}
	return f
}

func (f *fakeRun) run(t *testing.T, ctx context.Context) (*Outcome, []Event) {
	t.Helper()
	var evs []Event
	started := false
	out := f.runner.Run(ctx, f.spec, Callbacks{
		OnStart: func(pid int) { started = pid > 0 },
		OnEvent: func(e Event) { evs = append(evs, e) },
	})
	if out.Status != StatusFailed && !started {
		t.Error("OnStart was not called")
	}
	return out, evs
}

func TestRunSuccess(t *testing.T) {
	f := newFakeRun(t, "replay", "q5-schema-notools")
	f.spec.JSONSchema = []byte(`{"type":"object"}`)
	f.spec.NoSessionPersistence = true
	out, evs := f.run(t, context.Background())
	if out.Status != StatusSucceeded {
		t.Fatalf("status = %s (%s)", out.Status, out.Reason)
	}
	if out.Init == nil || out.Result == nil || out.RateLimit == nil {
		t.Fatalf("missing init/result/rate limit: %+v", out)
	}
	if _, ok := out.Result.Structured(); !ok {
		t.Error("structured output lost")
	}
	if len(evs) != 6 || evs[0].Seq != 1 {
		t.Errorf("got %d events, first seq %d", len(evs), evs[0].Seq)
	}
	stdin, _ := os.ReadFile(f.stdinOut)
	if string(stdin) != "do the thing" {
		t.Errorf("prompt on stdin = %q", stdin)
	}
	args, _ := os.ReadFile(f.argsOut)
	lines := strings.Split(strings.TrimRight(string(args), "\n"), "\n")
	for _, want := range []string{"-p", "--no-session-persistence", "--json-schema", "--strict-mcp-config"} {
		if !slices.Contains(lines, want) {
			t.Errorf("args missing %s: %q", want, lines)
		}
	}
}

func TestRunNonJSONLineKept(t *testing.T) {
	out, evs := newFakeRun(t, "noise", "q1-empty").run(t, context.Background())
	if out.Status != StatusSucceeded {
		t.Fatalf("status = %s (%s)", out.Status, out.Reason)
	}
	if evs[0].Type != TypeLog {
		t.Errorf("first event = %s, want log", evs[0].Type)
	}
}

func TestRunAborts(t *testing.T) {
	cases := []struct {
		name, fixture string
		tweak         func(*Spec)
		reason        string
	}{
		{"permission mode", "q1-empty", func(s *Spec) { s.PermissionMode = ModeAuto }, "instead of"},
		{"guard failure", "q9-missinghook", func(s *Spec) { s.RequireGuard = true }, "exit 127"},
		{"push", "q2-denyonly", func(s *Spec) { s.ForbidPush = true }, "pushed"},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			f := newFakeRun(t, "replay", c.fixture)
			c.tweak(f.spec)
			out, _ := f.run(t, context.Background())
			if out.Status != StatusAborted || !strings.Contains(out.Reason, c.reason) {
				t.Fatalf("status = %s (%s)", out.Status, out.Reason)
			}
		})
	}
}

func TestRunCancelInterrupts(t *testing.T) {
	f := newFakeRun(t, "hang", "q2-deny")
	ctx, cancel := context.WithCancel(context.Background())
	f.runner.InterruptGrace = 5 * time.Second
	var evs int
	out := f.runner.Run(ctx, f.spec, Callbacks{OnEvent: func(Event) {
		if evs++; evs == 3 {
			cancel()
		}
	}})
	if out.Status != StatusCancelled {
		t.Fatalf("status = %s (%s)", out.Status, out.Reason)
	}
	// SIGINT is enough: the CLI answers with an error result and exits 0.
	if out.ExitCode != 0 || out.Result == nil || out.Result.Subtype != "error_during_execution" {
		t.Errorf("exit %d, result %+v", out.ExitCode, out.Result)
	}
	if d := out.EndedAt.Sub(out.StartedAt); d > 4*time.Second {
		t.Errorf("cancel took %s; SIGINT should have been enough", d)
	}
}

func TestRunCancelEscalates(t *testing.T) {
	f := newFakeRun(t, "ignore-int", "q2-deny")
	ctx, cancel := context.WithCancel(context.Background())
	out := f.runner.Run(ctx, f.spec, Callbacks{OnEvent: func(Event) { cancel() }})
	if out.Status != StatusCancelled {
		t.Fatalf("status = %s (%s)", out.Status, out.Reason)
	}
	if out.ExitCode != 128+15 {
		t.Errorf("exit = %d, want SIGTERM's 143", out.ExitCode)
	}
}

func TestRunTimeout(t *testing.T) {
	f := newFakeRun(t, "hang", "q2-deny")
	f.spec.Timeout = 300 * time.Millisecond
	out, _ := f.run(t, context.Background())
	if out.Status != StatusTimedOut {
		t.Fatalf("status = %s (%s)", out.Status, out.Reason)
	}
}

func TestRunLingerAfterResult(t *testing.T) {
	f := newFakeRun(t, "linger", "q1-empty")
	out, _ := f.run(t, context.Background())
	if out.Status != StatusSucceeded {
		t.Fatalf("status = %s (%s)", out.Status, out.Reason)
	}
	if d := out.EndedAt.Sub(out.StartedAt); d > 5*time.Second {
		t.Errorf("run took %s; the post-result grace should have stopped it", d)
	}
}

// A session with sub-agents reports a result at the end of each turn, then
// resumes when a sub-agent reports back (docs/spike.md). A resumed turn that
// outlasts the post-result grace must not be cut short, and the run's result
// covers every turn.
func TestRunWaitsForResumedTurn(t *testing.T) {
	f := newFakeRun(t, "resume-slowly", "q10-subagents")
	f.spec.Env = append(f.spec.Env, "FAKE_PAUSE=0.6") // three times the grace
	f.spec.PermissionMode = ModeAuto
	out, evs := f.run(t, context.Background())
	if out.Status != StatusSucceeded {
		t.Fatalf("status = %s (%s)", out.Status, out.Reason)
	}
	if last := evs[len(evs)-1]; last.Type != TypeResult {
		t.Fatalf("the run stopped before its last result, at a %s event", last.Type)
	}
	r := out.Result
	if r.TotalCostUSD != 0.14690019999999998 {
		t.Errorf("cost = %v, want the last, cumulative one", r.TotalCostUSD)
	}
	if r.NumTurns != 9 || len(r.PermissionDenials) != 1 {
		t.Errorf("turns = %d and denials = %d, want every turn's (9 and 1)", r.NumTurns, len(r.PermissionDenials))
	}
}

func TestRunWithoutResult(t *testing.T) {
	out, _ := newFakeRun(t, "fail", "q1-empty").run(t, context.Background())
	if out.Status != StatusFailed || out.ExitCode != 3 || !strings.Contains(out.Reason, "boom") {
		t.Fatalf("status = %s, exit %d, reason %q", out.Status, out.ExitCode, out.Reason)
	}
}

func TestRunStartFailure(t *testing.T) {
	f := newFakeRun(t, "replay", "q1-empty")
	f.runner.Bin = filepath.Join(t.TempDir(), "missing-claude")
	out := f.runner.Run(context.Background(), f.spec, Callbacks{})
	if out.Status != StatusFailed || !strings.Contains(out.Reason, "start claude") {
		t.Fatalf("status = %s (%s)", out.Status, out.Reason)
	}
}

func TestRunInvalidSpec(t *testing.T) {
	f := newFakeRun(t, "replay", "q1-empty")
	f.spec.SessionID, f.spec.ResumeSession = "a", "b"
	out := f.runner.Run(context.Background(), f.spec, Callbacks{})
	if out.Status != StatusFailed || !strings.Contains(out.Reason, "invalid run") {
		t.Fatalf("status = %s (%s)", out.Status, out.Reason)
	}
}
