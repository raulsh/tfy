package pipeline

import (
	"cmp"
	"context"
	"encoding/json"
	"fmt"
	"math"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"time"

	"github.com/google/uuid"

	"github.com/raulsh/tfy/internal/claude"
	"github.com/raulsh/tfy/internal/domain"
	"github.com/raulsh/tfy/internal/events"
	"github.com/raulsh/tfy/internal/prompts"
	"github.com/raulsh/tfy/internal/store"
	"github.com/raulsh/tfy/internal/store/db"
)

// runRequest describes one Claude run, for a unit or (triage) a project.
type runRequest struct {
	Unit db.Unit
	// ProjectID is for runs without a unit. Cwd is where runs without a
	// unit start; a unit's run that sets it starts there instead of the
	// workspace.
	ProjectID     string
	Cwd           string
	Kind          string // profile and stage name
	Prompt        string
	PromptVersion string
	System        string
	Schema        json.RawMessage
	// Resume, when set, forks this earlier run's session.
	Resume *db.Run
	// Trusted checkouts (directory names under the workspace) for auto mode.
	Trusted []string
}

// runAgent runs req on the account its stage is assigned to. When that
// account reaches its usage limit, the account is paused and the run carries
// on with the next account that may run the stage: in the session the
// limited run left, if it got anywhere, or with the request again. When every
// account is limited, the outcome is rate_limited with the time the first one
// is free. The returned run row is the last run's, in its final state.
func (p *Pipeline) runAgent(ctx context.Context, req runRequest) (db.Run, *claude.Outcome, error) {
	as, err := p.assign(ctx, req)
	if err != nil {
		return db.Run{}, nil, err
	}
	orig := req
	// A session stays with the provider that started it: whether one can
	// move between providers is not known (docs/spike.md).
	provider := ""
	if req.Resume != nil {
		provider = p.runProvider(ctx, *req.Resume)
	}
	tried := map[string]bool{}
	run, out := db.Run{Kind: req.Kind}, (*claude.Outcome)(nil)
	for {
		// A request of its own can start over where the session cannot go.
		next, err := p.nextAccount(ctx, as.Pool, tried, provider, as.Model, orig.Resume == nil)
		if err != nil {
			return run, out, err
		}
		a := next.account
		if a == nil {
			// No account can run the model: a person chooses another.
			if next.until.IsZero() && len(next.blocked) > 0 {
				return run, &claude.Outcome{Status: claude.StatusFailed, Reason: noAccountForModel(as.Model, next.blocked)}, nil
			}
			if out == nil || out.Status != claude.StatusRateLimited {
				out = &claude.Outcome{Status: claude.StatusRateLimited, Reason: "every account for this run is at its usage limit"}
			}
			out.LimitedUntil = next.until
			if out.LimitedUntil.IsZero() {
				out.LimitedUntil = time.Now().Add(15 * time.Minute)
			}
			return run, out, nil
		}
		if next.moved {
			req, provider = orig, ""
			req.Prompt += "\nAn earlier attempt at this task, with another account, stopped part-way at a usage limit: the workspace may hold some of its work already. Look at what is there before you start, and build on it.\n"
		}
		if len(tried) > 0 && req.Unit.ID != "" {
			p.activity(ctx, req.Unit.ID, "system", "account", fmt.Sprintf("the %s run carries on with %s", req.Kind, a.Name), map[string]string{"account": a.ID})
		}
		// An account is free: the unit no longer waits for one.
		if req.Unit.Attention == string(domain.AttentionWaiting) {
			p.flag(ctx, req.Unit.ID, domain.AttentionNone, "")
			req.Unit.Attention, orig.Unit.Attention = "", ""
		}
		if run, out, err = p.runOnce(ctx, req, as, *a); err != nil || out.Status != claude.StatusRateLimited {
			return run, out, err
		}
		tried[a.ID] = true
		resets := limitResets(out)
		// A plan without credits for the model refuses that model alone.
		if rl := out.RateLimit; rl != nil && rl.NeedsCredits() {
			p.blockModel(ctx, a.ID, as.Model, resets)
			if req.Unit.ID != "" {
				p.activity(ctx, req.Unit.ID, "system", "account", fmt.Sprintf("%s cannot run %s without usage credits, until %s", a.Name, cmp.Or(as.Model, "its default model"), resets.Local().Format("Jan 2 15:04")), map[string]string{"account": a.ID, "run_id": run.ID})
			}
			continue
		}
		p.pauseAccount(ctx, a.ID, resets, fmt.Sprintf("the %s run reached the usage limit", req.Kind))
		if req.Unit.ID != "" {
			p.activity(ctx, req.Unit.ID, "system", "account", fmt.Sprintf("%s reached its usage limit, until %s", a.Name, resets.Local().Format("Jan 2 15:04")), map[string]string{"account": a.ID, "run_id": run.ID})
		}
		// Work done so far is in the run's session: carry it on, on an
		// account that can resume it.
		if r := out.Result; r != nil && r.TotalCostUSD > 0 && run.SessionID != "" && claude.Profiles[req.Kind].Persist {
			if req, err = continueRun(req, run); err != nil {
				return run, out, err
			}
			provider = claude.Provider(a)
		}
	}
}

// noAccountForModel says why no account can run model, and what to do.
func noAccountForModel(model string, blocked []domain.Account) string {
	names := make([]string, 0, len(blocked))
	for _, a := range blocked {
		until, _ := a.Blocked(model, time.Time{})
		names = append(names, fmt.Sprintf("%s (until %s)", a.Name, until.Local().Format("Jan 2 15:04")))
	}
	return fmt.Sprintf("none of the accounts for this run can run %s: it needs usage credits on %s. Choose another model for the stage, or add an account that has it",
		cmp.Or(model, "its default model"), strings.Join(names, ", "))
}

// continueRun asks the next account to carry on the session a limited run
// left.
func continueRun(req runRequest, limited db.Run) (runRequest, error) {
	prompt, version, err := prompts.Render("continue", struct{ Structured bool }{len(req.Schema) > 0})
	if err != nil {
		return req, err
	}
	req.Prompt, req.PromptVersion, req.Resume = prompt, version, &limited
	return req, nil
}

// nextPick is the account a run goes to next.
type nextPick struct {
	account *domain.Account
	// moved means the account cannot resume the session: the run starts
	// over there.
	moved bool
	// With no account: when the first limited one is free again, and those
	// that cannot run the model at all for now.
	until   time.Time
	blocked []domain.Account
}

// nextAccount picks the pool's first account that is not limited, can run
// model, and was not tried yet. With a session to carry on (from provider),
// it must be one that can resume it; failing that, startOver allows another.
func (p *Pipeline) nextAccount(ctx context.Context, pool []domain.Account, tried map[string]bool, provider, model string, startOver bool) (nextPick, error) {
	accounts, err := p.accounts(ctx)
	if err != nil {
		return nextPick{}, err
	}
	fresh := map[string]domain.Account{}
	for _, a := range accounts {
		fresh[a.ID] = a
	}
	// No account of the session's provider: try the session where it is
	// assigned rather than not at all.
	if !slices.ContainsFunc(pool, func(a domain.Account) bool { return claude.Provider(&a) == provider }) {
		provider = ""
	}
	now := time.Now()
	var next nextPick
	pick := func(match func(domain.Account) bool) *domain.Account {
		for _, a := range pool {
			a, ok := fresh[a.ID]
			if !ok || !match(a) {
				continue
			}
			if _, blocked := a.Blocked(model, now); blocked {
				if !slices.ContainsFunc(next.blocked, func(b domain.Account) bool { return b.ID == a.ID }) {
					next.blocked = append(next.blocked, a)
				}
				continue
			}
			if !a.Paused(now) {
				if !tried[a.ID] {
					return &a
				}
				continue
			}
			if next.until.IsZero() || a.PausedUntil.Before(next.until) {
				next.until = a.PausedUntil
			}
		}
		return nil
	}
	if next.account = pick(func(a domain.Account) bool { return provider == "" || claude.Provider(&a) == provider }); next.account != nil {
		return next, nil
	}
	if provider != "" && startOver {
		if next.account = pick(func(domain.Account) bool { return true }); next.account != nil {
			next.moved = true
			return next, nil
		}
	}
	return next, nil
}

// runProvider is the provider of the account a run was on, "" if unknown.
func (p *Pipeline) runProvider(ctx context.Context, run db.Run) string {
	row, err := p.Store.Q.GetAccount(ctx, run.AccountID)
	if err != nil {
		return ""
	}
	a := toAccount(row)
	return claude.Provider(&a)
}

// runOnce runs req on account a, streams its events into the store and the
// hub, and records the outcome. The returned run row reflects the final
// state.
func (p *Pipeline) runOnce(ctx context.Context, req runRequest, as domain.Assignment, a domain.Account) (db.Run, *claude.Outcome, error) {
	profile, ok := claude.Profiles[req.Kind]
	if !ok {
		return db.Run{}, nil, fmt.Errorf("no run profile %q", req.Kind)
	}
	stage := p.Config.Stage(req.Kind)
	stage.Model, stage.Effort = as.Model, as.Effort
	secret, err := p.readSecret(a.ID)
	if err != nil {
		return db.Run{}, nil, fmt.Errorf("read the secret of %s: %w", a.Name, err)
	}
	id, err := uuid.NewV7()
	if err != nil {
		return db.Run{}, nil, err
	}
	runID := id.String()
	runDir := p.Paths.RunDir(runID)
	if err := os.MkdirAll(runDir, 0o700); err != nil {
		return db.Run{}, nil, err
	}

	cwd, projectID := req.Unit.WorkspacePath, req.Unit.ProjectID
	if req.Unit.ID == "" {
		projectID = req.ProjectID
	}
	// A run of a unit may still start elsewhere: then it is not one of the
	// workspace's sessions, and the workspace is left alone.
	inWorkspace := req.Unit.ID != "" && req.Cwd == ""
	if req.Cwd != "" {
		cwd = req.Cwd
	}
	spec := &claude.Spec{
		Prompt:             req.Prompt,
		Cwd:                cwd,
		Model:              stage.Model,
		Effort:             stage.Effort,
		MaxBudgetUSD:       stage.Budget,
		Timeout:            stage.Timeout,
		AppendSystemPrompt: req.System,
		JSONSchema:         req.Schema,
	}
	parentID := ""
	if req.Resume != nil && req.Resume.SessionID != "" {
		spec.ResumeSession, spec.ForkSession = req.Resume.SessionID, true
		parentID = req.Resume.ID
	} else if profile.Persist {
		sid, _ := uuid.NewRandom()
		spec.SessionID = sid.String()
	}
	profile.Apply(spec)
	// The accounts' secrets are tfy's, not the run's.
	if slices.Contains(spec.Tools, "Read") {
		spec.DisallowedTools = append(spec.DisallowedTools, "Read(//"+strings.TrimPrefix(p.Paths.Accounts(), "/")+"/**)")
	}
	subagents := profile.Subagents && p.subagentsAllowed(ctx, req.Unit)
	if subagents {
		spec.Tools = append(spec.Tools, claude.AgentTool)
	}
	envOpts := claude.EnvOptions{
		GitConfigGlobal: p.Paths.GitConfig(), GHConfigDir: p.Paths.GHConfig(), ShellEnvFile: filepath.Join(runDir, "shell-env.sh"),
		Account: &a, Secret: secret,
	}
	if err := claude.WriteShellEnv(envOpts.ShellEnvFile); err != nil {
		return db.Run{}, nil, err
	}
	spec.GuardMarker = claude.GuardMarker
	// A unit's runs load conventions the way Claude Code does in a
	// repository: the workspace is made a Claude Code project first.
	var cp claudeProject
	if inWorkspace {
		urs, err := p.Store.Q.ListUnitRepos(ctx, req.Unit.ID)
		if err != nil {
			return db.Run{}, nil, err
		}
		if cp, err = p.prepareClaudeProject(ctx, req.Unit, urs, profile.Guarded); err != nil {
			return db.Run{}, nil, fmt.Errorf("prepare the workspace for Claude: %w", err)
		}
		if err := p.writeIssueFiles(ctx, req.Unit); err != nil {
			return db.Run{}, nil, fmt.Errorf("write the linked issues: %w", err)
		}
		spec.SettingSources = []string{"project"}
		// The checkouts serve the project's repositories as dependencies.
		envOpts.GitConfigGlobal = filepath.Join(runDir, "gitconfig")
		if envOpts.Extra, err = p.writeRunGitConfig(ctx, envOpts.GitConfigGlobal, urs); err != nil {
			return db.Run{}, nil, fmt.Errorf("write the run's git config: %w", err)
		}
	}
	spec.Env = claude.BuildEnv(envOpts)
	if profile.Guarded {
		spec.SettingsPath = runDir + "/settings.json"
		if err := claude.WriteSettings(spec.SettingsPath, p.runSettings(req, cp, profile)); err != nil {
			return db.Run{}, nil, err
		}
	}

	bg := context.WithoutCancel(ctx)
	run, err := p.Store.Q.CreateRun(bg, db.CreateRunParams{
		ID:             runID,
		UnitID:         store.NullString(req.Unit.ID),
		ProjectID:      store.NullString(projectID),
		ParentRunID:    parentID,
		Kind:           req.Kind,
		AccountID:      a.ID,
		Engine:         a.CLI,
		Model:          stage.Model,
		Effort:         stage.Effort,
		PermissionMode: spec.PermissionMode,
		PromptVersion:  req.PromptVersion,
		Cwd:            spec.Cwd,
		Now:            store.Now(),
	})
	if err != nil {
		return db.Run{}, nil, err
	}
	if spec.SessionID != "" {
		_ = p.Store.Q.SetRunSession(bg, db.SetRunSessionParams{SessionID: spec.SessionID, ID: runID})
	}
	if req.Unit.ID != "" {
		p.activity(bg, req.Unit.ID, "system", "run", fmt.Sprintf("%s run started on %s (%s)", req.Kind, a.Name, cmp.Or(stage.Model, "its default model")), map[string]string{"run_id": runID, "account": a.ID})
		for _, note := range cp.Notes {
			p.activity(bg, req.Unit.ID, "system", "conventions", note, map[string]string{"run_id": runID})
		}
	}
	p.changed("run", runID)
	topic := events.RunTopic(runID)

	out := p.Runner.Run(ctx, spec, claude.Callbacks{
		OnStart: func(pid int) {
			_ = p.Store.Q.StartRun(bg, db.StartRunParams{Pid: int64(pid), Now: store.NowNull(), ID: runID})
			p.Hub.PublishJSON(topic, "status", runID, map[string]string{"status": "running"})
			p.changed("run", runID)
			p.board.start(AgentRun{
				RunID: runID, Kind: req.Kind, UnitID: req.Unit.ID, UnitLabel: unitLabel(req.Unit), UnitTitle: req.Unit.Title,
				ProjectID: projectID, StartedAt: time.Now(), Subagents: subagents,
			}, spec.Cwd)
			p.boardChanged()
		},
		OnEvent: func(e claude.Event) {
			at := store.Now()
			tool := e.ToolName()
			summary := e.Summary()
			if err := p.Store.Q.InsertRunEvent(bg, db.InsertRunEventParams{
				RunID: runID, Seq: int64(e.Seq), At: at, Type: e.Type, Subtype: e.Subtype,
				Tool: tool, Summary: summary, Payload: string(e.Raw),
			}); err != nil {
				p.Log.Error("store run event", "run", runID, "seq", e.Seq, "error", err)
			}
			if init, ok := e.Init(); ok && init.SessionID != "" {
				_ = p.Store.Q.SetRunSession(bg, db.SetRunSessionParams{SessionID: init.SessionID, ID: runID})
			}
			data, _ := json.Marshal(eventView{Seq: int64(e.Seq), At: at, Type: e.Type, Subtype: e.Subtype, Tool: tool, Summary: summary, Payload: e.Raw})
			p.Hub.Publish(topic, events.Message{Kind: "event", ID: runID, Seq: int64(e.Seq), Data: data})
			if p.board.observe(runID, e, at) {
				p.boardChanged()
			}
		},
	})
	p.board.finish(runID)
	p.boardChanged()

	if rl := out.RateLimit; rl != nil && !rl.NeedsCredits() {
		p.saveQuota(ctx, a.ID, rl)
	}
	// The reported cost and tokens are cumulative over a resume chain.
	var total, parentTotal float64
	var in, outTok int64
	sessionID := spec.SessionID
	if out.Init != nil && out.Init.SessionID != "" {
		sessionID = out.Init.SessionID
	}
	result := ""
	turns := 0
	if r := out.Result; r != nil {
		total = r.TotalCostUSD
		in, outTok = r.Usage.InputTokens+r.Usage.CacheReadInputTokens+r.Usage.CacheCreationInputTokens, r.Usage.OutputTokens
		turns = r.NumTurns
		if r.SessionID != "" {
			sessionID = r.SessionID
		}
		if raw, ok := r.Structured(); ok {
			result = string(raw)
		} else {
			result = r.Result
		}
	}
	if req.Resume != nil {
		parentTotal = req.Resume.CostTotalUsd
	}
	if err := p.Store.Q.FinishRun(bg, db.FinishRunParams{
		Status:       string(out.Status),
		Reason:       out.Reason,
		Now:          store.NowNull(),
		SessionID:    sessionID,
		CostUsd:      math.Max(total-parentTotal, 0),
		CostTotalUsd: total,
		InputTokens:  in,
		OutputTokens: outTok,
		Turns:        int64(turns),
		Denials:      int64(out.Denials),
		Result:       result,
		ID:           runID,
	}); err != nil {
		p.Log.Error("finish run", "run", runID, "error", err)
	}
	p.Hub.PublishJSON(topic, "status", runID, map[string]string{"status": string(out.Status), "reason": out.Reason})
	p.changed("run", runID)
	msg := fmt.Sprintf("%s run %s", req.Kind, out.Status)
	if out.Reason != "" && out.Status != claude.StatusSucceeded {
		msg += ": " + out.Reason
	}
	if req.Unit.ID != "" {
		p.activity(bg, req.Unit.ID, "system", "run", msg, map[string]any{"run_id": runID, "cost_usd": math.Max(total-parentTotal, 0)})
	}

	final, err := p.Store.Q.GetRun(bg, runID)
	if err != nil {
		return run, out, err
	}
	return final, out, nil
}

// eventView is a run event as the API and the live stream present it.
type eventView struct {
	Seq     int64           `json:"seq"`
	At      time.Time       `json:"at"`
	Type    string          `json:"type"`
	Subtype string          `json:"subtype,omitempty"`
	Tool    string          `json:"tool,omitempty"`
	Summary string          `json:"summary,omitempty"`
	Payload json.RawMessage `json:"payload"`
}

// runFailed records why a run did not succeed on the unit and returns the
// error the job should end with.
func (p *Pipeline) runFailed(ctx context.Context, u db.Unit, run db.Run, out *claude.Outcome) error {
	reason := out.Reason
	switch out.Status {
	case claude.StatusRateLimited:
		return p.waitOnLimit(ctx, u, out)
	case claude.StatusBudgetExceeded:
		p.flag(ctx, u.ID, domain.AttentionBudgetExceeded, fmt.Sprintf("the %s run reached its $%.2f budget", run.Kind, p.Config.Stage(run.Kind).Budget))
	case claude.StatusCancelled:
		p.flag(ctx, u.ID, domain.AttentionInterrupted, "cancelled")
	default:
		if reason == "" {
			reason = string(out.Status)
		}
		p.flag(ctx, u.ID, domain.AttentionFailed, fmt.Sprintf("the %s run %s: %s", run.Kind, strings.ReplaceAll(string(out.Status), "_", " "), reason))
	}
	return &stopError{fmt.Errorf("%s run %s: %s", run.Kind, out.Status, reason)}
}
