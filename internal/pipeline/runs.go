package pipeline

import (
	"context"
	"encoding/json"
	"fmt"
	"math"
	"os"
	"strings"
	"time"

	"github.com/google/uuid"

	"github.com/raulsh/thefactory/internal/claude"
	"github.com/raulsh/thefactory/internal/domain"
	"github.com/raulsh/thefactory/internal/events"
	"github.com/raulsh/thefactory/internal/jobs"
	"github.com/raulsh/thefactory/internal/store"
	"github.com/raulsh/thefactory/internal/store/db"
)

// runRequest describes one Claude run, for a unit or (triage) a project.
type runRequest struct {
	Unit db.Unit
	// ProjectID and Cwd are for runs without a unit.
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

// runClaude starts a run, streams its events into the store and the hub,
// and records the outcome. The returned run row reflects the final state.
func (p *Pipeline) runClaude(ctx context.Context, req runRequest) (db.Run, *claude.Outcome, error) {
	profile, ok := claude.Profiles[req.Kind]
	if !ok {
		return db.Run{}, nil, fmt.Errorf("no run profile %q", req.Kind)
	}
	stage := p.Config.Stage(req.Kind)
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
		cwd, projectID = req.Cwd, req.ProjectID
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
	spec.Env = claude.BuildEnv(claude.EnvOptions{GitConfigGlobal: p.Paths.GitConfig(), GHConfigDir: p.Paths.GHConfig()})
	if profile.Guarded {
		guard := fmt.Sprintf("%q hook-guard --stage %s", p.Paths.GuardBin(), req.Kind)
		spec.SettingsPath = runDir + "/settings.json"
		settings := claude.GuardSettings(guard, req.Trusted)
		if !p.Config.CommitAttribution {
			settings.Attribution = &claude.Attribution{}
		}
		if err := claude.WriteSettings(spec.SettingsPath, settings); err != nil {
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
		p.activity(bg, req.Unit.ID, "system", "run", fmt.Sprintf("%s run started (%s)", req.Kind, stage.Model), map[string]string{"run_id": runID})
	}
	p.changed("run", runID)
	topic := events.RunTopic(runID)

	out := p.Runner.Run(ctx, spec, claude.Callbacks{
		OnStart: func(pid int) {
			_ = p.Store.Q.StartRun(bg, db.StartRunParams{Pid: int64(pid), Now: store.NowNull(), ID: runID})
			p.Hub.PublishJSON(topic, "status", runID, map[string]string{"status": "running"})
			p.changed("run", runID)
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
		},
	})

	if rl := out.RateLimit; rl != nil {
		p.quota.Store(&Quota{Status: rl.Status, Windows: rl.UnifiedWindows, UpdatedAt: time.Now()})
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
		// Park the unit and hold Claude work until the window resets.
		until := time.Now().Add(15 * time.Minute)
		if rl := out.RateLimit; rl != nil && rl.ResetsAt > 0 {
			until = time.Unix(rl.ResetsAt, 0)
		}
		p.Jobs.PauseClaude(until)
		p.flag(ctx, u.ID, domain.AttentionWaiting, "rate limited until "+until.Local().Format("Jan 2 15:04"))
		return &jobs.RetryError{After: time.Until(until) + time.Minute, Err: fmt.Errorf("rate limited")}
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
