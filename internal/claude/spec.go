package claude

import (
	"encoding/json"
	"errors"
	"fmt"
	"strconv"
	"strings"
	"time"
)

// Permission modes accepted by --permission-mode.
const (
	ModeDefault     = "default"
	ModeAcceptEdits = "acceptEdits"
	ModeAuto        = "auto"
	ModeDontAsk     = "dontAsk"
	ModePlan        = "plan"
)

// emptyMCPConfig is passed with --strict-mcp-config so no MCP server — the
// user's claude.ai connectors included — is loaded into a factory run.
const emptyMCPConfig = `{"mcpServers":{}}`

// Spec describes one invocation of `claude -p`.
type Spec struct {
	// Prompt is written to stdin: a single argv entry is capped at 128 KiB.
	Prompt string
	Cwd    string
	Env    []string

	Model        string
	Effort       string
	MaxBudgetUSD float64

	PermissionMode string
	// Tools is the exact built-in tool set. nil keeps the CLI default (which
	// includes scheduling and remote tools a factory run must not have); an
	// empty non-nil slice disables every tool.
	Tools           []string
	AllowedTools    []string
	DisallowedTools []string

	JSONSchema         json.RawMessage
	AppendSystemPrompt string
	SettingsPath       string
	AddDirs            []string

	// Exactly one of SessionID (a new session with a known id), ResumeSession
	// (continue an earlier session), or NoSessionPersistence may be set.
	SessionID            string
	ResumeSession        string
	ForkSession          bool
	NoSessionPersistence bool

	// Timeout bounds the whole run; zero means no limit.
	Timeout time.Duration

	// RequireGuard makes the runner abort when a PreToolUse hook fails to run
	// cleanly: a missing or crashing guard fails open in the CLI.
	RequireGuard bool
	// ForbidPush makes the runner abort when the CLI reports a git push.
	ForbidPush bool
	// MaxDenials aborts a run that keeps hitting the permission system; zero
	// means no limit.
	MaxDenials int
}

// Validate checks the combinations the CLI would reject or silently misread.
func (s *Spec) Validate() error {
	var errs []error
	if s.Cwd == "" {
		errs = append(errs, errors.New("cwd is required"))
	}
	if strings.TrimSpace(s.Prompt) == "" {
		errs = append(errs, errors.New("prompt is required"))
	}
	n := 0
	for _, set := range []bool{s.SessionID != "", s.ResumeSession != "", s.NoSessionPersistence} {
		if set {
			n++
		}
	}
	if n > 1 {
		errs = append(errs, errors.New("only one of session id, resume session, and no session persistence may be set"))
	}
	if s.ForkSession && s.ResumeSession == "" {
		errs = append(errs, errors.New("fork session requires a session to resume"))
	}
	if len(s.JSONSchema) > 0 && !json.Valid(s.JSONSchema) {
		errs = append(errs, errors.New("json schema is not valid JSON"))
	}
	return errors.Join(errs...)
}

// Args builds the argv (without the binary). Flags that isolate the run from
// user and repo configuration are always present; see docs/spike.md.
func (s *Spec) Args() []string {
	args := []string{
		"-p",
		"--output-format", "stream-json",
		"--verbose", // stream-json emits nothing but the result without it
		"--include-hook-events",
		"--setting-sources", "",
		"--strict-mcp-config",
		"--mcp-config", emptyMCPConfig,
		"--disable-slash-commands",
		"--permission-prompts", "none",
	}
	if s.SettingsPath != "" {
		args = append(args, "--settings", s.SettingsPath)
	}
	if s.Model != "" {
		args = append(args, "--model", s.Model)
	}
	if s.Effort != "" {
		args = append(args, "--effort", s.Effort)
	}
	if s.MaxBudgetUSD > 0 {
		args = append(args, "--max-budget-usd", strconv.FormatFloat(s.MaxBudgetUSD, 'f', -1, 64))
	}
	// Passed on every call, resumes included: a resume does not restore it.
	if s.PermissionMode != "" {
		args = append(args, "--permission-mode", s.PermissionMode)
	}
	if s.Tools != nil {
		args = append(args, "--tools", strings.Join(s.Tools, ","))
	}
	if len(s.JSONSchema) > 0 {
		args = append(args, "--json-schema", string(s.JSONSchema))
	}
	if s.AppendSystemPrompt != "" {
		args = append(args, "--append-system-prompt", s.AppendSystemPrompt)
	}
	switch {
	case s.ResumeSession != "":
		args = append(args, "--resume", s.ResumeSession)
		if s.ForkSession {
			args = append(args, "--fork-session")
		}
	case s.SessionID != "":
		args = append(args, "--session-id", s.SessionID)
	case s.NoSessionPersistence:
		args = append(args, "--no-session-persistence")
	}
	// Variadic flags go last: each consumes arguments up to the next flag.
	// Rules like `Bash(git diff *)` contain spaces, so each is its own argv
	// entry rather than a joined string.
	if len(s.AddDirs) > 0 {
		args = append(args, "--add-dir")
		args = append(args, s.AddDirs...)
	}
	if len(s.AllowedTools) > 0 {
		args = append(args, "--allowedTools")
		args = append(args, s.AllowedTools...)
	}
	if len(s.DisallowedTools) > 0 {
		args = append(args, "--disallowedTools")
		args = append(args, s.DisallowedTools...)
	}
	return args
}

// String renders the command line for logs, with the long JSON arguments
// elided.
func (s *Spec) String() string {
	args := s.Args()
	for i, a := range args {
		if len(a) > 120 {
			args[i] = fmt.Sprintf("<%d bytes>", len(a))
		} else if a == "" || strings.ContainsAny(a, " *()\"'") {
			args[i] = strconv.Quote(a)
		}
	}
	return "claude " + strings.Join(args, " ")
}
