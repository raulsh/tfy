package claude

import (
	"encoding/json"
	"fmt"
	"strings"

	"github.com/raulsh/thefactory/internal/guard"
)

// isGuarded reports whether a tool runs shell commands and so must go through
// the guard hook.
func isGuarded(tool string) bool {
	return tool == "Bash" || tool == "Monitor"
}

// monitor watches a run's events for the conditions that must stop it. The
// CLI itself fails open in several of them (a guard that cannot start lets
// the command run), so the runner is the last line of defence.
type monitor struct {
	spec *Spec

	init        *Init
	result      *Result
	rateLimit   *RateLimit
	rateLimited bool
	denials     int

	toolNames     map[string]string // tool_use id → tool name
	hookResponses map[string]int    // tool → PreToolUse hook responses
	executed      map[string]int    // tool → calls that ran (non-error results)
	// commands are the shell commands (by tool_use id) that ran since the
	// last push report, to check the CLI's report against. Denied or failed
	// calls are dropped: they pushed nothing.
	commands map[string]string
}

func newMonitor(spec *Spec) *monitor {
	return &monitor{
		spec:          spec,
		toolNames:     map[string]string{},
		hookResponses: map[string]int{},
		executed:      map[string]int{},
		commands:      map[string]string{},
	}
}

// observe records ev and returns a non-empty reason when the run must abort.
func (m *monitor) observe(ev Event) string {
	switch ev.Type {
	case TypeSystem:
		return m.observeSystem(ev)
	case TypeAssistant:
		if msg, ok := ev.Message(); ok {
			for _, b := range msg.Content {
				if b.Type == "tool_use" && b.ID != "" {
					m.toolNames[b.ID] = b.Name
					if isGuarded(b.Name) {
						var in struct {
							Command string `json:"command"`
						}
						if json.Unmarshal(b.Input, &in) == nil && in.Command != "" {
							m.commands[b.ID] = in.Command
						}
					}
				}
			}
		}
	case TypeUser:
		msg, ok := ev.Message()
		if !ok {
			return ""
		}
		for _, b := range msg.Content {
			if b.Type == "tool_result" && b.IsError {
				delete(m.commands, b.ToolUseID)
			}
		}
		if !m.spec.RequireGuard {
			return ""
		}
		for _, b := range msg.Content {
			if b.Type != "tool_result" || b.IsError {
				continue
			}
			tool := m.toolNames[b.ToolUseID]
			if !isGuarded(tool) {
				continue
			}
			// Every command that actually ran must have passed through a
			// PreToolUse hook first.
			m.executed[tool]++
			if m.executed[tool] > m.hookResponses[tool] {
				return fmt.Sprintf("a %s call ran without the guard hook responding", tool)
			}
		}
	case TypeRateLimit:
		if rl, ok := ev.RateLimit(); ok {
			m.rateLimit = rl
			if rl.Limited() {
				m.rateLimited = true
			}
		}
	case TypeResult:
		if r, ok := ev.Result(); ok {
			m.result = r
		}
	}
	return ""
}

func (m *monitor) observeSystem(ev Event) string {
	switch ev.Subtype {
	case SubInit:
		init, ok := ev.Init()
		if !ok {
			return ""
		}
		m.init = init
		// Auto mode falls back to the default mode, silently, where it is not
		// available; headless, every edit would then be denied.
		if want := m.spec.PermissionMode; want != "" && init.PermissionMode != want {
			return fmt.Sprintf("session started in permission mode %q instead of %q", init.PermissionMode, want)
		}
	case SubHookResponse:
		h, ok := ev.Hook()
		if !ok || h.HookEvent != "PreToolUse" {
			return ""
		}
		m.hookResponses[h.Tool()]++
		if !m.spec.RequireGuard {
			return ""
		}
		// 0 allows and 2 denies; anything else means the guard did not run
		// and the CLI will let the command through.
		if h.ExitCode == nil {
			if h.Outcome != "success" {
				return fmt.Sprintf("guard hook %s did not complete (%s)", h.HookName, h.Outcome)
			}
			return ""
		}
		if code := *h.ExitCode; code != 0 && code != 2 {
			return fmt.Sprintf("guard hook %s failed with exit %d: %s", h.HookName, code, strings.TrimSpace(h.Stderr))
		}
	case SubPermissionDenied:
		m.denials++
		if m.spec.MaxDenials > 0 && m.denials > m.spec.MaxDenials {
			return fmt.Sprintf("more than %d permission denials", m.spec.MaxDenials)
		}
	case SubVCSStateChanged:
		v, ok := ev.VCSStateChanged()
		if !ok || v.Kind != "push" || !m.spec.ForbidPush {
			return ""
		}
		// The CLI spots pushes in the command text, so "git push" written
		// into a file or echoed also counts. Abort only when the shell
		// parser finds a real push among the commands that just ran.
		cmds := m.commands
		m.commands = map[string]string{}
		if len(cmds) == 0 {
			return "the agent pushed to a remote (in " + v.Cwd + ")"
		}
		for _, c := range cmds {
			if !guard.CheckCommand(c).Allow {
				return "the agent pushed to a remote (in " + v.Cwd + ")"
			}
		}
	}
	return ""
}
