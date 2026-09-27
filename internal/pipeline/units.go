package pipeline

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"slices"
	"strings"

	"github.com/google/uuid"

	"github.com/raulsh/tfy/internal/domain"
	"github.com/raulsh/tfy/internal/store"
	"github.com/raulsh/tfy/internal/store/db"
)

func newID() string {
	id, err := uuid.NewV7()
	if err != nil {
		return uuid.NewString()
	}
	return id.String()
}

// CreateUnitInput is a developer's request for a unit of work.
type CreateUnitInput struct {
	ProjectID   string `json:"project_id"`
	Kind        string `json:"kind"`
	Title       string `json:"title"`
	Description string `json:"description"`
	CreatedBy   string `json:"created_by"`
	// Issue, when set, is the GitHub issue the unit is for (a link,
	// owner/repo#12 or #12). Title and kind then default to the issue's.
	Issue string `json:"issue"`
}

// CreateUnit registers a unit and starts defining it. Developer units skip
// the intake stage.
func (p *Pipeline) CreateUnit(ctx context.Context, in CreateUnitInput) (db.Unit, error) {
	if strings.TrimSpace(in.Issue) != "" {
		return p.createUnitFromIssue(ctx, in)
	}
	kind, err := domain.ParseKind(in.Kind)
	if err != nil {
		return db.Unit{}, &InvalidError{Msg: err.Error()}
	}
	u, err := p.newUnit(ctx, unitSpec{
		ProjectID: in.ProjectID, Kind: kind, Title: in.Title, Description: in.Description,
		Origin: domain.OriginDeveloper, State: domain.StateDefining, CreatedBy: in.CreatedBy,
	})
	if err != nil {
		return u, err
	}
	return u, p.enqueue(ctx, JobDefine, u, definePayload{})
}

// unitSpec is everything needed to register a unit.
type unitSpec struct {
	ProjectID    string
	Kind         domain.Kind
	Title        string
	Summary      string
	Description  string
	Origin       domain.Origin
	State        domain.State
	CreatedBy    string
	ParentUnitID string
}

// newUnit inserts a unit and prepares its workspace.
func (p *Pipeline) newUnit(ctx context.Context, s unitSpec) (db.Unit, error) {
	title := strings.TrimSpace(s.Title)
	if title == "" {
		return db.Unit{}, &InvalidError{Msg: "a title is required"}
	}
	if _, err := p.Store.Q.GetProject(ctx, s.ProjectID); err != nil {
		if store.IsNotFound(err) {
			return db.Unit{}, &NotFoundError{What: "project"}
		}
		return db.Unit{}, err
	}
	var u db.Unit
	err := p.Store.Tx(ctx, func(q *db.Queries) error {
		seq, err := q.NextUnitSeq(ctx)
		if err != nil {
			return err
		}
		u, err = q.CreateUnit(ctx, db.CreateUnitParams{
			ID: newID(), Seq: seq, ProjectID: s.ProjectID, ParentUnitID: store.NullString(s.ParentUnitID), Kind: string(s.Kind), Title: title,
			Summary: strings.TrimSpace(s.Summary), Description: strings.TrimSpace(s.Description), Origin: string(s.Origin),
			State: string(s.State), WorkspacePath: p.workspacePath(seq, title), CreatedBy: s.CreatedBy, Now: store.Now(),
		})
		return err
	})
	if err != nil {
		return u, err
	}
	if err := os.MkdirAll(filepath.Join(u.WorkspacePath, "docs"), 0o755); err != nil {
		return u, err
	}
	p.activity(ctx, u.ID, actorOr(s.CreatedBy), "created", fmt.Sprintf("%s created: %s", domain.Label(u.Seq), u.Title), nil)
	p.changed("unit", u.ID)
	return u, nil
}

func actorOr(s string) string {
	if s == "" {
		return "user"
	}
	return s
}

// Actions a person can take on a unit.
const (
	ActionAccept      = "accept"
	ActionReject      = "reject"
	ActionMarkReady   = "mark-ready"
	ActionApproveSpec = "approve-spec"
	ActionIterate     = "iterate"
	ActionBack        = "back"
	ActionRetry       = "retry"
	ActionCancel      = "cancel"
	ActionRefresh     = "refresh"
	ActionMerge       = "merge"
	ActionRereview    = "rereview"
	ActionOverride    = "override-approve"
	ActionReviseSpec  = "revise-spec"
	ActionReopen      = "reopen"
	ActionAcknowledge = "acknowledge"
	ActionMarkRelease = "mark-released"
	ActionFollowUp    = "follow-up"
	// ActionLearn runs a finished unit's retrospective (again).
	ActionLearn = "suggest-conventions"
)

// ActionInput carries an action's parameters.
type ActionInput struct {
	Feedback string `json:"feedback"`
	Actor    string `json:"actor"`
	// ClosePRs, on reject, closes the unit's pull requests and deletes
	// their branches.
	ClosePRs bool `json:"close_prs"`
}

// AvailableActions lists what a person can do with a unit now.
func AvailableActions(u db.Unit, busy bool) []string {
	s := domain.State(u.State)
	out := []string{} // never null in JSON
	if busy {
		out = append(out, ActionCancel)
	} else if _, working := jobForState[s]; working {
		out = append(out, ActionRetry)
	}
	attention := domain.Attention(u.Attention)
	switch s {
	case domain.StateProposed:
		out = append(out, ActionAccept)
	case domain.StateDefinitionReview:
		out = append(out, ActionMarkReady, ActionIterate)
	case domain.StateSpecReview:
		out = append(out, ActionApproveSpec, ActionIterate, ActionBack)
	case domain.StateReviewing:
		if attention == domain.AttentionReviewBlocked && !busy {
			out = append(out, ActionIterate, ActionOverride, ActionReviseSpec)
		}
		out = append(out, ActionRefresh)
	case domain.StateAwaitingMerge:
		if !busy {
			switch attention {
			case domain.AttentionHeadChanged, domain.AttentionConflict, domain.AttentionPRClosed:
			default:
				out = append(out, ActionMerge)
			}
			out = append(out, ActionRereview, ActionReviseSpec)
		}
		out = append(out, ActionRefresh)
	case domain.StateMerging:
		out = append(out, ActionRefresh)
	case domain.StateReleasing:
		// Also while it waits on CI: marking released stops the wait.
		out = append(out, ActionMarkRelease)
		if !busy && attention == domain.AttentionCIFailed {
			out = append(out, ActionFollowUp)
		}
	case domain.StateRejected:
		if !busy {
			out = append(out, ActionReopen)
		}
	case domain.StateDone:
		if !busy && u.Origin != string(domain.OriginRetrospective) {
			out = append(out, ActionLearn)
		}
	}
	if attention == domain.AttentionNewFeedback {
		out = append(out, ActionAcknowledge)
	}
	if !s.Terminal() {
		out = append(out, ActionReject)
	}
	return out
}

// Busy reports whether the unit has a job queued or running.
func (p *Pipeline) Busy(ctx context.Context, unitID string) bool {
	_, err := p.Store.Q.ActiveJobForUnit(ctx, store.NullString(unitID))
	return err == nil
}

// Act applies a person's action to a unit.
func (p *Pipeline) Act(ctx context.Context, unitID, action string, in ActionInput) (db.Unit, error) {
	u, err := p.Store.Q.GetUnit(ctx, unitID)
	if err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return u, &NotFoundError{What: "unit"}
		}
		return u, err
	}
	busy := p.Busy(ctx, u.ID)
	if !slices.Contains(AvailableActions(u, busy), action) {
		return u, &ConflictError{Msg: fmt.Sprintf("%q is not possible while the unit is %s", action, strings.ReplaceAll(u.State, "_", " "))}
	}
	actor := actorOr(in.Actor)
	state := domain.State(u.State)

	switch action {
	case ActionMarkReady:
		if u, err = p.transition(ctx, u, domain.StatePlanning, actor, "requirement marked ready"); err != nil {
			return u, err
		}
		if err := p.enqueue(ctx, JobPlan, u, planPayload{}); err != nil {
			return u, err
		}
		// The agreed requirement is what an issue most often lacks.
		p.checkIssuesWhenReady(ctx, u)
		return u, nil

	case ActionApproveSpec:
		// An approved spec starts its merge plan over.
		if err := p.Store.Q.SetUnitMergeStep(ctx, db.SetUnitMergeStepParams{MergeStep: 0, Now: store.Now(), ID: u.ID}); err != nil {
			return u, err
		}
		u.MergeStep = 0
		if u, err = p.transition(ctx, u, domain.StateDeveloping, actor, "specification approved"); err != nil {
			return u, err
		}
		return u, p.enqueue(ctx, JobDevelop, u, developPayload{})

	case ActionIterate:
		feedback := strings.TrimSpace(in.Feedback)
		if state == domain.StateReviewing {
			meta, _ := p.latestReview(ctx, u)
			if feedback != "" {
				meta.Findings = append(meta.Findings, Finding{Severity: "major", Repo: "all", Message: feedback})
				p.activity(ctx, u.ID, actor, "feedback", feedback, nil)
			}
			return u, p.sendBack(ctx, u, meta, actor)
		}
		if feedback == "" {
			return u, &InvalidError{Msg: "say what should change"}
		}
		switch state {
		case domain.StateDefinitionReview:
			if u, err = p.transition(ctx, u, domain.StateDefining, actor, "revision requested"); err != nil {
				return u, err
			}
			p.activity(ctx, u.ID, actor, "feedback", feedback, nil)
			return u, p.enqueue(ctx, JobDefine, u, definePayload{Revision: feedback})
		case domain.StateSpecReview:
			if u, err = p.transition(ctx, u, domain.StatePlanning, actor, "revision requested"); err != nil {
				return u, err
			}
			p.activity(ctx, u.ID, actor, "feedback", feedback, nil)
			return u, p.enqueue(ctx, JobPlan, u, planPayload{Revision: feedback})
		}

	case ActionOverride:
		return p.transition(ctx, u, domain.StateAwaitingMerge, actor, "review overridden")

	case ActionMerge:
		if u, err = p.transition(ctx, u, domain.StateMerging, actor, "merge requested"); err != nil {
			return u, err
		}
		return u, p.enqueue(ctx, JobMerge, u, nil)

	case ActionRereview:
		if u, err = p.transition(ctx, u, domain.StateReviewing, actor, "review requested"); err != nil {
			return u, err
		}
		return u, p.enqueue(ctx, JobReview, u, nil)

	case ActionReviseSpec:
		return p.transition(ctx, u, domain.StateSpecReview, actor, "back to the spec; the pull requests stay open")

	case ActionReopen:
		return p.reopen(ctx, u, actor)

	case ActionMarkRelease:
		p.Jobs.CancelUnit(ctx, u.ID)
		// Before the last step of a merge plan, this goes on to the next
		// step without waiting any longer.
		if steps, _, err := p.unitSteps(ctx, u); err == nil && int(u.MergeStep) < len(steps)-1 {
			p.flag(ctx, u.ID, domain.AttentionNone, "")
			p.activity(ctx, u.ID, actor, "step", fmt.Sprintf("went on to merge step %d without waiting", u.MergeStep+2), nil)
			if err := p.advanceStep(ctx, db.Job{}, u, steps); err != nil {
				return u, err
			}
			return p.Store.Q.GetUnit(ctx, u.ID)
		}
		if u, err = p.transition(ctx, u, domain.StateDone, actor, "marked released"); err != nil {
			return u, err
		}
		p.afterDone(ctx, u)
		return u, nil

	case ActionLearn:
		if _, err := os.Stat(u.WorkspacePath); err != nil {
			return u, &ConflictError{Msg: "the unit's workspace is gone, so there is nothing to look back at"}
		}
		p.activity(ctx, u.ID, actor, "retrospective", "retrospective requested", nil)
		return u, p.enqueue(ctx, JobLearn, u, nil)

	case ActionFollowUp:
		return p.followUp(ctx, u, actor)

	case ActionAcknowledge:
		p.flag(ctx, u.ID, domain.AttentionNone, "")
		return p.Store.Q.GetUnit(ctx, u.ID)

	case ActionBack:
		return p.transition(ctx, u, domain.StateDefinitionReview, actor, "back to the requirement")

	case ActionRetry:
		kind := jobForState[state]
		p.flag(ctx, u.ID, domain.AttentionNone, "")
		p.activity(ctx, u.ID, actor, "retry", "retrying "+kind, nil)
		return u, p.enqueue(ctx, kind, u, retryPayload(kind))

	case ActionCancel:
		if !p.Jobs.CancelUnit(ctx, u.ID) {
			// Only queued work: it is dropped already.
			p.flag(ctx, u.ID, domain.AttentionInterrupted, "cancelled")
		}
		p.activity(ctx, u.ID, actor, "cancel", "cancelled the running work", nil)
		return p.Store.Q.GetUnit(ctx, u.ID)

	case ActionRefresh:
		return u, p.pollUnitPRs(ctx, u)

	case ActionReject:
		p.Jobs.CancelUnit(ctx, u.ID)
		why := strings.TrimSpace(in.Feedback)
		if u, err = p.transition(ctx, u, domain.StateRejected, actor, why); err != nil {
			return u, err
		}
		return u, p.enqueue(ctx, JobCleanup, u, cleanupPayload{ClosePRs: in.ClosePRs})

	case ActionAccept:
		if u, err = p.transition(ctx, u, domain.StateDefining, actor, "proposal accepted"); err != nil {
			return u, err
		}
		return u, p.enqueue(ctx, JobDefine, u, definePayload{})
	}
	return u, &ConflictError{Msg: "unsupported action " + action}
}

// reopen brings a rejected unit back to the last gate its documents
// reached, rebuilding the workspace documents from the stored versions.
func (p *Pipeline) reopen(ctx context.Context, u db.Unit, actor string) (db.Unit, error) {
	if err := os.MkdirAll(filepath.Join(u.WorkspacePath, "docs"), 0o755); err != nil {
		return u, err
	}
	to := domain.StateProposed
	for _, kind := range []string{DocRequirement, DocSpec} {
		doc, err := p.Store.Q.LatestDocument(ctx, db.LatestDocumentParams{UnitID: u.ID, Kind: kind})
		if err != nil {
			continue
		}
		if err := writeDoc(u, kind, doc.Content); err != nil {
			return u, err
		}
		if kind == DocRequirement {
			to = domain.StateDefinitionReview
		} else {
			to = domain.StateSpecReview
		}
	}
	return p.transition(ctx, u, to, actor, "reopened")
}

func retryPayload(kind string) any {
	switch kind {
	case JobDevelop:
		return developPayload{Retry: true}
	case JobDefine:
		return definePayload{}
	case JobPlan:
		return planPayload{}
	}
	return struct{}{}
}

// SaveDocument stores a person's edit of a unit document.
func (p *Pipeline) SaveDocument(ctx context.Context, unitID, kind, content, actor string) (db.Document, error) {
	u, err := p.Store.Q.GetUnit(ctx, unitID)
	if err != nil {
		if store.IsNotFound(err) {
			return db.Document{}, &NotFoundError{What: "unit"}
		}
		return db.Document{}, err
	}
	if _, ok := docFiles[kind]; !ok {
		return db.Document{}, &InvalidError{Msg: "only the requirement and the spec can be edited"}
	}
	if p.Busy(ctx, u.ID) {
		return db.Document{}, &ConflictError{Msg: "wait for the current run to finish before editing"}
	}
	if strings.TrimSpace(content) == "" {
		return db.Document{}, &InvalidError{Msg: "the document cannot be empty"}
	}
	meta := "{}"
	if latest, err := p.Store.Q.LatestDocument(ctx, db.LatestDocumentParams{UnitID: u.ID, Kind: kind}); err == nil {
		meta = latest.Meta
	}
	if err := writeDoc(u, kind, content); err != nil {
		return db.Document{}, err
	}
	doc, err := p.saveDoc(ctx, u, kind, content, meta, actorOr(actor), "")
	if err == nil {
		p.changed("unit", u.ID)
	}
	return doc, err
}
