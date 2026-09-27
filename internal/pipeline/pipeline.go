// Package pipeline moves units through the factory: it owns the stage
// handlers, the Claude runs they start, and the git and GitHub work around
// them.
package pipeline

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"sync"
	"sync/atomic"
	"time"

	"github.com/raulsh/tfy/internal/claude"
	"github.com/raulsh/tfy/internal/config"
	"github.com/raulsh/tfy/internal/domain"
	"github.com/raulsh/tfy/internal/events"
	"github.com/raulsh/tfy/internal/gh"
	"github.com/raulsh/tfy/internal/git"
	"github.com/raulsh/tfy/internal/jobs"
	"github.com/raulsh/tfy/internal/slack"
	"github.com/raulsh/tfy/internal/store"
	"github.com/raulsh/tfy/internal/store/db"
)

// Job kinds.
const (
	JobDefine  = "define"
	JobPlan    = "plan"
	JobDevelop = "develop"
	JobPublish = "publish"
	JobReview  = "review"
	JobMerge   = "merge"
	JobCleanup = "cleanup"
	JobTriage  = "triage"
	JobRelease = "release"
	JobLearn   = "learn"
)

// jobForState is the job that does the work of a working state; it is what
// "retry" re-runs.
var jobForState = map[domain.State]string{
	domain.StateDefining:   JobDefine,
	domain.StatePlanning:   JobPlan,
	domain.StateDeveloping: JobDevelop,
	domain.StatePublishing: JobPublish,
	domain.StateReviewing:  JobReview,
	domain.StateMerging:    JobMerge,
	domain.StateReleasing:  JobRelease,
}

// Deps are the pipeline's collaborators.
type Deps struct {
	Store  *store.Store
	Config config.Config
	Paths  config.Paths
	Runner *claude.Runner
	Git    *git.Git
	GH     *gh.Client
	// Slack is nil when slk is not available: intake is then off.
	Slack *slack.Client
	Hub   *events.Hub
	Jobs  *jobs.Queue
	Log   *slog.Logger
}

// Pipeline is the factory's engine.
type Pipeline struct {
	Deps
	repoLocks sync.Map // full name → *sync.Mutex
	quota     atomic.Pointer[Quota]
}

// Quota is the latest account utilization the CLI reported.
type Quota struct {
	Status    string                            `json:"status"`
	Windows   map[string]claude.RateLimitWindow `json:"windows"`
	UpdatedAt time.Time                         `json:"updated_at"`
}

// Quota returns the latest reported utilization, or nil before any run.
func (p *Pipeline) Quota() *Quota { return p.quota.Load() }

// New creates the pipeline and registers its job kinds with the queue.
func New(d Deps) *Pipeline {
	p := &Pipeline{Deps: d}
	d.Jobs.Register(JobDefine, jobs.Kind{Handler: p.unitJob(p.define), Claude: true})
	d.Jobs.Register(JobPlan, jobs.Kind{Handler: p.unitJob(p.plan), Claude: true})
	d.Jobs.Register(JobDevelop, jobs.Kind{Handler: p.unitJob(p.develop), Claude: true, SideEffects: true})
	d.Jobs.Register(JobPublish, jobs.Kind{Handler: p.unitJob(p.publish), SideEffects: true})
	d.Jobs.Register(JobReview, jobs.Kind{Handler: p.unitJob(p.review), Claude: true})
	d.Jobs.Register(JobMerge, jobs.Kind{Handler: p.unitJob(p.merge), SideEffects: true})
	d.Jobs.Register(JobCleanup, jobs.Kind{Handler: p.unitJob(p.cleanup), SideEffects: true})
	d.Jobs.Register(JobTriage, jobs.Kind{Handler: p.triage, Claude: true})
	d.Jobs.Register(JobRelease, jobs.Kind{Handler: p.unitJob(p.release), Claude: true})
	d.Jobs.Register(JobLearn, jobs.Kind{Handler: p.unitJob(p.learn), Claude: true})
	d.Jobs.OnInterrupted = func(ctx context.Context, j db.Job) {
		if j.UnitID.Valid {
			p.flag(ctx, j.UnitID.String, domain.AttentionInterrupted,
				fmt.Sprintf("tfy stopped during %s; check the workspace, then retry", j.Kind))
		}
	}
	d.Jobs.OnChange = func(j db.Job) {
		if j.UnitID.Valid {
			p.changed("unit", j.UnitID.String)
		}
	}
	return p
}

// unitHandler does a job's work for a unit.
type unitHandler func(ctx context.Context, job db.Job, u db.Unit) error

// stopError means the handler already recorded why the unit stopped, so the
// wrapper must not overwrite the attention it set.
type stopError struct{ err error }

func (e *stopError) Error() string { return e.err.Error() }
func (e *stopError) Unwrap() error { return e.err }

// unitJob loads the unit, runs h, and flags the unit when h fails.
func (p *Pipeline) unitJob(h unitHandler) jobs.Handler {
	return func(ctx context.Context, job db.Job) error {
		if !job.UnitID.Valid {
			return errors.New("job has no unit")
		}
		u, err := p.Store.Q.GetUnit(ctx, job.UnitID.String)
		if err != nil {
			return err
		}
		err = h(ctx, job, u)
		var stop *stopError
		var retry *jobs.RetryError
		switch {
		case err == nil, errors.As(err, &stop), errors.As(err, &retry):
		case ctx.Err() != nil:
			// Cancelled by the user (shutdown never reaches here with a
			// cancelled context unless it was the user).
			p.flag(ctx, u.ID, domain.AttentionInterrupted, "cancelled")
		default:
			p.flag(ctx, u.ID, domain.AttentionFailed, err.Error())
		}
		return err
	}
}

// flag sets a unit's attention and records it.
func (p *Pipeline) flag(ctx context.Context, unitID string, a domain.Attention, detail string) {
	ctx = context.WithoutCancel(ctx)
	if err := p.Store.Q.SetUnitAttention(ctx, db.SetUnitAttentionParams{Attention: string(a), AttentionDetail: detail, Now: store.Now(), ID: unitID}); err != nil {
		p.Log.Error("flag unit", "unit", unitID, "error", err)
		return
	}
	if a != domain.AttentionNone {
		p.activity(ctx, unitID, "system", "attention", fmt.Sprintf("%s: %s", a, detail), nil)
	}
	p.changed("unit", unitID)
}

// transition moves a unit, refusing illegal or stale moves.
func (p *Pipeline) transition(ctx context.Context, u db.Unit, to domain.State, actor, why string) (db.Unit, error) {
	from := domain.State(u.State)
	if !domain.CanTransition(from, to) {
		return u, &ConflictError{Msg: (&domain.TransitionError{From: from, To: to}).Error()}
	}
	ctx = context.WithoutCancel(ctx)
	n, err := p.Store.Q.TransitionUnit(ctx, db.TransitionUnitParams{ToState: string(to), Now: store.Now(), ID: u.ID, FromState: string(from)})
	if err != nil {
		return u, err
	}
	if n == 0 {
		return u, &ConflictError{Msg: "the unit changed in the meantime; reload and try again"}
	}
	msg := fmt.Sprintf("%s → %s", from.Label(), to.Label())
	if why != "" {
		msg += ": " + why
	}
	p.activity(ctx, u.ID, actor, "transition", msg, map[string]string{"from": string(from), "to": string(to)})
	p.changed("unit", u.ID)
	return p.Store.Q.GetUnit(ctx, u.ID)
}

func (p *Pipeline) activity(ctx context.Context, unitID, actor, kind, message string, data any) {
	raw := []byte("{}")
	if data != nil {
		raw, _ = json.Marshal(data)
	}
	u, err := p.Store.Q.GetUnit(ctx, unitID)
	projectID := ""
	if err == nil {
		projectID = u.ProjectID
	}
	if err := p.Store.Q.AddActivity(context.WithoutCancel(ctx), db.AddActivityParams{
		UnitID: store.NullString(unitID), ProjectID: store.NullString(projectID), At: store.Now(),
		Actor: actor, Kind: kind, Message: message, Data: string(raw),
	}); err != nil {
		p.Log.Error("record activity", "unit", unitID, "error", err)
	}
}

// changed tells UI subscribers an entity changed.
func (p *Pipeline) changed(kind, id string) {
	p.Hub.Publish(events.TopicGlobal, events.Message{Kind: kind, ID: id})
}

func (p *Pipeline) enqueue(ctx context.Context, kind string, u db.Unit, payload any) error {
	_, err := p.Jobs.Enqueue(ctx, jobs.EnqueueOpts{
		Kind:      kind,
		UnitID:    u.ID,
		ProjectID: u.ProjectID,
		Payload:   payload,
		DedupeKey: "unit:" + u.ID,
	})
	if errors.Is(err, jobs.ErrDuplicate) {
		return &ConflictError{Msg: "this unit already has work queued or running"}
	}
	return err
}

// ConflictError is a request that is valid but not possible now.
type ConflictError struct{ Msg string }

func (e *ConflictError) Error() string { return e.Msg }

// NotFoundError is a request for something that does not exist.
type NotFoundError struct{ What string }

func (e *NotFoundError) Error() string { return e.What + " not found" }

// InvalidError is a malformed request.
type InvalidError struct{ Msg string }

func (e *InvalidError) Error() string { return e.Msg }
