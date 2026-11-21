package pipeline

import (
	"context"
	"encoding/json"
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/raulsh/tfy/internal/domain"
	"github.com/raulsh/tfy/internal/jobs"
	"github.com/raulsh/tfy/internal/store/db"
)

// addAccount creates an account, with secret when it is not empty.
func (h *harness) addAccount(id, kind string, settings domain.AccountSettings, secret string) {
	h.t.Helper()
	in := AccountInput{ID: id, Name: "Account " + id, CLI: domain.CLIClaude, Kind: kind, Settings: settings}
	if secret != "" {
		in.Secret = &secret
	}
	if _, err := h.p.CreateAccount(context.Background(), in); err != nil {
		h.t.Fatal(err)
	}
}

// useAccounts gives the project its accounts and per-stage settings.
func (h *harness) useAccounts(pr db.Project, runs domain.RunSettings) {
	h.t.Helper()
	s := domain.ParseProjectSettings(pr.Settings)
	s.Runs = runs
	if _, err := h.p.UpdateProject(context.Background(), pr.ID, ProjectInput{Name: pr.Name, Settings: &s}); err != nil {
		h.t.Fatal(err)
	}
}

// runEnv is the environment the fake CLI saw on the last run of kind.
func (h *harness) runEnv(kind string) []string {
	h.t.Helper()
	var env []string
	if err := json.Unmarshal([]byte(h.read(filepath.Join(h.control, "env-"+kind+".json"))), &env); err != nil {
		h.t.Fatal(err)
	}
	return env
}

func (h *harness) runs(unitID, kind string) []db.ListRunsRow {
	h.t.Helper()
	rows, err := h.st.Q.ListRuns(context.Background(), db.ListRunsParams{UnitID: unitID, Lim: 100})
	if err != nil {
		h.t.Fatal(err)
	}
	var out []db.ListRunsRow
	for _, r := range rows {
		if r.Kind == kind {
			out = append(out, r)
		}
	}
	slices.Reverse(out) // oldest first
	return out
}

func credentials(env []string) []string {
	var out []string
	for _, kv := range env {
		k, _, _ := strings.Cut(kv, "=")
		if strings.HasPrefix(k, "ANTHROPIC_") || strings.HasPrefix(k, "AWS_") || strings.HasPrefix(k, "CLAUDE_CODE_USE_") || k == "CLAUDE_CODE_OAUTH_TOKEN" {
			out = append(out, kv)
		}
	}
	slices.Sort(out)
	return out
}

// Each stage runs on the account the project assigns it, signed in with that
// account's credentials only: not tfy's, and not the provider in the user's
// Claude Code settings, which belong to this machine's account. The agent's
// commands see none of them.
func TestStagesRunOnTheirAccounts(t *testing.T) {
	h := newHarness(t)
	ctx := context.Background()
	if err := os.WriteFile(filepath.Join(os.Getenv("CLAUDE_CONFIG_DIR"), "settings.json"), []byte(`{"env": {"CLAUDE_CODE_USE_BEDROCK": "1", "AWS_PROFILE": "admin"}}`), 0o600); err != nil {
		t.Fatal(err)
	}
	t.Setenv("ANTHROPIC_API_KEY", "tfy's own")
	h.addAccount("sub1", domain.AccountToken, domain.AccountSettings{}, "tok1")
	h.addAccount("sub2", domain.AccountToken, domain.AccountSettings{}, "tok2")
	h.addAccount("bedrock", domain.AccountBedrock, domain.AccountSettings{Region: "us-east-1", AWSProfile: "bedrock-only", Models: map[string]string{"fable": "us.anthropic.claude-opus-5-5"}}, "")
	pr := h.project()
	h.useAccounts(pr, domain.RunSettings{
		Accounts: []string{"sub1", "sub2", "bedrock"},
		Stages:   domain.RunOverrides{"define": {Account: "sub2"}, "plan": {Account: "bedrock", Model: "fable", Effort: "low"}},
	})

	u, err := h.p.CreateUnit(ctx, CreateUnitInput{ProjectID: pr.ID, Kind: "bugfix", Title: "Health lies when the DB is down"})
	if err != nil {
		t.Fatal(err)
	}
	u = h.waitState(u.ID, domain.StateDefinitionReview)
	if got := credentials(h.runEnv("define")); !slices.Equal(got, []string{"CLAUDE_CODE_OAUTH_TOKEN=tok2"}) {
		t.Errorf("define signed in with %v, want sub2's token alone", got)
	}
	if _, err := h.p.Act(ctx, u.ID, ActionMarkReady, ActionInput{}); err != nil {
		t.Fatal(err)
	}
	h.waitState(u.ID, domain.StateSpecReview)
	want := []string{"ANTHROPIC_DEFAULT_FABLE_MODEL=us.anthropic.claude-opus-5-5", "AWS_PROFILE=bedrock-only", "AWS_REGION=us-east-1", "CLAUDE_CODE_USE_BEDROCK=1"}
	if got := credentials(h.runEnv("plan")); !slices.Equal(got, want) {
		t.Errorf("plan signed in with %v, want %v", got, want)
	}
	var args []string
	if err := json.Unmarshal([]byte(h.read(filepath.Join(h.control, "args-plan.json"))), &args); err != nil {
		t.Fatal(err)
	}
	if flagValue(args, "--model") != "fable" || flagValue(args, "--effort") != "low" {
		t.Errorf("plan ran with %s at %s", flagValue(args, "--model"), flagValue(args, "--effort"))
	}
	if deny := flagValue(args, "--disallowedTools"); deny != "Read(/"+h.p.Paths.Accounts()+"/**)" {
		t.Errorf("the run may read the accounts' secrets: --disallowedTools %q", deny)
	}
	for kind, account := range map[string]string{"define": "sub2", "plan": "bedrock"} {
		if rs := h.runs(u.ID, kind); len(rs) != 1 || rs[0].AccountID != account || rs[0].Engine != domain.CLIClaude {
			t.Errorf("%s runs: %+v", kind, rs)
		}
	}

	// The CLI runs this file in the shell of each of the agent's commands.
	cmd := exec.Command("sh", "-c", `. "$CLAUDE_ENV_FILE" && env`)
	cmd.Env = h.runEnv("plan")
	out, err := cmd.Output()
	if err != nil {
		t.Fatal(err)
	}
	if got := credentials(strings.Split(string(out), "\n")); len(got) != 0 {
		t.Errorf("the agent's commands see %v", got)
	}
}

// An account that reaches its usage limit mid-run is paused, and the run
// carries on with the project's next account, in the same session, without
// troubling anyone. Once the first account is free again, it is used again.
func TestRateLimitFailsOverWithTheSession(t *testing.T) {
	h := newHarness(t)
	ctx := context.Background()
	h.addAccount("sub1", domain.AccountToken, domain.AccountSettings{}, "tok1")
	h.addAccount("sub2", domain.AccountToken, domain.AccountSettings{}, "tok2")
	pr := h.project()
	h.useAccounts(pr, domain.RunSettings{Accounts: []string{"sub1", "sub2"}})
	if err := os.MkdirAll(h.control, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(h.control, "rate-limited-tok1"), []byte("worked"), 0o644); err != nil {
		t.Fatal(err)
	}

	u, err := h.p.CreateUnit(ctx, CreateUnitInput{ProjectID: pr.ID, Kind: "bugfix", Title: "Health lies when the DB is down"})
	if err != nil {
		t.Fatal(err)
	}
	u = h.waitState(u.ID, domain.StateDefinitionReview)
	if u.Attention != "" {
		t.Errorf("a run that failed over flagged the unit: %s: %s", u.Attention, u.AttentionDetail)
	}
	rs := h.runs(u.ID, "define")
	if len(rs) != 2 || rs[0].AccountID != "sub1" || rs[0].Status != "rate_limited" || rs[1].AccountID != "sub2" || rs[1].Status != "succeeded" {
		t.Fatalf("define runs: %+v", rs)
	}
	if rs[1].ParentRunID != rs[0].ID {
		t.Errorf("the second run must carry on the first one's session (parent %q)", rs[1].ParentRunID)
	}
	var args []string
	if err := json.Unmarshal([]byte(h.read(filepath.Join(h.control, "args-define.json"))), &args); err != nil {
		t.Fatal(err)
	}
	if flagValue(args, "--resume") != rs[0].SessionID || !slices.Contains(args, "--fork-session") {
		t.Errorf("the second run did not resume the first one's session: %v", args)
	}
	if p := h.read(filepath.Join(h.control, "prompt-define.txt")); !strings.Contains(p, "reached its usage limit") {
		t.Errorf("the second run was not told to carry on:\n%s", p)
	}
	sub1, _ := h.st.Q.GetAccount(ctx, "sub1")
	if !sub1.PausedUntil.Valid || !sub1.PausedUntil.Time.After(time.Now()) {
		t.Fatalf("sub1 is not paused: %+v", sub1.PausedUntil)
	}

	// While sub1 is paused, the next stage starts on sub2.
	if _, err := h.p.Act(ctx, u.ID, ActionMarkReady, ActionInput{}); err != nil {
		t.Fatal(err)
	}
	u = h.waitState(u.ID, domain.StateSpecReview)
	if rs := h.runs(u.ID, "plan"); len(rs) != 1 || rs[0].AccountID != "sub2" {
		t.Errorf("plan runs while sub1 is paused: %+v", rs)
	}
	// Once sub1 is free, it is the first choice again.
	if err := h.p.ResumeAccount(ctx, "sub1"); err != nil {
		t.Fatal(err)
	}
	if _, err := h.p.Act(ctx, u.ID, ActionIterate, ActionInput{Feedback: "Say what degraded means."}); err != nil {
		t.Fatal(err)
	}
	h.waitIdle("the plan revision", func() bool { return len(h.runs(u.ID, "plan")) == 2 && !h.p.Busy(ctx, u.ID) })
	if rs := h.runs(u.ID, "plan"); rs[1].AccountID != "sub1" {
		t.Errorf("after sub1 was resumed, plan ran on %s", rs[1].AccountID)
	}
}

// When every account a run may use is limited, the unit waits, and work
// resumes as soon as an account frees up.
func TestEveryAccountLimitedWaitsThenWakes(t *testing.T) {
	h := newHarness(t)
	ctx := context.Background()
	h.addAccount("sub1", domain.AccountToken, domain.AccountSettings{}, "tok1")
	pr := h.project()
	h.useAccounts(pr, domain.RunSettings{Accounts: []string{"sub1"}})
	if err := os.MkdirAll(h.control, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(h.control, "rate-limited-tok1"), nil, 0o644); err != nil {
		t.Fatal(err)
	}
	u, err := h.p.CreateUnit(ctx, CreateUnitInput{ProjectID: pr.ID, Kind: "bugfix", Title: "Health lies when the DB is down"})
	if err != nil {
		t.Fatal(err)
	}
	var job db.Job
	h.waitIdle("the define job to wait for an account", func() bool {
		_ = h.st.DB.QueryRow(`SELECT id, status, waiting_on, run_after FROM jobs WHERE unit_id = ? ORDER BY created_at DESC LIMIT 1`, u.ID).Scan(&job.ID, &job.Status, &job.WaitingOn, &job.RunAfter)
		return job.Status == jobs.StatusQueued && job.WaitingOn == jobs.WaitAccount
	})
	// Until the account's limit resets, an hour away in the fake.
	if wait := time.Until(job.RunAfter); wait < 50*time.Minute {
		t.Errorf("the job waits %s, not until the account's reset", wait)
	}
	if u, _ = h.st.Q.GetUnit(ctx, u.ID); u.Attention != string(domain.AttentionWaiting) {
		t.Errorf("unit attention %q, want waiting", u.Attention)
	}
	if err := h.p.ResumeAccount(ctx, "sub1"); err != nil {
		t.Fatal(err)
	}
	if u = h.waitState(u.ID, domain.StateDefinitionReview); u.Attention != "" {
		t.Errorf("the unit still waits: %s", u.AttentionDetail)
	}
}

// Settings can only name accounts that exist, that the project lists, and
// that can run the stage; an account in use cannot be deleted.
func TestAccountSettingsAreChecked(t *testing.T) {
	h := newHarness(t)
	ctx := context.Background()
	h.addAccount("sub1", domain.AccountToken, domain.AccountSettings{}, "tok1")
	pr := h.project()
	s := domain.ParseProjectSettings(pr.Settings)
	var invalid *InvalidError
	for _, runs := range []domain.RunSettings{
		{Accounts: []string{"nobody"}},
		{Accounts: []string{"sub1"}, Stages: domain.RunOverrides{"develop": {Account: "machine"}}},
		{Accounts: []string{"sub1"}, Stages: domain.RunOverrides{"develop": {Account: "sub1", Effort: "extreme"}}},
	} {
		s.Runs = runs
		if _, err := h.p.UpdateProject(ctx, pr.ID, ProjectInput{Name: pr.Name, Settings: &s}); !errors.As(err, &invalid) {
			t.Errorf("%+v: %v", runs, err)
		}
	}
	h.useAccounts(pr, domain.RunSettings{Accounts: []string{"sub1"}})
	if _, err := h.p.CreateUnit(ctx, CreateUnitInput{ProjectID: pr.ID, Title: "x", RunOverrides: domain.RunOverrides{"develop": {Account: "machine"}}}); !errors.As(err, &invalid) {
		t.Errorf("a unit override with an account the project does not use: %v", err)
	}
	var conflict *ConflictError
	if err := h.p.DeleteAccount(ctx, "sub1"); !errors.As(err, &conflict) || !strings.Contains(err.Error(), "Acme") {
		t.Errorf("deleting an account in use: %v", err)
	}
	if fi, err := os.Stat(h.p.Paths.AccountSecret("sub1")); err != nil || fi.Mode().Perm() != 0o600 {
		t.Errorf("secret file: %v (%v)", fi.Mode().Perm(), err)
	}
	if _, err := h.p.CreateAccount(ctx, AccountInput{ID: "sub2", Name: "x", CLI: domain.CLIClaude, Kind: domain.AccountToken}); !errors.As(err, &invalid) {
		t.Errorf("a token account without its token: %v", err)
	}
	// The account test asks the CLI how the account signs in.
	res, err := h.p.TestAccount(ctx, "sub1", true)
	if err != nil || !res.LoggedIn || res.Probe == nil || res.Probe.Status != "succeeded" || res.Probe.Reply != "ok" {
		t.Errorf("test: %+v %+v (%v)", res, res.Probe, err)
	}
}

// A session stays with its provider, so when the next account is another
// provider's, the run starts over there instead of waiting for the limit to
// reset: told that some of the work may be done already.
func TestFailoverToAnotherProviderStartsOver(t *testing.T) {
	h := newHarness(t)
	ctx := context.Background()
	h.addAccount("bedrock", domain.AccountBedrock, domain.AccountSettings{Region: "us-east-1", AWSProfile: "bedrock"}, "")
	h.addAccount("sub1", domain.AccountToken, domain.AccountSettings{}, "tok1")
	pr := h.project()
	h.useAccounts(pr, domain.RunSettings{Accounts: []string{"bedrock", "sub1"}})
	if err := os.MkdirAll(h.control, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(h.control, "rate-limited"), []byte("worked"), 0o644); err != nil {
		t.Fatal(err)
	}
	u, err := h.p.CreateUnit(ctx, CreateUnitInput{ProjectID: pr.ID, Kind: "bugfix", Title: "Health lies when the DB is down"})
	if err != nil {
		t.Fatal(err)
	}
	u = h.waitState(u.ID, domain.StateDefinitionReview)
	rs := h.runs(u.ID, "define")
	if len(rs) != 2 || rs[0].AccountID != "bedrock" || rs[0].Status != "rate_limited" || rs[1].AccountID != "sub1" || rs[1].Status != "succeeded" {
		t.Fatalf("define runs: %+v", rs)
	}
	if rs[1].ParentRunID != "" {
		t.Error("a session cannot move from Bedrock to a subscription: the second run must start one of its own")
	}
	if p := h.read(filepath.Join(h.control, "prompt-define.txt")); !strings.Contains(p, "An earlier attempt at this task") || !strings.Contains(p, "Health lies") {
		t.Errorf("the second run did not get the task again, with the note:\n%s", p)
	}
}

// A plan without usage credits for a model refuses that model alone: the
// account is not paused, its other stages keep running there, and the stage
// that needs the model carries on with the next account.
func TestModelNeedingCreditsMovesOnWithoutPausing(t *testing.T) {
	h := newHarness(t)
	ctx := context.Background()
	h.addAccount("pro", domain.AccountToken, domain.AccountSettings{}, "tok-pro")
	h.addAccount("max", domain.AccountToken, domain.AccountSettings{}, "tok-max")
	pr := h.project()
	h.useAccounts(pr, domain.RunSettings{Accounts: []string{"pro", "max"}, Stages: domain.RunOverrides{"define": {Model: "fable"}}})
	if err := os.MkdirAll(h.control, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(h.control, "credits-required-tok-pro"), []byte("fable"), 0o644); err != nil {
		t.Fatal(err)
	}
	u, err := h.p.CreateUnit(ctx, CreateUnitInput{ProjectID: pr.ID, Kind: "bugfix", Title: "Health lies when the DB is down"})
	if err != nil {
		t.Fatal(err)
	}
	u = h.waitState(u.ID, domain.StateDefinitionReview)
	if u.Attention != "" {
		t.Errorf("unit flagged: %s: %s", u.Attention, u.AttentionDetail)
	}
	rs := h.runs(u.ID, "define")
	if len(rs) != 2 || rs[0].AccountID != "pro" || !strings.Contains(rs[0].Reason, "usage credits") || rs[1].AccountID != "max" || rs[1].Status != "succeeded" {
		t.Fatalf("define runs: %+v", rs)
	}
	pro, _ := h.st.Q.GetAccount(ctx, "pro")
	if pro.PausedUntil.Valid {
		t.Errorf("a refused model paused the whole account until %v", pro.PausedUntil.Time)
	}
	if a := toAccount(pro); func() bool { _, b := a.Blocked("fable", time.Now()); return !b }() {
		t.Errorf("fable is not blocked on pro: %v", a.ModelBlocks)
	}
	// Planning runs on opus: pro is the first choice again, and the blocked
	// model is not tried there.
	if _, err := h.p.Act(ctx, u.ID, ActionMarkReady, ActionInput{}); err != nil {
		t.Fatal(err)
	}
	h.waitState(u.ID, domain.StateSpecReview)
	if rs := h.runs(u.ID, "plan"); len(rs) != 1 || rs[0].AccountID != "pro" {
		t.Errorf("plan runs: %+v", rs)
	}
	if _, err := h.p.Act(ctx, u.ID, ActionBack, ActionInput{}); err != nil {
		t.Fatal(err)
	}
	if _, err := h.p.Act(ctx, u.ID, ActionIterate, ActionInput{Feedback: "Say what degraded means."}); err != nil {
		t.Fatal(err)
	}
	h.waitIdle("the define revision", func() bool { return len(h.runs(u.ID, "define")) == 3 && !h.p.Busy(ctx, u.ID) })
	if rs := h.runs(u.ID, "define"); rs[2].AccountID != "max" {
		t.Errorf("the revision tried %s, whose plan cannot run fable", rs[2].AccountID)
	}
}

// When no account can run the model, waiting for a reset weeks away helps
// nobody: the unit stops with what to do.
func TestNoAccountForTheModelStops(t *testing.T) {
	h := newHarness(t)
	ctx := context.Background()
	h.addAccount("pro", domain.AccountToken, domain.AccountSettings{}, "tok-pro")
	pr := h.project()
	h.useAccounts(pr, domain.RunSettings{Accounts: []string{"pro"}, Stages: domain.RunOverrides{"define": {Model: "fable"}}})
	if err := os.MkdirAll(h.control, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(h.control, "credits-required-tok-pro"), []byte("fable"), 0o644); err != nil {
		t.Fatal(err)
	}
	u, err := h.p.CreateUnit(ctx, CreateUnitInput{ProjectID: pr.ID, Kind: "bugfix", Title: "Health lies when the DB is down"})
	if err != nil {
		t.Fatal(err)
	}
	h.waitIdle("the unit to stop", func() bool {
		u, _ = h.st.Q.GetUnit(ctx, u.ID)
		return u.Attention != "" && !h.p.Busy(ctx, u.ID)
	})
	if u.Attention != string(domain.AttentionFailed) || !strings.Contains(u.AttentionDetail, "needs usage credits on Account pro") || !strings.Contains(u.AttentionDetail, "another model") {
		t.Errorf("unit: %s: %s", u.Attention, u.AttentionDetail)
	}
}
