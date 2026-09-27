package cli

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"os/exec"
	"os/signal"
	"path/filepath"
	"sort"
	"strings"
	"syscall"
	"time"

	"github.com/spf13/cobra"

	"github.com/raulsh/thefactory/internal/claude"
)

func newDevCmd() *cobra.Command {
	dev := &cobra.Command{
		Use:    "dev",
		Short:  "Developer tools for working on thefactory itself",
		Hidden: true,
	}
	dev.AddCommand(newDevClaudeCmd())
	return dev
}

type devClaudeOpts struct {
	profile    string
	cwd        string
	promptFile string
	model      string
	effort     string
	budget     float64
	schemaFile string
	resume     string
	trust      []string
	timeout    time.Duration
	bin        string
}

func newDevClaudeCmd() *cobra.Command {
	var o devClaudeOpts
	cmd := &cobra.Command{
		Use:   "claude",
		Short: "Run one Claude profile by hand and print its event stream",
		Example: `  thefactory dev claude --profile develop --cwd ./ws --prompt-file task.md --trust repo-a,repo-b
  thefactory dev claude --profile triage --cwd /tmp --prompt-file msgs.md --schema-file triage.json`,
		Args: cobra.NoArgs,
		RunE: func(cmd *cobra.Command, args []string) error {
			return runDevClaude(cmd.Context(), o)
		},
	}
	f := cmd.Flags()
	f.StringVar(&o.profile, "profile", "", "run profile: "+strings.Join(profileNames(), ", "))
	f.StringVar(&o.cwd, "cwd", ".", "working directory for the run")
	f.StringVar(&o.promptFile, "prompt-file", "", "file holding the prompt (- for stdin)")
	f.StringVar(&o.model, "model", "sonnet", "model alias or id")
	f.StringVar(&o.effort, "effort", "low", "effort level")
	f.Float64Var(&o.budget, "budget", 1, "maximum spend in USD")
	f.StringVar(&o.schemaFile, "schema-file", "", "JSON schema for structured output")
	f.StringVar(&o.resume, "resume", "", "session id to resume (forked)")
	f.StringSliceVar(&o.trust, "trust", nil, "checkouts under cwd auto mode should treat as in scope")
	f.DurationVar(&o.timeout, "timeout", 15*time.Minute, "wall-clock limit")
	f.StringVar(&o.bin, "claude-bin", "claude", "claude executable")
	_ = cmd.MarkFlagRequired("profile")
	_ = cmd.MarkFlagRequired("prompt-file")
	return cmd
}

func profileNames() []string {
	names := make([]string, 0, len(claude.Profiles))
	for n := range claude.Profiles {
		names = append(names, n)
	}
	sort.Strings(names)
	return names
}

func runDevClaude(ctx context.Context, o devClaudeOpts) error {
	profile, ok := claude.Profiles[o.profile]
	if !ok {
		return fmt.Errorf("unknown profile %q (have %s)", o.profile, strings.Join(profileNames(), ", "))
	}
	prompt, err := readPrompt(o.promptFile)
	if err != nil {
		return err
	}
	cwd, err := filepath.Abs(o.cwd)
	if err != nil {
		return err
	}
	scratch, err := os.MkdirTemp("", "thefactory-dev-*")
	if err != nil {
		return err
	}
	defer os.RemoveAll(scratch)

	spec := &claude.Spec{
		Prompt:       prompt,
		Cwd:          cwd,
		Model:        o.model,
		Effort:       o.effort,
		MaxBudgetUSD: o.budget,
		Timeout:      o.timeout,
	}
	if o.resume != "" {
		spec.ResumeSession, spec.ForkSession = o.resume, true
	}
	profile.Apply(spec)
	if o.schemaFile != "" {
		schema, err := os.ReadFile(o.schemaFile)
		if err != nil {
			return err
		}
		spec.JSONSchema = schema
	}

	// Settings and credentials live outside the working directory, where the
	// agent cannot edit them.
	ghDir := filepath.Join(scratch, "gh")
	if err := os.MkdirAll(ghDir, 0o700); err != nil {
		return err
	}
	gitconfig := filepath.Join(scratch, "gitconfig")
	if err := os.WriteFile(gitconfig, []byte(gitIdentity()), 0o644); err != nil {
		return err
	}
	spec.Env = claude.BuildEnv(claude.EnvOptions{GitConfigGlobal: gitconfig, GHConfigDir: ghDir})
	if profile.Guarded {
		self, err := os.Executable()
		if err != nil {
			return err
		}
		settings := claude.GuardSettings(fmt.Sprintf("%q hook-guard --stage %s", self, profile.Name), o.trust)
		spec.SettingsPath = filepath.Join(scratch, "settings.json")
		if err := claude.WriteSettings(spec.SettingsPath, settings); err != nil {
			return err
		}
	}

	ctx, stop := signal.NotifyContext(ctx, os.Interrupt, syscall.SIGTERM)
	defer stop()

	fmt.Fprintln(os.Stderr, spec.String())
	runner := &claude.Runner{Bin: o.bin}
	out := runner.Run(ctx, spec, claude.Callbacks{
		OnStart: func(pid int) { fmt.Fprintf(os.Stderr, "started pid %d\n", pid) },
		OnEvent: func(e claude.Event) {
			kind := e.Type
			if e.Subtype != "" {
				kind += "/" + e.Subtype
			}
			if s := e.Summary(); s != "" {
				fmt.Printf("%4d %-22s %s\n", e.Seq, kind, s)
			}
		},
	})

	report := map[string]any{
		"status":    out.Status,
		"reason":    out.Reason,
		"exit_code": out.ExitCode,
		"denials":   out.Denials,
		"duration":  out.EndedAt.Sub(out.StartedAt).Round(time.Millisecond).String(),
	}
	if out.Init != nil {
		report["session_id"] = out.Init.SessionID
		report["permission_mode"] = out.Init.PermissionMode
	}
	if out.Result != nil {
		report["cost_usd"] = out.Result.TotalCostUSD
		report["turns"] = out.Result.NumTurns
		if raw, ok := out.Result.Structured(); ok {
			report["structured_output"] = raw
		}
	}
	enc := json.NewEncoder(os.Stdout)
	enc.SetIndent("", "  ")
	_ = enc.Encode(report)
	if out.Status != claude.StatusSucceeded {
		return exitCode(1)
	}
	return nil
}

func readPrompt(path string) (string, error) {
	var data []byte
	var err error
	if path == "-" {
		data, err = io.ReadAll(os.Stdin)
	} else {
		data, err = os.ReadFile(path)
	}
	return string(data), err
}

// gitIdentity renders a gitconfig carrying the user's commit identity and
// nothing else: no credential helper, no URL rewrites.
func gitIdentity() string {
	get := func(key string) string {
		out, err := exec.Command("git", "config", "--global", "--get", key).Output()
		if err != nil {
			return ""
		}
		return strings.TrimSpace(string(out))
	}
	name, email := get("user.name"), get("user.email")
	if name == "" {
		name = "thefactory"
	}
	if email == "" {
		email = "thefactory@localhost"
	}
	return fmt.Sprintf("[user]\n\tname = %s\n\temail = %s\n[commit]\n\tgpgsign = false\n[init]\n\tdefaultBranch = main\n", name, email)
}
