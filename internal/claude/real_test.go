package claude

import (
	"context"
	"fmt"
	"io"
	"log/slog"
	"os"
	"os/exec"
	"path/filepath"
	"testing"
	"time"
)

// TestRealPathRulesSurviveCd checks, against the real Claude Code CLI, that a
// run allowed to write under docs/ still can after its shell cd'd into a
// checkout: seen for real, plan runs lost their spec that way (docs/spike.md).
// It spends a few cents, so it only runs with TFY_REAL_CLAUDE=1:
//
//	TFY_REAL_CLAUDE=1 go test -run TestRealPathRulesSurviveCd -v ./internal/claude/
func TestRealPathRulesSurviveCd(t *testing.T) {
	if os.Getenv("TFY_REAL_CLAUDE") == "" {
		t.Skip("set TFY_REAL_CLAUDE=1 to run against the real Claude Code CLI")
	}
	bin, err := exec.LookPath("claude")
	if err != nil {
		t.Skip("claude is not installed")
	}
	ws := t.TempDir()
	if err := os.MkdirAll(filepath.Join(ws, "docs"), 0o755); err != nil {
		t.Fatal(err)
	}
	if out, err := exec.Command("git", "init", "-q", filepath.Join(ws, "app")).CombinedOutput(); err != nil {
		t.Fatalf("%v: %s", err, out)
	}
	if err := os.WriteFile(filepath.Join(ws, "app", "README.md"), []byte("app\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	spec := &Spec{
		Prompt: fmt.Sprintf("Do exactly these steps and nothing else. 1) Run this Bash command: cd %s/app && ls  "+
			"2) Use the Write tool to create %s/docs/spec.md containing hi.  3) Report whether step 2 succeeded.", ws, ws),
		Cwd: ws, Model: "sonnet", Effort: "low", MaxBudgetUSD: 0.5, Timeout: 5 * time.Minute,
		SettingSources: []string{"project"}, NoSessionPersistence: true,
	}
	Profiles["plan"].Apply(spec)
	spec.RequireGuard = false // no guard hook here: this is about the path rules
	spec.Env = BuildEnv(EnvOptions{})
	out := (&Runner{Bin: bin, Log: slog.New(slog.NewTextHandler(io.Discard, nil))}).Run(context.Background(), spec, Callbacks{})
	if out.Status != StatusSucceeded {
		t.Fatalf("run %s: %s\n%s", out.Status, out.Reason, out.Stderr)
	}
	if out.Result != nil {
		t.Logf("result: %s (denials %d)", out.Result.Result, out.Denials)
	}
	if b, err := os.ReadFile(filepath.Join(ws, "docs", "spec.md")); err != nil || len(b) == 0 {
		t.Fatalf("docs/spec.md was not written after a cd into the checkout (%v)", err)
	}
}
