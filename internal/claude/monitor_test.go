package claude

import (
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
