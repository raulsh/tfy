package claude

import (
	"encoding/json"
	"slices"
	"strings"
	"testing"
)

func replay(t *testing.T, fixture string, spec Spec) string {
	t.Helper()
	m := newMonitor(&spec)
	for _, e := range loadFixture(t, fixture) {
		if why := m.observe(e); why != "" {
			return why
		}
	}
	return ""
}

func TestMonitor(t *testing.T) {
	cases := []struct {
		name    string
		fixture string
		spec    Spec
		abort   string // substring; "" means the run must not abort
	}{
		{"guard denies are fine", "q2-deny", Spec{RequireGuard: true, ForbidPush: true, PermissionMode: ModeDontAsk}, ""},
		{"missing guard fails open in the CLI", "q9-missinghook", Spec{RequireGuard: true}, "exit 127"},
		{"command ran without any guard", "q2-denyonly", Spec{RequireGuard: true}, "without the guard"},
		{"push is reported by the CLI", "q2-denyonly", Spec{ForbidPush: true}, "pushed"},
		{"auto mode run with commits", "q4-auto-env", Spec{RequireGuard: true, ForbidPush: true, PermissionMode: ModeAuto}, ""},
		{"wrong permission mode", "q4-auto-env", Spec{PermissionMode: ModeDontAsk}, `"auto" instead of "dontAsk"`},
		{"unguarded profile ignores hooks", "q9-missinghook", Spec{}, ""},
		{"denials under the limit", "q2-edit", Spec{MaxDenials: 1}, ""},
		{"denials over the limit", "q5-ro", Spec{MaxDenials: 1}, "permission denials"},
		// Sub-agents run in the same session, and every layer holds for
		// them: the guard answers their shell commands (and blocked one
		// sub-agent's push), their pushes are reported, and their denials
		// count.
		{"sub-agents behind the guard", "q10-subagents", Spec{RequireGuard: true, GuardMarker: GuardMarker, ForbidPush: true, PermissionMode: ModeAuto}, ""},
		{"sub-agent ran without any guard", "q10-subagent-push", Spec{RequireGuard: true}, "without the guard"},
		{"sub-agent pushed", "q10-subagent-push", Spec{ForbidPush: true}, "pushed"},
		{"sub-agent denials count", "q10-subagents-dontask", Spec{MaxDenials: 1, PermissionMode: ModeDontAsk}, "permission denials"},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			if c.fixture == "q5-ro" {
				t.Skip("q5-ro was not kept as a fixture")
			}
			got := replay(t, c.fixture, c.spec)
			if c.abort == "" && got != "" {
				t.Fatalf("unexpected abort: %s", got)
			}
			if c.abort != "" && !strings.Contains(got, c.abort) {
				t.Fatalf("abort = %q, want it to mention %q", got, c.abort)
			}
		})
	}
}

// Seen for real: the agent wrote push instructions for a person into a
// file; the CLI reported a push from the text alone.
func TestPushReportCheckedAgainstCommands(t *testing.T) {
	toolUse := func(id, cmd string) Event {
		raw, _ := json.Marshal(map[string]any{"type": "assistant", "message": map[string]any{"role": "assistant",
			"content": []any{map[string]any{"type": "tool_use", "id": id, "name": "Bash", "input": map[string]string{"command": cmd}}}}})
		return ParseLine(1, raw)
	}
	pushed := ParseLine(2, []byte(`{"type":"system","subtype":"vcs_state_changed","kind":"push","cwd":"/w/app"}`))

	m := newMonitor(&Spec{ForbidPush: true})
	m.observe(toolUse("t1", "cat > ../docs/push-commands.txt <<EOF\ngit push --force-with-lease origin main\nEOF"))
	if why := m.observe(pushed); why != "" {
		t.Fatalf("text about pushing is not a push: %s", why)
	}
	// A push the guard denied pushed nothing, and must not taint later
	// reports.
	m.observe(toolUse("t2", "git push origin main"))
	m.observe(ParseLine(3, []byte(`{"type":"user","message":{"role":"user","content":[{"type":"tool_result","tool_use_id":"t2","is_error":true,"content":"blocked by the guard"}]}}`)))
	m.observe(toolUse("t3", "echo 'then run: git push'"))
	if why := m.observe(pushed); why != "" {
		t.Fatalf("a denied push plus text about pushing is not a push: %s", why)
	}
	m.observe(toolUse("t4", "git -C app push https://github.com/x/y main"))
	if why := m.observe(pushed); why == "" {
		t.Fatal("a real push must abort")
	}
}

// With repository hooks next to the guard, only the guard's own responses
// (marked on stdout) count, and a repository hook's failure is not the
// run's.
func TestGuardMarkerSeparatesRepositoryHooks(t *testing.T) {
	hook := func(stdout string, exit int) Event {
		raw, _ := json.Marshal(map[string]any{"type": "system", "subtype": "hook_response", "hook_name": "PreToolUse:Bash",
			"hook_event": "PreToolUse", "exit_code": exit, "outcome": "success", "stdout": stdout})
		return ParseLine(1, raw)
	}
	toolUse := func(id string) Event {
		raw, _ := json.Marshal(map[string]any{"type": "assistant", "message": map[string]any{"role": "assistant",
			"content": []any{map[string]any{"type": "tool_use", "id": id, "name": "Bash", "input": map[string]string{"command": "make test"}}}}})
		return ParseLine(2, raw)
	}
	result := func(id string) Event {
		raw, _ := json.Marshal(map[string]any{"type": "user", "message": map[string]any{"role": "user",
			"content": []any{map[string]any{"type": "tool_result", "tool_use_id": id, "content": "ok"}}}})
		return ParseLine(3, raw)
	}
	spec := Spec{RequireGuard: true, GuardMarker: GuardMarker}

	m := newMonitor(&spec)
	m.observe(toolUse("t1"))
	if why := m.observe(hook("lint warnings\n", 1)); why != "" {
		t.Fatalf("a repository hook's non-blocking error must not abort: %s", why)
	}
	m.observe(hook(GuardMarker+"\n", 0))
	if why := m.observe(result("t1")); why != "" {
		t.Fatalf("the guard answered: %s", why)
	}

	// The repository hook answered, the guard did not.
	m.observe(toolUse("t2"))
	m.observe(hook("", 0))
	if why := m.observe(result("t2")); !strings.Contains(why, "without the guard") {
		t.Fatalf("a repository hook cannot stand in for the guard, got %q", why)
	}

	m = newMonitor(&spec)
	if why := m.observe(hook(GuardMarker+"\n", 1)); !strings.Contains(why, "exit 1") {
		t.Fatalf("a guard that fails must abort the run, got %q", why)
	}
}

// A run is idle once it reported a result with no sub-agent still working,
// until it resumes for another turn.
func TestMonitorIdle(t *testing.T) {
	ev := func(line string) Event { return ParseLine(1, []byte(line)) }
	started := ev(`{"type":"system","subtype":"task_started","task_id":"a1","task_type":"local_agent","description":"Look around"}`)
	bash := ev(`{"type":"system","subtype":"task_started","task_id":"b1","task_type":"local_bash","description":"Serve"}`)
	result := ev(`{"type":"result","subtype":"success","num_turns":1,"total_cost_usd":0.1}`)
	done := ev(`{"type":"system","subtype":"task_notification","task_id":"a1","status":"completed"}`)
	resumed := ev(`{"type":"system","subtype":"init","session_id":"s","permissionMode":"auto"}`)

	m := newMonitor(&Spec{})
	steps := []struct {
		ev   Event
		idle bool
	}{
		{started, false}, {bash, false}, {result, false}, // a sub-agent works on
		{done, true}, {resumed, false}, {result, true}, // a background command does not count
	}
	for i, s := range steps {
		m.observe(s.ev)
		if m.idle() != s.idle {
			t.Fatalf("step %d (%s/%s): idle = %v", i, s.ev.Type, s.ev.Subtype, m.idle())
		}
	}
	if m.result.NumTurns != 2 {
		t.Errorf("turns = %d, want both results'", m.result.NumTurns)
	}
}

func TestProjectSettingSources(t *testing.T) {
	spec := &Spec{Prompt: "x", Cwd: "/w", SettingSources: []string{"project"}}
	Profiles["develop"].Apply(spec)
	args := spec.Args()
	if i := slices.Index(args, "--setting-sources"); i < 0 || args[i+1] != "project" {
		t.Errorf("--setting-sources: %q", args)
	}
}
