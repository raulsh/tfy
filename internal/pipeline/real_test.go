package pipeline

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"strings"
	"testing"
	"time"

	"github.com/raulsh/tfy/internal/config"
	"github.com/raulsh/tfy/internal/domain"
	"github.com/raulsh/tfy/internal/store"
	"github.com/raulsh/tfy/internal/store/db"
)

// TestRealClaude drives one unit, made from a GitHub issue, through define,
// an issue check, plan, develop, publish, review, merge, release and its
// retrospective with the real Claude Code CLI (sonnet, low effort) against the
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
	for _, kind := range []string{"define", "plan", "develop", "review", "release", "learn", "issue"} {
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
	saveIssues("acme/app", []fakeIssue{{Number: 3, Title: "Greet people from the command line", State: "OPEN", URL: "https://github.com/acme/app/issues/3",
		Author: fakeAuthor{Login: "ana"}, Labels: []map[string]string{{"name": "enhancement"}}, UpdatedAt: "2026-09-27T10:00:00Z",
		Body:     "It would be nice to have a script that says hello.",
		Comments: []fakeComment{{Author: fakeAuthor{Login: "bo"}, Body: "Please keep it POSIX sh: CI runs dash.", CreatedAt: "2026-09-27T11:00:00Z"}}}})
	u, err := h.p.CreateUnit(ctx, CreateUnitInput{ProjectID: pr.ID, Kind: "feature", Title: "Add a greeting script", Issue: "#3",
		Description: "Add bin/hello, a POSIX sh script that prints `Hello, <name>!` for its first argument, or `Hello, world!` without one. Keep it tiny."})
	if err != nil {
		t.Fatal(err)
	}
	wait := func(s domain.State) db.Unit { return h.waitStateFor(u.ID, s, 15*time.Minute) }
	u = wait(domain.StateDefinitionReview)

	// The issue check, with the real CLI: a vague issue next to a drafted
	// requirement.
	issue := h.issues(u.ID)[0]
	if _, err := h.p.CheckIssue(ctx, u.ID, issue.ID, "test"); err != nil {
		t.Fatal(err)
	}
	for deadline := time.Now().Add(10 * time.Minute); ; time.Sleep(time.Second) {
		row, _ := h.st.Q.GetUnitIssue(ctx, db.GetUnitIssueParams{ID: issue.ID, UnitID: u.ID})
		if (row.SuggestionState == SuggestionReady || row.SuggestionState == SuggestionNone) && !h.p.IssueCheckActive(ctx, issue.ID) {
			t.Logf("issue check: %s\n%s", row.SuggestionState, row.Suggestion)
			break
		}
		if row.SuggestionState == SuggestionFailed || time.Now().After(deadline) {
			t.Fatalf("issue check: %s %s", row.SuggestionState, row.Suggestion)
		}
	}
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
	if !strings.Contains(opened.Body, "Closes #3") && !regexp.MustCompile(`(?i)\b(close[sd]?|fix(e[sd])?|resolve[sd]?):?\s+#3\b`).MatchString(opened.Body) {
		t.Error("the pull request must close the issue")
	}
	// Merged: with no CI on the fake GitHub the unit is done at once, and
	// its retrospective runs.
	if _, err := h.p.Act(ctx, u.ID, ActionMerge, ActionInput{}); err != nil {
		t.Fatal(err)
	}
	u = wait(domain.StateDone)
	retro := h.waitDocFor(u.ID, DocRetrospective, 1, 10*time.Minute)
	t.Logf("retrospective:\n%s\n%s", retro.Content, retro.Meta)

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

// goModule commits a Go module to a repository on the fake GitHub. With a
// dependency, it pins that module's commit, fetched through a git config
// that serves the fake GitHub's repositories (as tfy's runs do).
func (h *harness) goModule(repo string, files map[string]string, dep, depRev string) {
	h.t.Helper()
	dir := filepath.Join(h.t.TempDir(), "w")
	run(h.t, "", "git", "clone", "-q", filepath.Join(h.remotes, repo+".git"), dir)
	for name, content := range files {
		if err := os.WriteFile(filepath.Join(dir, name), []byte(content), 0o644); err != nil {
			h.t.Fatal(err)
		}
	}
	if dep != "" {
		gitconfig := filepath.Join(h.t.TempDir(), "gitconfig")
		rules := fmt.Sprintf("[url %q]\n\tinsteadOf = https://%s\n", "file://"+filepath.Join(h.remotes, strings.TrimPrefix(dep, "github.com/")+".git"), dep)
		if err := os.WriteFile(gitconfig, []byte(rules), 0o644); err != nil {
			h.t.Fatal(err)
		}
		cmd := exec.Command("go", "get", dep+"@"+depRev)
		cmd.Dir = dir
		cmd.Env = append(os.Environ(), "GIT_CONFIG_GLOBAL="+gitconfig, "GOPRIVATE=github.com/acme/*", "GOFLAGS=-mod=mod")
		if out, err := cmd.CombinedOutput(); err != nil {
			h.t.Fatalf("go get %s: %v\n%s", dep, err, out)
		}
	}
	run(h.t, dir, "git", "add", "-A")
	run(h.t, dir, "git", "-c", "user.name=t", "-c", "user.email=t@t", "commit", "-q", "-m", "feat: start "+repo)
	run(h.t, dir, "git", "push", "-q", "origin", "HEAD:main")
}

func goModLine(t *testing.T, dir, module string) string {
	t.Helper()
	b, err := os.ReadFile(filepath.Join(dir, "go.mod"))
	if err != nil {
		t.Fatal(err)
	}
	for _, line := range strings.Split(string(b), "\n") {
		if strings.Contains(line, module+" ") {
			return strings.TrimSpace(line)
		}
	}
	return ""
}

// TestRealClaudeMergePlan runs a change across two Go modules, one using
// the other, with the real Claude Code CLI and Go toolchain against the
// local fake GitHub: the plan must order the merges, development must pin
// the dependency's unit-branch commit, and after the dependency is squashed
// into main the update round must pin the merged commit. Opt-in, like
// TestRealClaude.
func TestRealClaudeMergePlan(t *testing.T) {
	if os.Getenv("TFY_REAL_CLAUDE") == "" {
		t.Skip("set TFY_REAL_CLAUDE=1 to run against the real Claude Code CLI")
	}
	bin, err := exec.LookPath("claude")
	if err != nil {
		t.Skip("claude is not installed")
	}
	if _, err := exec.LookPath("go"); err != nil {
		t.Skip("go is not installed")
	}
	h := newHarness(t)
	ctx := context.Background()
	h.p.Runner.Bin, h.p.Config.ClaudeBin = bin, bin
	for _, kind := range []string{"define", "plan", "develop", "review", "release", "learn", "issue"} {
		h.p.Config.Stages[kind] = config.Stage{Model: "sonnet", Effort: "low", Budget: 1, Timeout: 10 * time.Minute}
	}
	if out, err := exec.Command("go", "build", "-o", h.p.Paths.GuardBin(), "../../cmd/tfy").CombinedOutput(); err != nil {
		t.Fatalf("build tfy: %v\n%s", err, out)
	}
	h.addRemote("acme/api")
	h.goModule("acme/api", map[string]string{
		"go.mod": "module github.com/acme/api\n\ngo 1.22\n",
		"api.go": "package api\n\n// Name is the service's name.\nconst Name = \"api\"\n",
	}, "", "")
	apiMain := remoteHead("acme/api", "main")
	h.goModule("acme/app", map[string]string{
		"go.mod":  "module github.com/acme/app\n\ngo 1.22\n",
		"main.go": "package main\n\nimport (\n\t\"fmt\"\n\n\t\"github.com/acme/api\"\n)\n\nfunc main() { fmt.Println(api.Name) }\n",
	}, "github.com/acme/api", apiMain)
	pr := h.project()
	if _, err := h.p.LinkRepo(ctx, pr.ID, "acme/api"); err != nil {
		t.Fatal(err)
	}
	u, err := h.p.CreateUnit(ctx, CreateUnitInput{ProjectID: pr.ID, Kind: "feature", Title: "Greet from the api",
		Description: "Add `func Greet(name string) string` to acme/api, returning \"Hello, <name>, from api\". Make acme/app print api.Greet(\"world\") instead of api.Name. acme/app uses acme/api as a Go module pinned in its go.mod; the committed go.mod must end up pinning acme/api's merged commit."})
	if err != nil {
		t.Fatal(err)
	}
	wait := func(s domain.State) db.Unit { return h.waitStateFor(u.ID, s, 20*time.Minute) }
	u = wait(domain.StateDefinitionReview)
	if _, err := h.p.Act(ctx, u.ID, ActionMarkReady, ActionInput{}); err != nil {
		t.Fatal(err)
	}
	u = wait(domain.StateSpecReview)
	plan, _ := h.p.MergePlan(ctx, u)
	t.Logf("merge plan: %+v", plan)
	if len(plan.Steps) != 2 || plan.Steps[0].Repos[0] != "acme/api" {
		t.Fatalf("the plan must merge acme/api first, then acme/app: %+v", plan)
	}
	if _, err := h.p.Act(ctx, u.ID, ActionApproveSpec, ActionInput{}); err != nil {
		t.Fatal(err)
	}
	u = wait(domain.StateAwaitingMerge)
	urs, _ := h.st.Q.ListUnitRepos(ctx, u.ID)
	var appDir, apiBranchHead string
	for _, ur := range urs {
		switch ur.FullName {
		case "acme/app":
			appDir = ur.CheckoutPath
		case "acme/api":
			apiBranchHead = remoteHead("acme/api", ur.Branch)
		}
	}
	first := goModLine(t, appDir, "github.com/acme/api")
	t.Logf("after development, app requires: %s (api's branch head %s)", first, apiBranchHead[:12])
	if !strings.Contains(first, apiBranchHead[:12]) {
		t.Errorf("development should pin api's commit on the unit's branch, got %q", first)
	}

	if _, err := h.p.Act(ctx, u.ID, ActionMerge, ActionInput{}); err != nil {
		t.Fatal(err)
	}
	u = wait(domain.StateAwaitingMerge)
	merged := loadPRs("acme/api")[0].MergeCommit.Oid
	second := goModLine(t, appDir, "github.com/acme/api")
	t.Logf("after step 1 merged as %s, app requires: %s", merged[:12], second)
	if u.MergeStep != 1 || !strings.Contains(second, merged[:12]) {
		t.Errorf("the update round must pin the merged commit %s, got %q (step %d)", merged[:12], second, u.MergeStep)
	}
	build := exec.Command("go", "build", "./...")
	build.Dir = appDir
	build.Env = append(os.Environ(), "GIT_CONFIG_GLOBAL="+filepath.Join(h.p.Paths.Root, "agent", "gitconfig"), "GOPROXY=off")
	if out, err := build.CombinedOutput(); err != nil {
		t.Errorf("app no longer builds with its pinned api: %v\n%s", err, out)
	}

	if _, err := h.p.Act(ctx, u.ID, ActionMerge, ActionInput{}); err != nil {
		t.Fatal(err)
	}
	u = wait(domain.StateDone)
	runs, _ := h.st.Q.ListRuns(ctx, db.ListRunsParams{UnitID: u.ID, Lim: 50})
	total := 0.0
	for i := len(runs) - 1; i >= 0; i-- {
		r := runs[i]
		total += r.CostUsd
		t.Logf("%-8s %-9s $%.3f %d turns %d denials", r.Kind, r.Status, r.CostUsd, r.Turns, r.Denials)
		if r.Status != "succeeded" {
			t.Errorf("%s run %s: %s", r.Kind, r.Status, r.Reason)
		}
	}
	t.Logf("total $%.2f", total)
}
