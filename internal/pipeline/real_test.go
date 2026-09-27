package pipeline

import (
	"context"
	"encoding/json"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/raulsh/tfy/internal/config"
	"github.com/raulsh/tfy/internal/domain"
	"github.com/raulsh/tfy/internal/store"
	"github.com/raulsh/tfy/internal/store/db"
)

// TestRealClaude drives one unit through define, plan, develop, publish and
// review with the real Claude Code CLI (sonnet, low effort) against the
// local fake GitHub. It spends a little money and never touches GitHub, so
// it only runs with TFY_REAL_CLAUDE=1:
//
//	TFY_REAL_CLAUDE=1 go test -run TestRealClaude -v -timeout 30m ./internal/pipeline/
func TestRealClaude(t *testing.T) {
	if os.Getenv("TFY_REAL_CLAUDE") == "" {
		t.Skip("set TFY_REAL_CLAUDE=1 to run against the real Claude Code CLI")
	}
	bin, err := exec.LookPath("claude")
	if err != nil {
		t.Skip("claude is not installed")
	}
	h := newHarness(t)
	ctx := context.Background()
	h.p.Runner.Bin, h.p.Config.ClaudeBin = bin, bin
	for _, kind := range []string{"define", "plan", "develop", "review", "release", "learn"} {
		h.p.Config.Stages[kind] = config.Stage{Model: "sonnet", Effort: "low", Budget: 1, Timeout: 10 * time.Minute}
	}
	// The guard and Stop hooks run the tfy binary from the data directory.
	if out, err := exec.Command("go", "build", "-o", h.p.Paths.GuardBin(), "../../cmd/tfy").CombinedOutput(); err != nil {
		t.Fatalf("build tfy: %v\n%s", err, out)
	}
	hookLog := filepath.Join(h.control, "repo-hook.log")
	if err := os.MkdirAll(h.control, 0o755); err != nil {
		t.Fatal(err)
	}
	h.commitToRemote("acme/app", map[string]string{
		"README.md":                "# app\n\nA tiny shell toolbox.\n",
		"CLAUDE.md":                "# app\n\nShell scripts live in bin/ and are POSIX sh. Test a script by running it.\n",
		".claude/rules/commits.md": "# Commits\n\nEvery commit message is one line in Conventional Commits form, such as `feat: add hello`. Never add a Co-Authored-By trailer or any other trailer.\n",
		".claude/hooks/log.sh":     "#!/bin/sh\necho \"$CLAUDE_PROJECT_DIR|$(pwd)\" >> " + shellQuote(hookLog) + "\n",
		".claude/settings.json":    `{"hooks": {"PostToolUse": [{"matcher": "Bash", "hooks": [{"type": "command", "command": "\"$CLAUDE_PROJECT_DIR\"/.claude/hooks/log.sh"}]}]}}`,
	})
	pr := h.project()
	conv := "Pull request titles are Conventional Commits, like the commit messages."
	if _, err := h.p.UpdateProject(ctx, pr.ID, ProjectInput{Name: pr.Name, Conventions: &conv}); err != nil {
		t.Fatal(err)
	}
	u, err := h.p.CreateUnit(ctx, CreateUnitInput{ProjectID: pr.ID, Kind: "feature", Title: "Add a greeting script",
		Description: "Add bin/hello, a POSIX sh script that prints `Hello, <name>!` for its first argument, or `Hello, world!` without one. Keep it tiny."})
	if err != nil {
		t.Fatal(err)
	}
	wait := func(s domain.State) db.Unit { return h.waitStateFor(u.ID, s, 15*time.Minute) }
	u = wait(domain.StateDefinitionReview)
	if _, err := h.p.Act(ctx, u.ID, ActionMarkReady, ActionInput{}); err != nil {
		t.Fatal(err)
	}
	u = wait(domain.StateSpecReview)
	if _, err := h.p.Act(ctx, u.ID, ActionApproveSpec, ActionInput{}); err != nil {
		t.Fatal(err)
	}
	u = wait(domain.StateAwaitingMerge)

	urs, _ := h.st.Q.ListUnitRepos(ctx, u.ID)
	ur := urs[0]
	log, err := exec.Command("git", "-C", filepath.Join(h.remotes, "acme", "app.git"), "log", "--format=%B%x00", "main.."+ur.Branch).Output()
	if err != nil {
		t.Fatal(err)
	}
	for _, msg := range strings.Split(strings.TrimRight(string(log), "\x00\n"), "\x00") {
		msg = strings.TrimSpace(msg)
		if strings.Contains(msg, "\n") || strings.Contains(strings.ToLower(msg), "co-authored-by") || !strings.Contains(msg, ": ") {
			t.Errorf("commit message breaks the repository's rules: %q", msg)
		}
	}
	opened := loadPRs("acme/app")[0]
	t.Logf("pull request: %q\n%s", opened.Title, opened.Body)
	if !strings.Contains(opened.Title, ": ") || opened.Title == u.Title || !strings.Contains(opened.Body, "<!-- tfy:U-1 -->") {
		t.Errorf("the pull request must carry Claude's conventional title and description: %q", opened.Title)
	}
	hooks, _ := os.ReadFile(hookLog)
	if !strings.Contains(string(hooks), ur.CheckoutPath+"|"+ur.CheckoutPath) {
		t.Errorf("the repository's hook must run from its checkout, got:\n%s", hooks)
	}
	runs, _ := h.st.Q.ListRuns(ctx, db.ListRunsParams{UnitID: u.ID, Lim: 50})
	marked := false
	for _, r := range runs {
		if r.Status != "succeeded" {
			t.Errorf("%s run %s: %s", r.Kind, r.Status, r.Reason)
		}
		evs, _ := h.st.Q.ListRunEvents(ctx, db.ListRunEventsParams{RunID: r.ID, After: 0, Lim: 5000})
		for _, e := range evs {
			var p struct {
				Stdout string `json:"stdout"`
			}
			if e.Subtype == "hook_response" && json.Unmarshal([]byte(e.Payload), &p) == nil && strings.Contains(p.Stdout, "tfy-guard") {
				marked = true
			}
		}
		t.Logf("%-8s %-9s $%.3f %d turns %d denials", r.Kind, r.Status, r.CostUsd, r.Turns, r.Denials)
	}
	if !marked {
		t.Error("the CLI must report the guard's stdout marker")
	}
	acts, _ := h.st.Q.ListUnitActivity(ctx, db.ListUnitActivityParams{UnitID: store.NullString(u.ID), Lim: 200})
	for i := len(acts) - 1; i >= 0; i-- {
		t.Logf("activity: %s", acts[i].Message)
	}
}
