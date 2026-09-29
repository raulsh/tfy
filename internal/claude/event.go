// Package claude runs the Claude Code CLI headless (`claude -p --output-format
// stream-json`) and interprets its event stream.
//
// The event shapes here were recorded from Claude Code 2.1.283; see
// docs/spike.md and the fixtures in testdata/.
package claude

import (
	"encoding/json"
	"fmt"
	"slices"
	"strings"
)

// Event types emitted on the stream. TypeLog is ours: a stdout line that was
// not JSON, kept so nothing the CLI printed is lost.
const (
	TypeSystem    = "system"
	TypeAssistant = "assistant"
	TypeUser      = "user"
	TypeResult    = "result"
	TypeRateLimit = "rate_limit_event"
	TypeLog       = "log"
)

// System event subtypes the runner reacts to.
const (
	SubInit             = "init"
	SubHookStarted      = "hook_started"
	SubHookResponse     = "hook_response"
	SubPermissionDenied = "permission_denied"
	SubVCSStateChanged  = "vcs_state_changed"
	// Tasks are the session's background work: shell commands started with
	// run_in_background and sub-agents started with the Agent tool.
	SubTaskStarted      = "task_started"
	SubTaskProgress     = "task_progress"
	SubTaskUpdated      = "task_updated"
	SubTaskNotification = "task_notification"
)

// TaskTypeAgent is the task type of a sub-agent; background shell commands
// are local_bash.
const TaskTypeAgent = "local_agent"

// Event is one line of stream-json output. Raw always holds the full line, so
// unknown event types survive untouched.
type Event struct {
	Seq     int             `json:"seq"`
	Type    string          `json:"type"`
	Subtype string          `json:"subtype,omitempty"`
	Raw     json.RawMessage `json:"raw"`
}

// ParseLine turns one stdout line into an Event. It never fails: a line that
// is not a JSON object becomes a TypeLog event carrying the text.
func ParseLine(seq int, line []byte) Event {
	var head struct {
		Type    string `json:"type"`
		Subtype string `json:"subtype"`
	}
	if err := json.Unmarshal(line, &head); err != nil || head.Type == "" {
		raw, _ := json.Marshal(map[string]string{"type": TypeLog, "text": string(line)})
		return Event{Seq: seq, Type: TypeLog, Raw: raw}
	}
	raw := make([]byte, len(line))
	copy(raw, line)
	return Event{Seq: seq, Type: head.Type, Subtype: head.Subtype, Raw: raw}
}

// Init is the system/init event.
type Init struct {
	SessionID         string   `json:"session_id"`
	Cwd               string   `json:"cwd"`
	Model             string   `json:"model"`
	PermissionMode    string   `json:"permissionMode"`
	Tools             []string `json:"tools"`
	ClaudeCodeVersion string   `json:"claude_code_version"`
	APIKeySource      string   `json:"apiKeySource"`
}

// Denial is one entry of result.permission_denials.
type Denial struct {
	ToolName  string          `json:"tool_name"`
	ToolUseID string          `json:"tool_use_id"`
	ToolInput json.RawMessage `json:"tool_input"`
}

// Usage is the token accounting on a result event. Like the cost, it is
// cumulative across a --resume chain.
type Usage struct {
	InputTokens              int64 `json:"input_tokens"`
	OutputTokens             int64 `json:"output_tokens"`
	CacheCreationInputTokens int64 `json:"cache_creation_input_tokens"`
	CacheReadInputTokens     int64 `json:"cache_read_input_tokens"`
}

// Result is the final result event. TotalCostUSD is cumulative across a
// --resume chain (even with --fork-session), so callers store the difference
// from the parent run.
type Result struct {
	Subtype           string          `json:"subtype"`
	IsError           bool            `json:"is_error"`
	NumTurns          int             `json:"num_turns"`
	DurationMS        int64           `json:"duration_ms"`
	TotalCostUSD      float64         `json:"total_cost_usd"`
	SessionID         string          `json:"session_id"`
	Result            string          `json:"result"`
	StructuredOutput  json.RawMessage `json:"structured_output,omitempty"`
	PermissionDenials []Denial        `json:"permission_denials"`
	Usage             Usage           `json:"usage"`
	TerminalReason    string          `json:"terminal_reason"`
	StopReason        string          `json:"stop_reason"`
}

// mergeEarlier folds in the result of an earlier turn of the same run. A
// session with sub-agents reports a result each time its turn ends, and
// carries on when they report back. The cost is cumulative already; turns,
// duration, tokens and denials are per turn. The structured output stays if
// the last turn did not repeat it.
func (r *Result) mergeEarlier(prev *Result) {
	r.NumTurns += prev.NumTurns
	r.DurationMS += prev.DurationMS
	r.Usage.InputTokens += prev.Usage.InputTokens
	r.Usage.OutputTokens += prev.Usage.OutputTokens
	r.Usage.CacheCreationInputTokens += prev.Usage.CacheCreationInputTokens
	r.Usage.CacheReadInputTokens += prev.Usage.CacheReadInputTokens
	r.PermissionDenials = append(slices.Clone(prev.PermissionDenials), r.PermissionDenials...)
	if _, ok := r.Structured(); !ok {
		if raw, ok := prev.Structured(); ok {
			r.StructuredOutput = raw
		}
	}
}

// Structured returns the schema-validated output, falling back to parsing the
// result text, which carries the same JSON.
func (r *Result) Structured() (json.RawMessage, bool) {
	if len(r.StructuredOutput) > 0 && string(r.StructuredOutput) != "null" {
		return r.StructuredOutput, true
	}
	text := strings.TrimSpace(r.Result)
	if json.Valid([]byte(text)) && strings.HasPrefix(text, "{") {
		return json.RawMessage(text), true
	}
	return nil, false
}

// RateLimitWindow is one entry of rate_limit_info.unifiedWindows.
type RateLimitWindow struct {
	Utilization float64 `json:"utilization"`
	ResetsAt    int64   `json:"resetsAt"`
}

// RateLimit is rate_limit_event.rate_limit_info. It arrives on every run with
// Status "allowed"; only other statuses mean the account is being limited.
type RateLimit struct {
	Status         string                     `json:"status"`
	ResetsAt       int64                      `json:"resetsAt"`
	RateLimitType  string                     `json:"rateLimitType"`
	UnifiedWindows map[string]RateLimitWindow `json:"unifiedWindows"`
}

// Limited reports whether this event means requests are being refused.
func (r *RateLimit) Limited() bool {
	return r.Status != "" && !strings.HasPrefix(r.Status, "allowed")
}

// HookEvent is system/hook_started and system/hook_response. ExitCode and
// Outcome are only set on responses.
type HookEvent struct {
	HookID    string `json:"hook_id"`
	HookName  string `json:"hook_name"` // e.g. "PreToolUse:Bash"
	HookEvent string `json:"hook_event"`
	ExitCode  *int   `json:"exit_code"`
	Outcome   string `json:"outcome"`
	Stdout    string `json:"stdout"`
	Stderr    string `json:"stderr"`
}

// Tool returns the tool a PreToolUse hook ran for, e.g. "Bash".
func (h *HookEvent) Tool() string {
	_, tool, _ := strings.Cut(h.HookName, ":")
	return tool
}

// PermissionDenied is system/permission_denied.
type PermissionDenied struct {
	ToolName           string `json:"tool_name"`
	ToolUseID          string `json:"tool_use_id"`
	DecisionReasonType string `json:"decision_reason_type"`
	Message            string `json:"message"`
}

// VCSStateChanged is system/vcs_state_changed; Kind is "commit" or "push".
type VCSStateChanged struct {
	Kind   string `json:"kind"`
	Branch string `json:"branch"`
	Cwd    string `json:"cwd"`
}

// Task is system/task_started, task_progress, task_updated and
// task_notification. Each carries some of the fields: task_updated only its
// Patch.
type Task struct {
	TaskID       string     `json:"task_id"`
	ToolUseID    string     `json:"tool_use_id"` // the call that started it
	TaskType     string     `json:"task_type"`   // on task_started
	Description  string     `json:"description"` // on progress, what it is doing now
	SubagentType string     `json:"subagent_type"`
	Prompt       string     `json:"prompt"`
	SpawnDepth   int        `json:"spawn_depth"` // 1 for the main agent's sub-agents
	Status       string     `json:"status"`
	Summary      string     `json:"summary"`
	LastToolName string     `json:"last_tool_name"`
	Usage        *TaskUsage `json:"usage"`
	Patch        *TaskPatch `json:"patch"`
}

// TaskUsage is a task's running totals.
type TaskUsage struct {
	TotalTokens int64 `json:"total_tokens"`
	ToolUses    int   `json:"tool_uses"`
	DurationMS  int64 `json:"duration_ms"`
}

// TaskPatch is the change a task_updated event reports.
type TaskPatch struct {
	Status  string `json:"status"`
	EndTime int64  `json:"end_time"` // Unix milliseconds
}

// Ended returns the status a task ended with (completed, failed, killed,
// stopped), or "" while it runs.
func (t *Task) Ended() string {
	s := t.Status
	if t.Patch != nil && t.Patch.Status != "" {
		s = t.Patch.Status
	}
	switch s {
	case "", "running", "pending":
		return ""
	}
	return s
}

// SubagentLaunch is what an Agent call's result says about the sub-agent.
type SubagentLaunch struct {
	AgentID string `json:"agentId"` // the task id
	Model   string `json:"resolvedModel"`
}

// ContentBlock is one block of an assistant or user message.
type ContentBlock struct {
	Type      string          `json:"type"`
	Text      string          `json:"text,omitempty"`
	Thinking  string          `json:"thinking,omitempty"`
	ID        string          `json:"id,omitempty"`
	Name      string          `json:"name,omitempty"`
	Input     json.RawMessage `json:"input,omitempty"`
	ToolUseID string          `json:"tool_use_id,omitempty"`
	IsError   bool            `json:"is_error,omitempty"`
	Content   json.RawMessage `json:"content,omitempty"`
}

// ResultText flattens a tool_result's content, which is either a string or a
// list of text blocks.
func (b *ContentBlock) ResultText() string {
	if len(b.Content) == 0 {
		return ""
	}
	var s string
	if json.Unmarshal(b.Content, &s) == nil {
		return s
	}
	var parts []struct {
		Text string `json:"text"`
	}
	if json.Unmarshal(b.Content, &parts) == nil {
		var sb strings.Builder
		for _, p := range parts {
			sb.WriteString(p.Text)
		}
		return sb.String()
	}
	return string(b.Content)
}

// Message is the payload of assistant and user events.
type Message struct {
	Role    string         `json:"role"`
	Model   string         `json:"model"` // on assistant messages
	Content []ContentBlock `json:"-"`
}

func (m *Message) UnmarshalJSON(data []byte) error {
	var raw struct {
		Role    string          `json:"role"`
		Model   string          `json:"model"`
		Content json.RawMessage `json:"content"`
	}
	if err := json.Unmarshal(data, &raw); err != nil {
		return err
	}
	m.Role, m.Model = raw.Role, raw.Model
	m.Content = nil
	if len(raw.Content) == 0 {
		return nil
	}
	var text string
	if json.Unmarshal(raw.Content, &text) == nil {
		m.Content = []ContentBlock{{Type: "text", Text: text}}
		return nil
	}
	return json.Unmarshal(raw.Content, &m.Content)
}

// Init decodes a system/init event.
func (e Event) Init() (*Init, bool) {
	if e.Type != TypeSystem || e.Subtype != SubInit {
		return nil, false
	}
	return decode[Init](e.Raw)
}

// Result decodes a result event.
func (e Event) Result() (*Result, bool) {
	if e.Type != TypeResult {
		return nil, false
	}
	return decode[Result](e.Raw)
}

// RateLimit decodes a rate_limit_event.
func (e Event) RateLimit() (*RateLimit, bool) {
	if e.Type != TypeRateLimit {
		return nil, false
	}
	w, ok := decode[struct {
		Info RateLimit `json:"rate_limit_info"`
	}](e.Raw)
	if !ok {
		return nil, false
	}
	return &w.Info, true
}

// Hook decodes system/hook_started and system/hook_response events.
func (e Event) Hook() (*HookEvent, bool) {
	if e.Type != TypeSystem || (e.Subtype != SubHookStarted && e.Subtype != SubHookResponse) {
		return nil, false
	}
	return decode[HookEvent](e.Raw)
}

// PermissionDenied decodes a system/permission_denied event.
func (e Event) PermissionDenied() (*PermissionDenied, bool) {
	if e.Type != TypeSystem || e.Subtype != SubPermissionDenied {
		return nil, false
	}
	return decode[PermissionDenied](e.Raw)
}

// VCSStateChanged decodes a system/vcs_state_changed event.
func (e Event) VCSStateChanged() (*VCSStateChanged, bool) {
	if e.Type != TypeSystem || e.Subtype != SubVCSStateChanged {
		return nil, false
	}
	return decode[VCSStateChanged](e.Raw)
}

// Task decodes the system task events.
func (e Event) Task() (*Task, bool) {
	if e.Type != TypeSystem {
		return nil, false
	}
	switch e.Subtype {
	case SubTaskStarted, SubTaskProgress, SubTaskUpdated, SubTaskNotification:
		return decode[Task](e.Raw)
	}
	return nil, false
}

// ParentToolUseID returns the Agent call a sub-agent's assistant or user
// event belongs to, or "" for the main agent's.
func (e Event) ParentToolUseID() string {
	if e.Type != TypeAssistant && e.Type != TypeUser {
		return ""
	}
	w, _ := decode[struct {
		Parent string `json:"parent_tool_use_id"`
	}](e.Raw)
	if w == nil {
		return ""
	}
	return w.Parent
}

// SubagentLaunch decodes the result of an Agent call, from the user event
// that carries it.
func (e Event) SubagentLaunch() (*SubagentLaunch, bool) {
	if e.Type != TypeUser {
		return nil, false
	}
	w, ok := decode[struct {
		Result json.RawMessage `json:"tool_use_result"`
	}](e.Raw)
	if !ok || !strings.HasPrefix(string(w.Result), "{") {
		return nil, false
	}
	l, ok := decode[SubagentLaunch](w.Result)
	if !ok || l.AgentID == "" {
		return nil, false
	}
	return l, true
}

// Message decodes the message of an assistant or user event.
func (e Event) Message() (*Message, bool) {
	if e.Type != TypeAssistant && e.Type != TypeUser {
		return nil, false
	}
	w, ok := decode[struct {
		Message Message `json:"message"`
	}](e.Raw)
	if !ok {
		return nil, false
	}
	return &w.Message, true
}

func decode[T any](raw json.RawMessage) (*T, bool) {
	var v T
	if err := json.Unmarshal(raw, &v); err != nil {
		return nil, false
	}
	return &v, true
}

// Summary is a one-line human description of an event, for run timelines.
// It returns "" for events not worth a line of their own.
func (e Event) Summary() string {
	switch e.Type {
	case TypeAssistant:
		m, ok := e.Message()
		if !ok {
			return ""
		}
		var parts []string
		for _, b := range m.Content {
			switch b.Type {
			case "text":
				if t := strings.TrimSpace(b.Text); t != "" {
					parts = append(parts, clip(oneLine(t), 240))
				}
			case "tool_use":
				parts = append(parts, b.Name+": "+toolInputSummary(b.Name, b.Input))
			}
		}
		return strings.Join(parts, " · ")
	case TypeUser:
		m, ok := e.Message()
		if !ok {
			return ""
		}
		for _, b := range m.Content {
			if b.Type == "tool_result" && b.IsError {
				return "tool error: " + clip(oneLine(b.ResultText()), 200)
			}
		}
		return ""
	case TypeResult:
		r, ok := e.Result()
		if !ok {
			return "result"
		}
		return fmt.Sprintf("result: %s · %d turns · $%.4f", r.Subtype, r.NumTurns, r.TotalCostUSD)
	case TypeSystem:
		switch e.Subtype {
		case SubInit:
			if i, ok := e.Init(); ok {
				return fmt.Sprintf("session %s · %s · mode %s", shortID(i.SessionID), i.Model, i.PermissionMode)
			}
		case SubPermissionDenied:
			if d, ok := e.PermissionDenied(); ok {
				return "denied " + d.ToolName + ": " + clip(oneLine(d.Message), 160)
			}
		case SubHookResponse:
			if h, ok := e.Hook(); ok && h.ExitCode != nil && *h.ExitCode != 0 {
				return fmt.Sprintf("hook %s exit %d: %s", h.HookName, *h.ExitCode, clip(oneLine(h.Stderr), 160))
			}
		case SubVCSStateChanged:
			if v, ok := e.VCSStateChanged(); ok {
				return "git " + v.Kind + " in " + v.Cwd
			}
		case SubTaskStarted:
			if t, ok := e.Task(); ok {
				if t.TaskType == TaskTypeAgent {
					return fmt.Sprintf("sub-agent started: %s (%s)", clip(oneLine(t.Description), 160), t.SubagentType)
				}
				return "background task started: " + clip(oneLine(t.Description), 160)
			}
		case SubTaskNotification:
			if t, ok := e.Task(); ok {
				return "background task " + t.Status
			}
		}
		return ""
	case TypeLog:
		var l struct {
			Text string `json:"text"`
		}
		_ = json.Unmarshal(e.Raw, &l)
		return clip(oneLine(l.Text), 240)
	}
	return ""
}

// ToolName returns the tool of the first tool_use block in an assistant event.
func (e Event) ToolName() string {
	if e.Type != TypeAssistant {
		return ""
	}
	m, ok := e.Message()
	if !ok {
		return ""
	}
	for _, b := range m.Content {
		if b.Type == "tool_use" {
			return b.Name
		}
	}
	return ""
}

// Activity describes a tool call for people watching a run: the tool and
// what it works on, in the words Claude gave the call when it gave some.
func (b *ContentBlock) Activity() string {
	var in struct {
		Description string `json:"description"`
	}
	if json.Unmarshal(b.Input, &in) == nil && strings.TrimSpace(in.Description) != "" {
		return b.Name + ": " + clip(oneLine(in.Description), 160)
	}
	return b.Name + ": " + toolInputSummary(b.Name, b.Input)
}

func toolInputSummary(tool string, input json.RawMessage) string {
	var in map[string]any
	if json.Unmarshal(input, &in) != nil {
		return ""
	}
	for _, k := range []string{"command", "file_path", "notebook_path", "url", "query", "pattern", "description"} {
		if v, ok := in[k].(string); ok && v != "" {
			return clip(oneLine(v), 200)
		}
	}
	return clip(string(input), 120)
}

func oneLine(s string) string {
	return strings.Join(strings.Fields(s), " ")
}

func clip(s string, n int) string {
	r := []rune(s)
	if len(r) <= n {
		return s
	}
	return string(r[:n-1]) + "…"
}

func shortID(id string) string {
	if len(id) > 8 {
		return id[:8]
	}
	return id
}
