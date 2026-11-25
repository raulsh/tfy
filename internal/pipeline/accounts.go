package pipeline

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"time"

	"github.com/raulsh/tfy/internal/claude"
	"github.com/raulsh/tfy/internal/config"
	"github.com/raulsh/tfy/internal/domain"
	"github.com/raulsh/tfy/internal/jobs"
	"github.com/raulsh/tfy/internal/store"
	"github.com/raulsh/tfy/internal/store/db"
)

// toAccount converts a stored account.
func toAccount(a db.Account) domain.Account {
	out := domain.Account{ID: a.ID, Name: a.Name, CLI: a.Cli, Kind: a.Kind, Settings: domain.ParseAccountSettings(a.Settings)}
	if a.PausedUntil.Valid {
		out.PausedUntil = a.PausedUntil.Time
	}
	out.ModelBlocks = domain.ParseModelBlocks(a.ModelBlocks, time.Now())
	return out
}

// blockModel keeps an account from running model, and the rest of its
// family, leaving it free for other models: its plan has no usage credits
// for it, or its own usage window is used up.
func (p *Pipeline) blockModel(ctx context.Context, id, model string, block domain.ModelBlock) {
	block.Until = block.Until.UTC()
	p.editModelBlocks(ctx, id, func(blocks map[string]domain.ModelBlock) bool {
		family := domain.ModelFamily(model)
		if cur, ok := blocks[family]; ok && !block.Until.After(cur.Until) {
			return false
		}
		blocks[family] = block
		return true
	})
}

// unblockModel lets an account try model again.
func (p *Pipeline) unblockModel(ctx context.Context, id, model string) {
	p.editModelBlocks(ctx, id, func(blocks map[string]domain.ModelBlock) bool {
		n := len(blocks)
		delete(blocks, domain.ModelFamily(model))
		delete(blocks, model)
		return len(blocks) < n
	})
}

// editModelBlocks changes an account's model blocks with edit, which
// reports whether it changed any. Blocks that ended are dropped.
func (p *Pipeline) editModelBlocks(ctx context.Context, id string, edit func(map[string]domain.ModelBlock) bool) {
	ctx = context.WithoutCancel(ctx)
	row, err := p.Store.Q.GetAccount(ctx, id)
	if err != nil {
		return
	}
	blocks := domain.ParseModelBlocks(row.ModelBlocks, time.Now())
	if !edit(blocks) {
		return
	}
	b, _ := json.Marshal(blocks)
	if err := p.Store.Q.SetAccountModelBlocks(ctx, db.SetAccountModelBlocksParams{ModelBlocks: string(b), ID: id}); err != nil {
		p.Log.Error("change the model blocks of an account", "account", id, "error", err)
	}
	p.changed("account", id)
}

// accounts returns every account.
func (p *Pipeline) accounts(ctx context.Context) ([]domain.Account, error) {
	rows, err := p.Store.Q.ListAccounts(ctx)
	if err != nil {
		return nil, err
	}
	out := make([]domain.Account, 0, len(rows))
	for _, r := range rows {
		out = append(out, toAccount(r))
	}
	return out, nil
}

// HasSecret reports whether an account's secret is set.
func (p *Pipeline) HasSecret(id string) bool {
	fi, err := os.Stat(p.Paths.AccountSecret(id))
	return err == nil && fi.Size() > 0
}

func (p *Pipeline) readSecret(id string) (string, error) {
	return config.ReadAccountSecret(p.Paths, id)
}

// GlobalRunSettings are the run settings of projects that have none.
func (p *Pipeline) GlobalRunSettings(ctx context.Context) domain.RunSettings {
	raw, err := p.Store.Q.GetAppSetting(ctx, "runs")
	if err != nil {
		return domain.RunSettings{Accounts: []string{}, Stages: domain.RunOverrides{}}
	}
	return domain.ParseRunSettings(raw)
}

// SetGlobalRunSettings replaces the global run settings.
func (p *Pipeline) SetGlobalRunSettings(ctx context.Context, s domain.RunSettings) (domain.RunSettings, error) {
	s, err := p.checkRunSettings(ctx, s, nil)
	if err != nil {
		return s, err
	}
	if len(s.Accounts) == 0 {
		return s, &InvalidError{Msg: "the global settings need at least one account"}
	}
	if err := p.Store.Q.PutAppSetting(ctx, db.PutAppSettingParams{Key: "runs", Value: s.JSON(), Now: store.Now()}); err != nil {
		return s, err
	}
	p.accountsChanged(ctx, "")
	return s, nil
}

// checkRunSettings normalizes run settings and checks them against the
// accounts: each one exists, and each stage's account, model and effort are
// ones its CLI can run. fallback is the account list the stages choose from
// when the settings have none (the global one, for a project).
func (p *Pipeline) checkRunSettings(ctx context.Context, s domain.RunSettings, fallback []string) (domain.RunSettings, error) {
	s, err := s.Normalize()
	if err != nil {
		return s, &InvalidError{Msg: err.Error()}
	}
	accounts, err := p.accounts(ctx)
	if err != nil {
		return s, err
	}
	byID := map[string]domain.Account{}
	for _, a := range accounts {
		byID[a.ID] = a
	}
	for _, id := range s.Accounts {
		if _, ok := byID[id]; !ok {
			return s, &InvalidError{Msg: fmt.Sprintf("there is no account %q", id)}
		}
	}
	listed := s.Accounts
	if len(listed) == 0 {
		listed = fallback
	}
	for kind, o := range s.Stages {
		if o.Account == "" {
			continue
		}
		a, ok := byID[o.Account]
		switch {
		case !ok:
			return s, &InvalidError{Msg: fmt.Sprintf("%s: there is no account %q", kind, o.Account)}
		case !slices.Contains(listed, o.Account):
			return s, &InvalidError{Msg: fmt.Sprintf("%s: %s is not one of the accounts listed", kind, a.Name)}
		case !slices.Contains(domain.CLIs[a.CLI].Stages, kind):
			return s, &InvalidError{Msg: fmt.Sprintf("%s: %s cannot run %s", kind, a.Name, kind)}
		case o.Model != "" && !domain.ModelFits(a.CLI, o.Model):
			return s, &InvalidError{Msg: fmt.Sprintf("%s: %s cannot run model %q", kind, a.Name, o.Model)}
		case o.Effort != "" && !slices.Contains(domain.CLIs[a.CLI].Efforts, o.Effort):
			return s, &InvalidError{Msg: fmt.Sprintf("%s: %s has no effort level %q", kind, a.Name, o.Effort)}
		}
	}
	return s, nil
}

// AccountInput creates or changes an account. A nil Secret keeps the
// current one.
type AccountInput struct {
	ID       string                 `json:"id"`
	Name     string                 `json:"name"`
	CLI      string                 `json:"cli"`
	Kind     string                 `json:"kind"`
	Settings domain.AccountSettings `json:"settings"`
	Secret   *string                `json:"secret,omitempty"`
}

// CreateAccount adds an account. It is not used until a list of accounts,
// global or a project's, names it.
func (p *Pipeline) CreateAccount(ctx context.Context, in AccountInput) (db.Account, error) {
	a := domain.Account{ID: strings.TrimSpace(in.ID), Name: strings.TrimSpace(in.Name), CLI: in.CLI, Kind: in.Kind, Settings: in.Settings.Normalize()}
	hasSecret := in.Secret != nil && strings.TrimSpace(*in.Secret) != ""
	if err := a.Validate(hasSecret); err != nil {
		return db.Account{}, &InvalidError{Msg: err.Error()}
	}
	if _, err := p.Store.Q.GetAccount(ctx, a.ID); err == nil {
		return db.Account{}, &ConflictError{Msg: fmt.Sprintf("there is an account %q already", a.ID)}
	}
	if hasSecret {
		if err := config.WriteAccountSecret(p.Paths, a.ID, *in.Secret); err != nil {
			return db.Account{}, err
		}
	}
	row, err := p.Store.Q.CreateAccount(ctx, db.CreateAccountParams{ID: a.ID, Name: a.Name, Cli: a.CLI, Kind: a.Kind, Settings: a.Settings.JSON(), Now: store.Now()})
	if err != nil {
		return row, err
	}
	p.accountsChanged(ctx, a.ID)
	return row, nil
}

// UpdateAccount changes an account's name, settings and, when given, its
// secret. Its CLI and kind stay.
func (p *Pipeline) UpdateAccount(ctx context.Context, id string, in AccountInput) (db.Account, error) {
	cur, err := p.Store.Q.GetAccount(ctx, id)
	if err != nil {
		if store.IsNotFound(err) {
			return db.Account{}, &NotFoundError{What: "account"}
		}
		return db.Account{}, err
	}
	a := toAccount(cur)
	a.Name, a.Settings = strings.TrimSpace(in.Name), in.Settings.Normalize()
	hasSecret := p.HasSecret(id)
	if in.Secret != nil {
		hasSecret = strings.TrimSpace(*in.Secret) != ""
	}
	if err := a.Validate(hasSecret); err != nil {
		return db.Account{}, &InvalidError{Msg: err.Error()}
	}
	if in.Secret != nil {
		if err := p.SetAccountSecret(ctx, id, *in.Secret); err != nil {
			return db.Account{}, err
		}
	}
	row, err := p.Store.Q.UpdateAccount(ctx, db.UpdateAccountParams{Name: a.Name, Settings: a.Settings.JSON(), Now: store.Now(), ID: id})
	if err != nil {
		return row, err
	}
	p.accountsChanged(ctx, id)
	return row, nil
}

// SetAccountSecret replaces an account's secret; an empty one removes it.
func (p *Pipeline) SetAccountSecret(ctx context.Context, id, secret string) error {
	if _, err := p.Store.Q.GetAccount(ctx, id); err != nil {
		if store.IsNotFound(err) {
			return &NotFoundError{What: "account"}
		}
		return err
	}
	if err := config.WriteAccountSecret(p.Paths, id, secret); err != nil {
		return err
	}
	p.accountsChanged(ctx, id)
	return nil
}

// DeleteAccount removes an account no settings use any more, and its secret.
func (p *Pipeline) DeleteAccount(ctx context.Context, id string) error {
	if _, err := p.Store.Q.GetAccount(ctx, id); err != nil {
		if store.IsNotFound(err) {
			return &NotFoundError{What: "account"}
		}
		return err
	}
	var users []string
	if p.GlobalRunSettings(ctx).Uses(id) {
		users = append(users, "the global settings")
	}
	projects, err := p.Store.Q.ListProjects(ctx)
	if err != nil {
		return err
	}
	for _, pr := range projects {
		if domain.ParseProjectSettings(pr.Settings).Runs.Uses(id) {
			users = append(users, pr.Name)
		}
	}
	if len(users) > 0 {
		return &ConflictError{Msg: "the account is in use by " + strings.Join(users, ", ")}
	}
	if err := p.Store.Q.DeleteAccount(ctx, id); err != nil {
		return err
	}
	if err := os.Remove(p.Paths.AccountSecret(id)); err != nil && !errors.Is(err, os.ErrNotExist) {
		return err
	}
	p.accountsChanged(ctx, id)
	return nil
}

// ResumeAccount lifts an account's usage-limit pause, for a person who knows
// better than the reset time; work waiting for an account runs now.
func (p *Pipeline) ResumeAccount(ctx context.Context, id string) error {
	if _, err := p.Store.Q.GetAccount(ctx, id); err != nil {
		if store.IsNotFound(err) {
			return &NotFoundError{What: "account"}
		}
		return err
	}
	if err := p.Store.Q.ResumeAccounts(ctx, []string{id}); err != nil {
		return err
	}
	p.accountsChanged(ctx, id)
	return nil
}

// accountsChanged tells the UI, and wakes the work waiting for an account:
// the change may have freed one.
func (p *Pipeline) accountsChanged(ctx context.Context, id string) {
	if err := p.Jobs.WakeWaiting(context.WithoutCancel(ctx), jobs.WaitAccount); err != nil {
		p.Log.Error("wake jobs waiting for an account", "error", err)
	}
	p.changed("account", id)
}

// assign resolves the account a run starts on, its model and effort, and
// the accounts it may fail over to.
func (p *Pipeline) assign(ctx context.Context, req runRequest) (domain.Assignment, error) {
	accounts, err := p.accounts(ctx)
	if err != nil {
		return domain.Assignment{}, err
	}
	projectID := req.Unit.ProjectID
	if req.Unit.ID == "" {
		projectID = req.ProjectID
	}
	var project domain.RunSettings
	if projectID != "" {
		if pr, err := p.Store.Q.GetProject(ctx, projectID); err == nil {
			project = domain.ParseProjectSettings(pr.Settings).Runs
		}
	}
	stage := p.Config.Stage(req.Kind)
	as, err := domain.Resolve(req.Kind, accounts, p.GlobalRunSettings(ctx), project,
		domain.ParseRunOverrides(req.Unit.RunOverrides), domain.RunOverride{Model: stage.Model, Effort: stage.Effort})
	if err != nil {
		return as, &InvalidError{Msg: err.Error()}
	}
	return as, nil
}

// checkOverrides checks a unit's overrides against the accounts its project
// may use.
func (p *Pipeline) checkOverrides(ctx context.Context, projectID string, o domain.RunOverrides) error {
	listed := p.GlobalRunSettings(ctx).Accounts
	if pr, err := p.Store.Q.GetProject(ctx, projectID); err == nil {
		if own := domain.ParseProjectSettings(pr.Settings).Runs.Accounts; len(own) > 0 {
			listed = own
		}
	}
	_, err := p.checkRunSettings(ctx, domain.RunSettings{Accounts: listed, Stages: o}, nil)
	return err
}

// AccountTest is what checking an account found.
type AccountTest struct {
	claude.AuthStatus
	Error string `json:"error,omitempty"`
	// Probe is a one-word prompt's outcome, when one was asked for.
	Probe *AccountProbe `json:"probe,omitempty"`
}

// AccountProbe is the outcome of a one-word prompt on an account.
type AccountProbe struct {
	Status  string  `json:"status"`
	Model   string  `json:"model,omitempty"`
	Reply   string  `json:"reply,omitempty"`
	Reason  string  `json:"reason,omitempty"`
	CostUSD float64 `json:"cost_usd"`
}

// TestAccount reports how an account signs in, for free. With probe, it also
// sends a one-word prompt with no tools on haiku, which costs a fraction of
// a cent and proves the account answers.
func (p *Pipeline) TestAccount(ctx context.Context, id string, probe bool) (AccountTest, error) {
	row, err := p.Store.Q.GetAccount(ctx, id)
	if err != nil {
		if store.IsNotFound(err) {
			return AccountTest{}, &NotFoundError{What: "account"}
		}
		return AccountTest{}, err
	}
	a := toAccount(row)
	secret, err := p.readSecret(id)
	if err != nil {
		return AccountTest{}, err
	}
	env := claude.BuildEnv(claude.EnvOptions{Account: &a, Secret: secret})
	var t AccountTest
	if t.AuthStatus, err = p.Runner.AuthStatus(ctx, env); err != nil {
		t.Error = err.Error()
	}
	if !probe {
		return t, nil
	}
	dir, err := p.probeDir()
	if err != nil {
		return t, err
	}
	spec := &claude.Spec{
		Prompt: "Reply with the single word ok.", Cwd: dir, Env: env, Model: "haiku", Effort: "low",
		MaxBudgetUSD: 0.05, Timeout: 2 * time.Minute, Tools: []string{}, PermissionMode: claude.ModeDontAsk,
		NoSessionPersistence: true,
	}
	out := p.Runner.Run(ctx, spec, claude.Callbacks{})
	t.Probe = &AccountProbe{Status: string(out.Status), Reason: out.Reason}
	if out.Init != nil {
		t.Probe.Model = out.Init.Model
	}
	if r := out.Result; r != nil {
		t.Probe.Reply, t.Probe.CostUSD = clip(strings.TrimSpace(r.Result), 200), r.TotalCostUSD
	}
	if rl := out.RateLimit; rl != nil {
		p.saveQuota(ctx, id, rl)
	}
	if out.Status == claude.StatusRateLimited {
		p.pauseAccount(ctx, id, limitResets(out), "a test reached the usage limit")
	}
	return t, nil
}

// pauseAccount holds an account back until its usage limit resets.
func (p *Pipeline) pauseAccount(ctx context.Context, id string, until time.Time, reason string) {
	err := p.Store.Q.PauseAccount(context.WithoutCancel(ctx), db.PauseAccountParams{Until: store.NullTime(until.UTC()), Reason: reason, ID: id})
	if err != nil {
		p.Log.Error("pause account", "account", id, "error", err)
	}
	p.changed("account", id)
}

// limitResets is when a rate-limited run's account can be used again. A
// subscription reports the reset of its usage window; a provider without
// windows only answered 429, which passes in moments.
func limitResets(out *claude.Outcome) time.Time {
	if rl := out.RateLimit; rl != nil && rl.Limited() {
		if rl.ResetsAt > 0 {
			return time.Unix(rl.ResetsAt, 0)
		}
		return time.Now().Add(15 * time.Minute)
	}
	return time.Now().Add(2 * time.Minute)
}

// saveQuota keeps an account's latest usage report.
func (p *Pipeline) saveQuota(ctx context.Context, id string, rl *claude.RateLimit) {
	b, _ := json.Marshal(Quota{Status: rl.Status, Windows: rl.UnifiedWindows, UpdatedAt: time.Now()})
	if err := p.Store.Q.SetAccountQuota(context.WithoutCancel(ctx), db.SetAccountQuotaParams{Quota: string(b), Now: store.NowNull(), ID: id}); err != nil {
		p.Log.Error("save account quota", "account", id, "error", err)
	}
	p.changed("account", id)
}

// Quota is an account's latest utilization, as its CLI reported it.
type Quota struct {
	Status    string                            `json:"status"`
	Windows   map[string]claude.RateLimitWindow `json:"windows"`
	UpdatedAt time.Time                         `json:"updated_at"`
}

// resumeStageAccounts lifts the usage-limit pauses of the accounts a unit's
// run of kind could use, and their blocks on the model it runs: a person
// retrying it knows better than the reset time. Blocks on other models
// stay, so a retry on another model does not try them again. Accounts still
// limited pause again on the first try.
func (p *Pipeline) resumeStageAccounts(ctx context.Context, u db.Unit, kind string) {
	if !slices.Contains(domain.RunKinds, kind) {
		return
	}
	as, err := p.assign(ctx, runRequest{Unit: u, Kind: kind})
	if err != nil {
		return
	}
	ids := make([]string, 0, len(as.Pool))
	for _, a := range as.Pool {
		ids = append(ids, a.ID)
		p.unblockModel(ctx, a.ID, as.Model)
	}
	if err := p.Store.Q.LiftAccountPauses(ctx, ids); err != nil {
		p.Log.Error("resume accounts", "error", err)
	}
	p.accountsChanged(ctx, "")
}

// waitOnLimit holds a job back while every account its run could use is at
// its usage limit: until the first one resets, or until an account frees up
// sooner (a person resumes one, or the settings change).
func (p *Pipeline) waitOnLimit(ctx context.Context, u db.Unit, out *claude.Outcome) error {
	until := out.LimitedUntil
	if until.IsZero() {
		until = limitResets(out)
	}
	if u.ID != "" {
		p.flag(ctx, u.ID, domain.AttentionWaiting, "every account for this run is at its usage limit, until "+until.Local().Format("Jan 2 15:04"))
	}
	return &jobs.RetryError{After: time.Until(until) + time.Minute, Wait: jobs.WaitAccount, Err: errors.New("rate limited")}
}

// probeDir is where account tests run: an empty directory.
func (p *Pipeline) probeDir() (string, error) {
	dir := filepath.Join(p.Paths.Root, "probe")
	return dir, os.MkdirAll(dir, 0o755)
}
