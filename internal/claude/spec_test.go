package claude

import (
	"encoding/json"
	"os"
	"os/exec"
	"path/filepath"
	"slices"
	"strings"
	"testing"

	"github.com/raulsh/tfy/internal/domain"
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

// A provider such as Bedrock reaches the CLI, from tfy's environment and from
// the user's Claude Code settings, which runs don't load. The agent's
// commands get none of it, and publishing credentials stay out either way.
func TestBuildEnvGivesTheProviderToTheCLIAlone(t *testing.T) {
	dir := t.TempDir()
	t.Setenv("CLAUDE_CONFIG_DIR", dir)
	settings := `{"env": {"CLAUDE_CODE_USE_BEDROCK": "1", "AWS_PROFILE": "bedrock", "AWS_REGION": "us-west-2", "GH_TOKEN": "secret"}, "permissions": {"allow": ["Bash"]}}`
	if err := os.WriteFile(filepath.Join(dir, "settings.json"), []byte(settings), 0o600); err != nil {
		t.Fatal(err)
	}
	t.Setenv("AWS_REGION", "us-east-1")
	t.Setenv("AWS_SECRET_ACCESS_KEY", "key")
	t.Setenv("GH_TOKEN", "secret")
	t.Setenv("SSH_AUTH_SOCK", "/tmp/agent")
	shellEnv := filepath.Join(t.TempDir(), "shell-env.sh")
	if err := WriteShellEnv(shellEnv); err != nil {
		t.Fatal(err)
	}
	env := BuildEnv(EnvOptions{ShellEnvFile: shellEnv})
	for _, want := range []string{"CLAUDE_CODE_USE_BEDROCK=1", "AWS_PROFILE=bedrock", "AWS_REGION=us-west-2", "AWS_SECRET_ACCESS_KEY=key", "CLAUDE_ENV_FILE=" + shellEnv} {
		if !slices.Contains(env, want) {
			t.Errorf("the CLI lacks %s", want)
		}
	}

	// The CLI runs the file in the shell of each of the agent's commands.
	cmd := exec.Command("sh", "-c", `. "$CLAUDE_ENV_FILE" && env`)
	cmd.Env = env
	out, err := cmd.Output()
	if err != nil {
		t.Fatal(err)
	}
	seen := "\n" + string(out)
	for _, k := range append(slices.Clone(credentialEnv), "GH_TOKEN", "SSH_AUTH_SOCK") {
		if strings.Contains(seen, "\n"+k+"=") {
			t.Errorf("the agent's commands see %s", k)
		}
	}
	if !strings.Contains(seen, "\nHOME=") {
		t.Errorf("the agent's commands lost the rest of the environment:%s", seen)
	}
}

// An account other than this machine's signs the CLI in with exactly its own
// credentials: none inherited from tfy or from the user's Claude Code
// settings, which could sign it in elsewhere.
func TestBuildEnvForAnAccount(t *testing.T) {
	dir := t.TempDir()
	t.Setenv("CLAUDE_CONFIG_DIR", dir)
	if err := os.WriteFile(filepath.Join(dir, "settings.json"), []byte(`{"env": {"CLAUDE_CODE_USE_BEDROCK": "1", "AWS_PROFILE": "admin"}}`), 0o600); err != nil {
		t.Fatal(err)
	}
	t.Setenv("ANTHROPIC_API_KEY", "inherited")
	t.Setenv("ANTHROPIC_AUTH_TOKEN", "inherited")
	t.Setenv("AWS_REGION", "eu-west-1")
	models := map[string]string{"fable": "us.anthropic.claude-opus-5-5"}
	cases := []struct {
		account domain.Account
		secret  string
		want    []string
	}{
		{domain.Account{Kind: domain.AccountToken, Settings: domain.AccountSettings{Models: models}}, "tok", []string{"CLAUDE_CODE_OAUTH_TOKEN=tok", "ANTHROPIC_DEFAULT_FABLE_MODEL=us.anthropic.claude-opus-5-5"}},
		{domain.Account{Kind: domain.AccountAPIKey}, "key", []string{"ANTHROPIC_API_KEY=key"}},
		{domain.Account{Kind: domain.AccountBedrock, Settings: domain.AccountSettings{Region: "us-east-1", AWSProfile: "bedrock"}}, "", []string{"CLAUDE_CODE_USE_BEDROCK=1", "AWS_REGION=us-east-1", "AWS_PROFILE=bedrock"}},
		{domain.Account{Kind: domain.AccountBedrock, Settings: domain.AccountSettings{Region: "us-east-1"}}, "bk", []string{"CLAUDE_CODE_USE_BEDROCK=1", "AWS_REGION=us-east-1", "AWS_BEARER_TOKEN_BEDROCK=bk"}},
	}
	for _, c := range cases {
		env := BuildEnv(EnvOptions{Account: &c.account, Secret: c.secret})
		var got []string
		for _, kv := range env {
			if k, _, _ := strings.Cut(kv, "="); slices.Contains(credentialEnv, k) {
				got = append(got, kv)
			}
		}
		slices.Sort(got)
		want := slices.Sorted(slices.Values(c.want))
		if !slices.Equal(got, want) {
			t.Errorf("%s: credentials %v, want %v", c.account.Kind, got, want)
		}
	}
	// This machine's account keeps what tfy and the user's settings give it.
	machine := domain.Account{Kind: domain.AccountMachine}
	env := BuildEnv(EnvOptions{Account: &machine})
	for _, want := range []string{"ANTHROPIC_API_KEY=inherited", "CLAUDE_CODE_USE_BEDROCK=1", "AWS_PROFILE=admin"} {
		if !slices.Contains(env, want) {
			t.Errorf("this machine's account lacks %s", want)
		}
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
