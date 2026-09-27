package claude

import (
	"encoding/json"
	"slices"
	"strings"
	"testing"
)

func TestDevelopArgs(t *testing.T) {
	spec := &Spec{
		Prompt:        "implement the spec",
		Cwd:           "/w",
		Model:         "opus",
		Effort:        "high",
		MaxBudgetUSD:  20,
		SettingsPath:  "/w/.tfy/settings.json",
		ResumeSession: "sid-1",
		ForkSession:   true,
	}
	Profiles["develop"].Apply(spec)
	args := spec.Args()

	pairs := map[string]string{
		"--output-format":      "stream-json",
		"--setting-sources":    "",
		"--mcp-config":         emptyMCPConfig,
		"--permission-prompts": "none",
		"--permission-mode":    ModeAuto,
		"--model":              "opus",
		"--effort":             "high",
		"--max-budget-usd":     "20",
		"--settings":           "/w/.tfy/settings.json",
		"--resume":             "sid-1",
		"--tools":              "Bash,Read,Write,Edit,NotebookEdit,WebFetch,WebSearch",
	}
	for flag, want := range pairs {
		i := slices.Index(args, flag)
		if i < 0 || i+1 >= len(args) || args[i+1] != want {
			t.Errorf("%s: want %q in %q", flag, want, args)
		}
	}
	for _, flag := range []string{"-p", "--verbose", "--include-hook-events", "--strict-mcp-config", "--disable-slash-commands", "--fork-session"} {
		if !slices.Contains(args, flag) {
			t.Errorf("missing %s", flag)
		}
	}
	if slices.Contains(args, "--session-id") || slices.Contains(args, "--no-session-persistence") {
		t.Error("resume must not also set a session id or disable persistence")
	}
	if !spec.RequireGuard || !spec.ForbidPush {
		t.Error("develop runs are guarded and may not push")
	}
	if strings.Contains(strings.Join(args, " "), "implement the spec") {
		t.Error("the prompt goes on stdin, not argv")
	}
}

func TestVariadicFlagsLast(t *testing.T) {
	spec := &Spec{Prompt: "x", Cwd: "/w", NoSessionPersistence: true}
	Profiles["define"].Apply(spec)
	args := spec.Args()
	i := slices.Index(args, "--allowedTools")
	if i < 0 {
		t.Fatal("no --allowedTools")
	}
	// Relative path rules are anchored to the working directory: the CLI
	// would resolve them against wherever the shell last cd'd to.
	if got := args[i+1:]; !slices.Equal(got, []string{"Read", "Edit(//w/docs/**)", "Write(//w/docs/**)"}) {
		t.Errorf("allowed tools = %q; each rule must be its own argument, absolute, and last", got)
	}
}

func TestNoToolsProfile(t *testing.T) {
	spec := &Spec{Prompt: "x", Cwd: "/w"}
	Profiles["triage"].Apply(spec)
	args := spec.Args()
	i := slices.Index(args, "--tools")
	if i < 0 || args[i+1] != "" {
		t.Errorf("triage must pass --tools \"\": %q", args)
	}
	if !spec.NoSessionPersistence {
		t.Error("triage sessions are not persisted")
	}
}

func TestValidate(t *testing.T) {
	bad := []Spec{
		{Prompt: "x"},
		{Cwd: "/w"},
		{Prompt: "x", Cwd: "/w", SessionID: "a", NoSessionPersistence: true},
		{Prompt: "x", Cwd: "/w", ForkSession: true},
		{Prompt: "x", Cwd: "/w", JSONSchema: []byte("{")},
	}
	for i, s := range bad {
		if s.Validate() == nil {
			t.Errorf("case %d: expected an error", i)
		}
	}
	good := Spec{Prompt: "x", Cwd: "/w", ResumeSession: "a", ForkSession: true}
	if err := good.Validate(); err != nil {
		t.Error(err)
	}
}

func TestBuildEnvDropsPublishingCredentials(t *testing.T) {
	t.Setenv("GH_TOKEN", "secret")
	t.Setenv("GITHUB_TOKEN", "secret")
	t.Setenv("SSH_AUTH_SOCK", "/tmp/agent")
	t.Setenv("GOPATH", "/go")
	env := BuildEnv(EnvOptions{GitConfigGlobal: "/f/gitconfig", GHConfigDir: "/f/gh", Extra: []string{"TERM=xterm"}})
	joined := strings.Join(env, "\n")
	for _, bad := range []string{"GH_TOKEN", "GITHUB_TOKEN", "SSH_AUTH_SOCK"} {
		if strings.Contains(joined, bad+"=") {
			t.Errorf("%s leaked into the run", bad)
		}
	}
	for _, want := range []string{"GOPATH=/go", "GIT_CONFIG_GLOBAL=/f/gitconfig", "GH_CONFIG_DIR=/f/gh", "CLAUDE_CODE_DISABLE_AUTO_MEMORY=1", "CLAUDE_BASH_MAINTAIN_PROJECT_WORKING_DIR=1", "TERM=xterm"} {
		if !slices.Contains(env, want) {
			t.Errorf("missing %s", want)
		}
	}
	if slices.Contains(env, "TERM=dumb") {
		t.Error("Extra must override defaults")
	}
}

func TestAttributionSettingsSerializeEmptyStrings(t *testing.T) {
	s := GuardSettings("guard", nil)
	s.Attribution = &Attribution{}
	b, _ := json.Marshal(s)
	if !strings.Contains(string(b), `"attribution":{"commit":"","pr":""}`) {
		t.Fatalf("settings = %s", b)
	}
}

func TestAnchorRules(t *testing.T) {
	got := anchorRules([]string{"Read", "Write(./docs/**)", "Bash(git diff *)", "Edit(//already/abs/**)", "Edit(src/**)"}, "/home/u/.tfy/workspaces/u5-x")
	want := []string{"Read", "Write(//home/u/.tfy/workspaces/u5-x/docs/**)", "Bash(git diff *)", "Edit(//already/abs/**)", "Edit(src/**)"}
	if !slices.Equal(got, want) {
		t.Errorf("anchorRules = %q, want %q", got, want)
	}
	if got := anchorRules([]string{"Write(./docs/**)"}, "relative"); got[0] != "Write(./docs/**)" {
		t.Error("without an absolute working directory, rules stay as they are")
	}
}
