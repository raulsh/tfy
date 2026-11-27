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
	"github.com/raulsh/tfy/internal/store"
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
	if b, ok := toAccount(pro).Blocked("fable", time.Now()); !ok || b.Cause != domain.BlockCredits {
		t.Errorf("fable on pro: %+v blocked=%v, want blocked for want of credits", b, ok)
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
	if u.Attention != string(domain.AttentionModelUnavailable) || !strings.Contains(u.AttentionDetail, "Account pro cannot run fable without usage credits") || !strings.Contains(u.AttentionDetail, "another model") {
		t.Errorf("unit: %s: %s", u.Attention, u.AttentionDetail)
	}
}

// A model's own usage window, such as Fable's weekly one, refuses that model
// alone: the run moves on to an account that can still run it, and the
// account stays free for runs of its other models.
func TestModelLimitMovesOnWithoutPausing(t *testing.T) {
	h := newHarness(t)
	ctx := context.Background()
	h.addAccount("sub1", domain.AccountToken, domain.AccountSettings{}, "tok1")
	h.addAccount("sub2", domain.AccountToken, domain.AccountSettings{}, "tok2")
	pr := h.project()
	h.useAccounts(pr, domain.RunSettings{Accounts: []string{"sub1", "sub2"}, Stages: domain.RunOverrides{"define": {Model: "fable"}}})
	h.setControl("model-limited-tok1", "fable")
	u, err := h.p.CreateUnit(ctx, CreateUnitInput{ProjectID: pr.ID, Kind: "bugfix", Title: "Health lies when the DB is down"})
	if err != nil {
		t.Fatal(err)
	}
	if u = h.waitState(u.ID, domain.StateDefinitionReview); u.Attention != "" {
		t.Errorf("unit flagged: %s: %s", u.Attention, u.AttentionDetail)
	}
	rs := h.runs(u.ID, "define")
	if len(rs) != 2 || rs[0].AccountID != "sub1" || rs[0].Status != "rate_limited" || !strings.Contains(rs[0].Reason, "Fable limit") || rs[1].AccountID != "sub2" || rs[1].Status != "succeeded" {
		t.Fatalf("define runs: %+v", rs)
	}
	sub1, _ := h.st.Q.GetAccount(ctx, "sub1")
	if sub1.PausedUntil.Valid {
		t.Errorf("Fable's limit paused the whole account until %v", sub1.PausedUntil.Time)
	}
	// Blocked by family: a full model name of Fable is refused too.
	if b, ok := toAccount(sub1).Blocked("claude-fable-5-2", time.Now()); !ok || b.Cause != domain.BlockLimit || time.Until(b.Until) < 47*time.Hour {
		t.Errorf("fable on sub1: %+v blocked=%v, want limited until the window resets", b, ok)
	}
	// Planning runs on opus: sub1 is the first choice again.
	if _, err := h.p.Act(ctx, u.ID, ActionMarkReady, ActionInput{}); err != nil {
		t.Fatal(err)
	}
	h.waitState(u.ID, domain.StateSpecReview)
	if rs := h.runs(u.ID, "plan"); len(rs) != 1 || rs[0].AccountID != "sub1" {
		t.Errorf("plan runs: %+v", rs)
	}
}

// Seen for real: the only account used up its Fable limit, with its other
// models free. The unit stops and says so; a person moves the stage to
// another model and retries, and the block on Fable stays for the runs
// still set to it.
func TestSwitchModelWhenNoAccountCanRunIt(t *testing.T) {
	h := newHarness(t)
	ctx := context.Background()
	h.addAccount("sub1", domain.AccountToken, domain.AccountSettings{}, "tok1")
	pr := h.project()
	h.useAccounts(pr, domain.RunSettings{Accounts: []string{"sub1"}, Stages: domain.RunOverrides{"define": {Model: "fable"}}})
	h.setControl("model-limited-tok1", "fable")
	u, err := h.p.CreateUnit(ctx, CreateUnitInput{ProjectID: pr.ID, Kind: "bugfix", Title: "Health lies when the DB is down"})
	if err != nil {
		t.Fatal(err)
	}
	h.waitIdle("the unit to stop", func() bool {
		u, _ = h.st.Q.GetUnit(ctx, u.ID)
		return u.Attention != "" && !h.p.Busy(ctx, u.ID)
	})
	if u.Attention != string(domain.AttentionModelUnavailable) || !strings.Contains(u.AttentionDetail, "Account sub1 reached its fable limit") || !strings.Contains(u.AttentionDetail, "Choose another model for the define run") {
		t.Fatalf("unit: %s: %s", u.Attention, u.AttentionDetail)
	}
	if !slices.Contains(AvailableActions(u, false), ActionRetry) {
		t.Errorf("no retry among %v", AvailableActions(u, false))
	}

	// Overrides are checked as at creation.
	if _, err := h.p.SetRunOverrides(ctx, u.ID, domain.RunOverrides{"define": {Account: "nobody"}}, "dev"); err == nil {
		t.Error("an override naming an unknown account was taken")
	}
	if _, err := h.p.SetRunOverrides(ctx, u.ID, domain.RunOverrides{"define": {Model: "opus", Effort: "medium"}}, "dev"); err != nil {
		t.Fatal(err)
	}
	if _, err := h.p.Act(ctx, u.ID, ActionRetry, ActionInput{}); err != nil {
		t.Fatal(err)
	}
	if u = h.waitState(u.ID, domain.StateDefinitionReview); u.Attention != "" {
		t.Errorf("unit flagged after the switch: %s: %s", u.Attention, u.AttentionDetail)
	}
	rs := h.runs(u.ID, "define")
	if len(rs) != 2 || rs[0].Model != "fable" || rs[1].Model != "opus" || rs[1].Effort != "medium" || rs[1].AccountID != "sub1" || rs[1].Status != "succeeded" {
		t.Fatalf("define runs: %+v", rs)
	}
	var args []string
	if err := json.Unmarshal([]byte(h.read(filepath.Join(h.control, "args-define.json"))), &args); err != nil {
		t.Fatal(err)
	}
	if flagValue(args, "--model") != "opus" {
		t.Errorf("the retried run asked for %q", flagValue(args, "--model"))
	}
	// The retry was on opus: it says nothing about Fable's limit.
	sub1, _ := h.st.Q.GetAccount(ctx, "sub1")
	if _, ok := toAccount(sub1).Blocked("fable", time.Now()); !ok {
		t.Error("retrying on opus lifted the block on fable")
	}
	acts, _ := h.st.Q.ListUnitActivity(ctx, db.ListUnitActivityParams{UnitID: store.NullString(u.ID), Lim: 200})
	if !slices.ContainsFunc(acts, func(a db.Activity) bool {
		return a.Actor == "dev" && strings.Contains(a.Message, "define opus, medium effort")
	}) {
		t.Errorf("the change is not in the unit's activity")
	}
}

// A unit waiting for an account may run at once on another model: some
// accounts are at their limit, and the others cannot run the stage's model.
func TestRunOverridesWakeAWaitingJob(t *testing.T) {
	h := newHarness(t)
	ctx := context.Background()
	h.addAccount("sub1", domain.AccountToken, domain.AccountSettings{}, "tok1")
	h.addAccount("sub2", domain.AccountToken, domain.AccountSettings{}, "tok2")
	pr := h.project()
	h.useAccounts(pr, domain.RunSettings{Accounts: []string{"sub1", "sub2"}, Stages: domain.RunOverrides{"define": {Model: "fable"}}})
	h.setControl("rate-limited-tok1", "")
	h.setControl("credits-required-tok2", "fable")
	u, err := h.p.CreateUnit(ctx, CreateUnitInput{ProjectID: pr.ID, Kind: "bugfix", Title: "Health lies when the DB is down"})
	if err != nil {
		t.Fatal(err)
	}
	h.waitIdle("the define job to wait for an account", func() bool {
		var waiting string
		_ = h.st.DB.QueryRow(`SELECT waiting_on FROM jobs WHERE unit_id = ? AND status = 'queued'`, u.ID).Scan(&waiting)
		return waiting == jobs.WaitAccount
	})
	if _, err := h.p.SetRunOverrides(ctx, u.ID, domain.RunOverrides{"define": {Model: "sonnet"}}, ""); err != nil {
		t.Fatal(err)
	}
	if u = h.waitState(u.ID, domain.StateDefinitionReview); u.Attention != "" {
		t.Errorf("the unit still waits: %s", u.AttentionDetail)
	}
	if rs := h.runs(u.ID, "define"); len(rs) != 3 || rs[2].AccountID != "sub2" || rs[2].Model != "sonnet" || rs[2].Status != "succeeded" {
		t.Errorf("define runs: %+v", rs)
	}
}

// When every account hits its usage limit mid-run, the job waits and later
// tries again in the last session that made progress instead of starting
// over. Subsequent immediate $0 rejections never displace that session.
func TestWaitAndWakeResumesLimitedSession(t *testing.T) {
	h := newHarness(t)
	ctx := context.Background()
	h.addAccount("sub1", domain.AccountToken, domain.AccountSettings{}, "tok1")
	h.addAccount("sub2", domain.AccountToken, domain.AccountSettings{}, "tok2")
	pr := h.project()
	h.useAccounts(pr, domain.RunSettings{Accounts: []string{"sub1", "sub2"}})
	h.setControl("rate-limited-tok1", "worked")
	h.setControl("rate-limited-tok2", "worked")

	u, err := h.p.CreateUnit(ctx, CreateUnitInput{ProjectID: pr.ID, Kind: "bugfix", Title: "Health lies when the DB is down"})
	if err != nil {
		t.Fatal(err)
	}
	waitQueued := func(wantRuns int) {
		t.Helper()
		h.waitIdle("the define job to wait for an account", func() bool {
			var status, waiting string
			_ = h.st.DB.QueryRow(`SELECT status, waiting_on FROM jobs WHERE unit_id = ? ORDER BY created_at DESC LIMIT 1`, u.ID).Scan(&status, &waiting)
			return status == jobs.StatusQueued && waiting == jobs.WaitAccount && len(h.runs(u.ID, "define")) == wantRuns
		})
	}
	waitQueued(2)

	// Next attempt: both accounts are turned away at $0 before doing any work.
	h.setControl("rate-limited-tok1", "")
	h.setControl("rate-limited-tok2", "")
	if err := h.p.ResumeAccount(ctx, "sub1"); err != nil {
		t.Fatal(err)
	}
	if err := h.p.ResumeAccount(ctx, "sub2"); err != nil {
		t.Fatal(err)
	}
	waitQueued(4)

	// Finally an account is free: it must pick up rs[1]'s session, skipping the
	// two $0 rejections rs[2] and rs[3].
	if err := h.p.ResumeAccount(ctx, "sub1"); err != nil {
		t.Fatal(err)
	}
	if u = h.waitState(u.ID, domain.StateDefinitionReview); u.Attention != "" {
		t.Errorf("the unit still waits: %s", u.AttentionDetail)
	}
	rs := h.runs(u.ID, "define")
	if len(rs) != 5 {
		t.Fatalf("define runs (%d): %+v", len(rs), rs)
	}
	if rs[1].ParentRunID != rs[0].ID {
		t.Errorf("failover run parent = %q, want %q", rs[1].ParentRunID, rs[0].ID)
	}
	for i := 2; i <= 4; i++ {
		if rs[i].ParentRunID != rs[1].ID {
			t.Errorf("run %d parent = %q, want %q (last run that did work)", i, rs[i].ParentRunID, rs[1].ID)
		}
	}
	var args []string
	if err := json.Unmarshal([]byte(h.read(filepath.Join(h.control, "args-define.json"))), &args); err != nil {
		t.Fatal(err)
	}
	if flagValue(args, "--resume") != rs[1].SessionID || !slices.Contains(args, "--fork-session") {
		t.Errorf("resumed run args = %v, want --resume %s --fork-session", args, rs[1].SessionID)
	}
	if p := h.read(filepath.Join(h.control, "prompt-define.txt")); !strings.Contains(p, "reached its usage limit") {
		t.Errorf("resumed run did not get continue prompt:\n%s", p)
	}
}

// Cancelling and retrying after a rate limit carries on the limited session,
// unless an input document changed in the meantime; revisions carry on only
// when the revision itself got work done before hitting the limit.
func TestRetryAndRevisionsResumeLimitedSessions(t *testing.T) {
	h := newHarness(t)
	ctx := context.Background()
	h.addAccount("sub1", domain.AccountToken, domain.AccountSettings{}, "tok1")
	pr := h.project()
	h.useAccounts(pr, domain.RunSettings{Accounts: []string{"sub1"}})
	h.setControl("rate-limited-tok1", "worked")

	u, err := h.p.CreateUnit(ctx, CreateUnitInput{ProjectID: pr.ID, Kind: "bugfix", Title: "Health lies when the DB is down"})
	if err != nil {
		t.Fatal(err)
	}
	waitForAccount := func(kind string, wantRuns int) {
		t.Helper()
		h.waitIdle("job waiting for account", func() bool {
			var status, waiting string
			_ = h.st.DB.QueryRow(`SELECT status, waiting_on FROM jobs WHERE unit_id = ? ORDER BY created_at DESC LIMIT 1`, u.ID).Scan(&status, &waiting)
			return status == jobs.StatusQueued && waiting == jobs.WaitAccount && len(h.runs(u.ID, kind)) == wantRuns
		})
	}
	waitForAccount("define", 1)

	// Cancel + manual Retry still picks up the limited define session.
	for _, action := range []string{ActionCancel, ActionRetry} {
		if _, err := h.p.Act(ctx, u.ID, action, ActionInput{}); err != nil {
			t.Fatalf("%s: %v", action, err)
		}
	}
	u = h.waitState(u.ID, domain.StateDefinitionReview)
	if rs := h.runs(u.ID, "define"); len(rs) != 2 || rs[1].ParentRunID != rs[0].ID {
		t.Fatalf("cancel+retry define runs: %+v", rs)
	}

	// Revision immediately turned away at $0 must re-send the revision prompt
	// on the succeeded define session, not continue.tmpl.
	h.setControl("rate-limited-tok1", "")
	if _, err := h.p.Act(ctx, u.ID, ActionIterate, ActionInput{Feedback: "Mention HTTP 503."}); err != nil {
		t.Fatal(err)
	}
	waitForAccount("define", 3)
	if err := h.p.ResumeAccount(ctx, "sub1"); err != nil {
		t.Fatal(err)
	}
	h.waitIdle("define revision after $0 limit", func() bool { return len(h.runs(u.ID, "define")) == 4 && !h.p.Busy(ctx, u.ID) })
	rs := h.runs(u.ID, "define")
	if rs[3].ParentRunID != rs[1].ID {
		t.Errorf("revision after $0 limit resumed %q, want succeeded run %q", rs[3].ParentRunID, rs[1].ID)
	}
	if p := h.read(filepath.Join(h.control, "prompt-define.txt")); !strings.Contains(p, "Mention HTTP 503.") {
		t.Errorf("revision after $0 limit lost revision feedback:\n%s", p)
	}

	// Revision that did work before hitting the limit resumes that revision run.
	h.setControl("rate-limited-tok1", "worked")
	if _, err := h.p.Act(ctx, u.ID, ActionIterate, ActionInput{Feedback: "Add timeout requirement."}); err != nil {
		t.Fatal(err)
	}
	waitForAccount("define", 5)
	if err := h.p.ResumeAccount(ctx, "sub1"); err != nil {
		t.Fatal(err)
	}
	h.waitIdle("define revision after worked limit", func() bool { return len(h.runs(u.ID, "define")) == 6 && !h.p.Busy(ctx, u.ID) })
	rs = h.runs(u.ID, "define")
	if rs[5].ParentRunID != rs[4].ID {
		t.Errorf("revision after worked limit resumed %q, want limited revision %q", rs[5].ParentRunID, rs[4].ID)
	}
	if p := h.read(filepath.Join(h.control, "prompt-define.txt")); !strings.Contains(p, "reached its usage limit") {
		t.Errorf("revision after worked limit did not get continue prompt:\n%s", p)
	}

	// If a person edits the requirement after cancelling a limited plan run,
	// retrying plan starts fresh so the edit is read.
	h.setControl("rate-limited-tok1", "worked")
	if _, err := h.p.Act(ctx, u.ID, ActionMarkReady, ActionInput{}); err != nil {
		t.Fatal(err)
	}
	waitForAccount("plan", 1)
	if _, err := h.p.Act(ctx, u.ID, ActionCancel, ActionInput{}); err != nil {
		t.Fatal(err)
	}
	if _, err := h.p.SaveDocument(ctx, u.ID, DocRequirement, "# Requirement\n\nEdited by a person.\n", "dev"); err != nil {
		t.Fatal(err)
	}
	if _, err := h.p.Act(ctx, u.ID, ActionRetry, ActionInput{}); err != nil {
		t.Fatal(err)
	}
	h.waitState(u.ID, domain.StateSpecReview)
	if prs := h.runs(u.ID, "plan"); len(prs) != 2 || prs[1].ParentRunID != "" {
		t.Errorf("plan after requirement edit must start fresh: %+v", prs)
	}
}

// Switching the model while waiting on a rate limit carries on the limited
// run's session on the new model; switching to another provider starts over.
func TestSwitchModelOrProviderWhileWaitingOnLimitedSession(t *testing.T) {
	h := newHarness(t)
	ctx := context.Background()
	h.addAccount("sub1", domain.AccountToken, domain.AccountSettings{}, "tok1")
	h.addAccount("sub2", domain.AccountToken, domain.AccountSettings{}, "tok2")
	h.addAccount("bedrock", domain.AccountBedrock, domain.AccountSettings{Region: "us-east-1", AWSProfile: "bedrock"}, "")
	pr := h.project()
	// Model switch within the same provider keeps the session.
	h.useAccounts(pr, domain.RunSettings{Accounts: []string{"sub1", "sub2"}, Stages: domain.RunOverrides{"define": {Model: "fable"}}})
	h.setControl("rate-limited-tok1", "worked")
	h.setControl("credits-required-tok2", "fable")

	u, err := h.p.CreateUnit(ctx, CreateUnitInput{ProjectID: pr.ID, Kind: "bugfix", Title: "Health lies when the DB is down"})
	if err != nil {
		t.Fatal(err)
	}
	h.waitIdle("waiting on limit", func() bool {
		var waiting string
		_ = h.st.DB.QueryRow(`SELECT waiting_on FROM jobs WHERE unit_id = ? AND status = 'queued'`, u.ID).Scan(&waiting)
		return waiting == jobs.WaitAccount
	})
	if _, err := h.p.SetRunOverrides(ctx, u.ID, domain.RunOverrides{"define": {Model: "sonnet"}}, "dev"); err != nil {
		t.Fatal(err)
	}
	h.waitState(u.ID, domain.StateDefinitionReview)
	rs := h.runs(u.ID, "define")
	if len(rs) != 3 || rs[2].AccountID != "sub2" || rs[2].Model != "sonnet" || rs[2].ParentRunID != rs[0].ID {
		t.Fatalf("model switch while waiting must resume rs[0]: %+v", rs)
	}

	// Switching to another provider while waiting starts over with the note.
	h.useAccounts(pr, domain.RunSettings{Accounts: []string{"bedrock", "sub2"}})
	h.p.pauseAccount(ctx, "sub2", time.Now().Add(time.Hour), "test")
	h.setControl("rate-limited", "worked")
	u2, err := h.p.CreateUnit(ctx, CreateUnitInput{ProjectID: pr.ID, Kind: "bugfix", Title: "Health lies on bedrock",
		RunOverrides: domain.RunOverrides{"define": {Account: "bedrock"}}})
	if err != nil {
		t.Fatal(err)
	}
	h.waitIdle("bedrock waiting on limit", func() bool {
		var waiting string
		_ = h.st.DB.QueryRow(`SELECT waiting_on FROM jobs WHERE unit_id = ? AND status = 'queued'`, u2.ID).Scan(&waiting)
		return waiting == jobs.WaitAccount
	})
	// Switch the unit to sub2 before unpausing it so waking runs on sub2.
	if _, err := h.p.SetRunOverrides(ctx, u2.ID, domain.RunOverrides{"define": {Account: "sub2"}}, "dev"); err != nil {
		t.Fatal(err)
	}
	if err := h.p.ResumeAccount(ctx, "sub2"); err != nil {
		t.Fatal(err)
	}
	h.waitState(u2.ID, domain.StateDefinitionReview)
	rs2 := h.runs(u2.ID, "define")
	if len(rs2) != 2 || rs2[1].AccountID != "sub2" || rs2[1].ParentRunID != "" {
		t.Fatalf("provider switch while waiting must start over: %+v", rs2)
	}
	if p := h.read(filepath.Join(h.control, "prompt-define.txt")); !strings.Contains(p, "An earlier attempt at this task") {
		t.Errorf("provider switch did not get start-over note:\n%s", p)
	}
}
