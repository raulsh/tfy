package pipeline

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/raulsh/thefactory/internal/domain"
	"github.com/raulsh/thefactory/internal/gh"
	"github.com/raulsh/thefactory/internal/jobs"
	"github.com/raulsh/thefactory/internal/store"
	"github.com/raulsh/thefactory/internal/store/db"
)

// merge merges every pull request of the unit, but only after checking all
// of them: none may have changed since the review, none may conflict. A
// failure part-way leaves the merged ones merged; retrying merges the rest.
func (p *Pipeline) merge(ctx context.Context, job db.Job, u db.Unit) error {
	project, err := p.Store.Q.GetProject(ctx, u.ProjectID)
	if err != nil {
		return err
	}
	settings := domain.ParseProjectSettings(project.Settings)
	urs, err := p.Store.Q.ListUnitRepos(ctx, u.ID)
	if err != nil {
		return err
	}

	type pending struct {
		ur db.ListUnitReposRow
		pr *gh.PR
	}
	var todo []pending
	for _, ur := range targetsOf(urs) {
		if ur.PrNumber == 0 {
			continue
		}
		pr, err := p.GH.PRView(ctx, ur.FullName, int(ur.PrNumber))
		if err != nil {
			return err
		}
		switch pr.State {
		case "MERGED":
			continue
		case "CLOSED":
			return p.mergeBlocked(ctx, u, domain.AttentionPRClosed, fmt.Sprintf("%s #%d was closed without merging", ur.FullName, pr.Number))
		}
		if ur.ReviewedSha != "" && pr.HeadRefOid != "" && pr.HeadRefOid != ur.ReviewedSha {
			return p.mergeBlocked(ctx, u, domain.AttentionHeadChanged, fmt.Sprintf("%s #%d has commits the review did not see; review it again", ur.FullName, pr.Number))
		}
		if pr.ChecksState() == "failure" {
			return p.mergeBlocked(ctx, u, domain.AttentionCIFailed, fmt.Sprintf("%s #%d has failing checks; fix them or merge on GitHub", ur.FullName, pr.Number))
		}
		switch pr.Mergeable {
		case "CONFLICTING":
			return p.mergeBlocked(ctx, u, domain.AttentionConflict, fmt.Sprintf("%s #%d conflicts with %s", ur.FullName, pr.Number, ur.DefaultBranch))
		case "UNKNOWN", "":
			// GitHub computes mergeability lazily; ask again shortly.
			if job.Attempts < 6 {
				return &jobs.RetryError{After: 10 * time.Second, Err: fmt.Errorf("%s #%d: GitHub is still computing mergeability", ur.FullName, pr.Number)}
			}
		}
		todo = append(todo, pending{ur, pr})
	}

	var merged []string
	for _, t := range todo {
		if t.pr.IsDraft {
			if err := p.GH.PRReady(ctx, t.ur.FullName, t.pr.Number); err != nil {
				return p.partial(ctx, u, merged, fmt.Errorf("mark %s #%d ready: %w", t.ur.FullName, t.pr.Number, err))
			}
		}
		head := t.ur.ReviewedSha
		if head == "" {
			head = t.pr.HeadRefOid
		}
		if err := p.GH.PRMerge(ctx, t.ur.FullName, t.pr.Number, settings.MergeMethod, head, settings.DeleteBranch); err != nil {
			return p.partial(ctx, u, merged, fmt.Errorf("merge %s #%d: %w", t.ur.FullName, t.pr.Number, err))
		}
		merged = append(merged, fmt.Sprintf("%s #%d", t.ur.FullName, t.pr.Number))
		p.activity(ctx, u.ID, "system", "pr", fmt.Sprintf("merged %s #%d (%s)", t.ur.FullName, t.pr.Number, settings.MergeMethod), map[string]string{"url": t.pr.URL})
	}
	// Record the merged state and finish the unit.
	return p.pollUnitPRs(ctx, u)
}

// mergeBlocked returns the unit to awaiting merge with the reason.
func (p *Pipeline) mergeBlocked(ctx context.Context, u db.Unit, a domain.Attention, detail string) error {
	if u.State == string(domain.StateMerging) {
		var err error
		if u, err = p.transition(ctx, u, domain.StateAwaitingMerge, "system", "merge stopped"); err != nil {
			return err
		}
	}
	p.flag(ctx, u.ID, a, detail)
	return &stopError{errors.New(detail)}
}

func (p *Pipeline) partial(ctx context.Context, u db.Unit, merged []string, err error) error {
	if len(merged) == 0 {
		return p.mergeBlocked(ctx, u, domain.AttentionFailed, err.Error())
	}
	p.flag(ctx, u.ID, domain.AttentionPartiallyMerged, fmt.Sprintf("merged %s; then %v. Retry to merge the rest.", strings.Join(merged, ", "), err))
	return &stopError{err}
}

// cleanupPayload says what to undo when a unit is rejected.
type cleanupPayload struct {
	ClosePRs bool `json:"close_prs"`
}

// cleanup undoes a rejected unit's footprint: optionally its pull requests
// and branches, and always its workspace and Claude session state.
func (p *Pipeline) cleanup(ctx context.Context, job db.Job, u db.Unit) error {
	payload := decodePayload[cleanupPayload](job)
	urs, err := p.Store.Q.ListUnitRepos(ctx, u.ID)
	if err != nil {
		return err
	}
	var errs []error
	if payload.ClosePRs {
		for _, ur := range urs {
			if ur.PrNumber == 0 || ur.PrState == "merged" || ur.PrState == "closed" {
				continue
			}
			if err := p.GH.PRClose(ctx, ur.FullName, int(ur.PrNumber), true); err != nil {
				errs = append(errs, err)
				continue
			}
			_ = p.Store.Q.SetUnitRepoPRStatus(ctx, db.SetUnitRepoPRStatusParams{
				PrState: "closed", ChecksState: ur.ChecksState, HeadSha: ur.HeadSha, MergeSha: ur.MergeSha,
				MergedAt: ur.MergedAt, Now: store.Now(), UnitID: u.ID, RepoID: ur.RepoID,
			})
			p.activity(ctx, u.ID, "system", "pr", fmt.Sprintf("closed %s #%d", ur.FullName, ur.PrNumber), nil)
		}
	}
	p.purgeWorkspace(ctx, u)
	// The feedback behind a rejected unit goes back to the inbox.
	if err := p.Store.Q.UnlinkFeedbackFromUnit(ctx, db.UnlinkFeedbackFromUnitParams{Now: store.Now(), UnitID: store.NullString(u.ID)}); err != nil {
		errs = append(errs, err)
	}
	p.changed("unit", u.ID)
	return errors.Join(errs...)
}

// purgeWorkspace deletes the unit's checkouts and documents on disk (the
// database keeps every document version) and Claude's state for it.
func (p *Pipeline) purgeWorkspace(ctx context.Context, u db.Unit) {
	if u.WorkspacePath == "" || !strings.HasPrefix(u.WorkspacePath, p.Paths.Workspaces()+"/") {
		return
	}
	if err := p.Runner.Purge(ctx, u.WorkspacePath); err != nil {
		p.Log.Warn("purge claude project state", "unit", domain.Label(u.Seq), "error", err)
	}
	if err := removeAll(u.WorkspacePath); err != nil {
		p.Log.Warn("remove workspace", "unit", domain.Label(u.Seq), "error", err)
		return
	}
	p.activity(ctx, u.ID, "system", "cleanup", "workspace removed", nil)
}
