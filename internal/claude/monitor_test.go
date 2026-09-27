package claude

import (
	"encoding/json"
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
