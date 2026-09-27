package pipeline

// Some changes cannot merge all at once. A shared module must be merged
// before the repositories that use it can pin its merged commit; a service
// must be deployed before the clients that need it. The spec then carries a
// merge plan: ordered steps, each merged together, each waiting for the
// steps before it to be merged, released or tagged, and each optionally
// needing an update first — such as moving a pin to the commit just merged.
//
// The whole change is still developed and reviewed at once. Then each step
// is merged by a person; tfy waits for what the next step needs, runs a
// development round for its update and a review of just that update, and
// offers the next step for merging.

import (
	"context"
	"encoding/json"
	"fmt"
	"slices"
	"strings"
	"time"

	"github.com/raulsh/tfy/internal/domain"
	"github.com/raulsh/tfy/internal/jobs"
	"github.com/raulsh/tfy/internal/store"
	"github.com/raulsh/tfy/internal/store/db"
)

// What a merge step waits for, of the steps before it.
const (
	WaitMerged   = "merged"   // their pull requests are merged
	WaitReleased = "released" // CI is green on their merge commits
	WaitTagged   = "tagged"   // a tag contains each of their merge commits
)

// MergeStep is one step of a spec's merge plan.
type MergeStep struct {
	// Repos are merged together, as directory names or owner/name.
	Repos   []string `json:"repos"`
	WaitFor string   `json:"wait_for,omitempty"`
	// Update is a change to make in this step's repositories once the steps
	// before it are merged (and released or tagged, as WaitFor says), before
	// this step merges.
	Update string `json:"update,omitempty"`
}

// step is a merge step resolved against the unit's target repositories.
type step struct {
	Index   int
	Repos   []db.ListUnitReposRow
	WaitFor string
	Update  string
}

// resolveSteps turns a merge plan into steps over the unit's targets. With
// no plan there is one step with every target. Unknown names are ignored, a
// repository named twice stays in its first step, and targets the plan
// forgets join the last step, so nothing is left unmerged.
func resolveSteps(plan []MergeStep, targets []db.ListUnitReposRow) []step {
	placed := map[string]bool{}
	var steps []step
	for _, ms := range plan {
		st := step{WaitFor: normalizeWait(ms.WaitFor), Update: strings.TrimSpace(ms.Update)}
		for _, name := range ms.Repos {
			name = strings.TrimSuffix(strings.TrimSpace(name), "/")
			for _, ur := range targets {
				if !placed[ur.RepoID] && (strings.EqualFold(name, dirOf(ur)) || strings.EqualFold(name, ur.FullName)) {
					placed[ur.RepoID] = true
					st.Repos = append(st.Repos, ur)
				}
			}
		}
		if len(st.Repos) > 0 {
			st.Index = len(steps)
			steps = append(steps, st)
		}
	}
	var rest []db.ListUnitReposRow
	for _, ur := range targets {
		if !placed[ur.RepoID] {
			rest = append(rest, ur)
		}
	}
	switch {
	case len(steps) == 0:
		return []step{{Repos: targets, WaitFor: WaitMerged}}
	case len(rest) > 0:
		steps[len(steps)-1].Repos = append(steps[len(steps)-1].Repos, rest...)
	}
	steps[0].WaitFor = WaitMerged // nothing comes before the first step
	return steps
}

func normalizeWait(w string) string {
	switch w = strings.ToLower(strings.TrimSpace(w)); w {
	case WaitReleased, WaitTagged:
		return w
	}
	return WaitMerged
}

// unitSteps are the unit's merge steps, from its latest spec.
func (p *Pipeline) unitSteps(ctx context.Context, u db.Unit) ([]step, []db.ListUnitReposRow, error) {
	urs, err := p.Store.Q.ListUnitRepos(ctx, u.ID)
	if err != nil {
		return nil, nil, err
	}
	var meta SpecMeta
	if doc, err := p.Store.Q.LatestDocument(ctx, db.LatestDocumentParams{UnitID: u.ID, Kind: DocSpec}); err == nil {
		_ = json.Unmarshal([]byte(doc.Meta), &meta)
	}
	return resolveSteps(meta.MergePlan, targetsOf(urs)), urs, nil
}

// currentStep is the step the unit is merging or preparing.
func currentStep(u db.Unit, steps []step) step {
	return steps[min(max(int(u.MergeStep), 0), len(steps)-1)]
}

func merged(ur db.ListUnitReposRow) bool { return ur.PrState == "merged" }

// openRepos are a step's repositories whose pull requests are not merged.
func openRepos(st step) []db.ListUnitReposRow {
	var out []db.ListUnitReposRow
	for _, ur := range st.Repos {
		if !merged(ur) {
			out = append(out, ur)
		}
	}
	return out
}

func stepNames(st step) string {
	var names []string
	for _, ur := range st.Repos {
		names = append(names, ur.FullName)
	}
	return strings.Join(names, ", ")
}

// releaseStep follows a merged step that is not the last: once the step
// reached what the next one waits for, the unit moves on to it.
func (p *Pipeline) releaseStep(ctx context.Context, job db.Job, u db.Unit, steps []step) error {
	cur := steps[u.MergeStep]
	next := steps[u.MergeStep+1]
	switch next.WaitFor {
	case WaitReleased:
		pending, failed, err := p.trackCI(ctx, u, cur.Repos)
		if err != nil {
			return err
		}
		if len(failed) > 0 {
			p.flag(ctx, u.ID, domain.AttentionCIFailed, fmt.Sprintf("CI failed on the merged code of step %d, which step %d waits for — %s", cur.Index+1, next.Index+1, strings.Join(failed, "; ")))
			return &stopError{fmt.Errorf("CI failed")}
		}
		if pending {
			return p.waitMore(ctx, job, u, fmt.Sprintf("CI on the merge commits of step %d", cur.Index+1))
		}
	case WaitTagged:
		var untagged []string
		for _, ur := range cur.Repos {
			ok, err := p.tagged(ctx, ur)
			if err != nil {
				return err
			}
			if !ok {
				untagged = append(untagged, ur.FullName)
			}
		}
		if len(untagged) > 0 {
			return p.waitMore(ctx, job, u, "a tag with the merged code of "+strings.Join(untagged, ", "))
		}
	}
	return p.advanceStep(ctx, job, u, steps)
}

// waitMore polls again in a minute, for up to two hours.
func (p *Pipeline) waitMore(ctx context.Context, job db.Job, u db.Unit, what string) error {
	if job.Attempts < releaseAttempts {
		return &jobs.RetryError{After: releasePoll, Err: fmt.Errorf("waiting for %s", what)}
	}
	p.flag(ctx, u.ID, domain.AttentionFailed, fmt.Sprintf("still waiting for %s after two hours; check it, then continue to the next step", what))
	return &stopError{fmt.Errorf("waited too long for %s", what)}
}

// tagged reports whether a tag on GitHub contains the repository's merge
// commit.
func (p *Pipeline) tagged(ctx context.Context, ur db.ListUnitReposRow) (bool, error) {
	if ur.MergeSha == "" {
		return false, nil
	}
	repo, err := p.Store.Q.GetRepo(ctx, ur.RepoID)
	if err != nil {
		return false, err
	}
	if repo, err = p.syncManagedClone(ctx, repo); err != nil {
		return false, err
	}
	tags, err := p.Git.TagsContaining(ctx, repo.ClonePath, ur.MergeSha)
	if err != nil {
		return false, nil // the merge commit is not fetched yet
	}
	return len(tags) > 0, nil
}

// advanceStep moves the unit to the next step with unmerged pull requests:
// through a development round when the step has an update to make, or
// straight to awaiting its merge.
func (p *Pipeline) advanceStep(ctx context.Context, job db.Job, u db.Unit, steps []step) error {
	next := int(u.MergeStep) + 1
	for next < len(steps)-1 && len(openRepos(steps[next])) == 0 {
		next++ // merged ahead of its turn, on GitHub
	}
	if err := p.Store.Q.SetUnitMergeStep(ctx, db.SetUnitMergeStepParams{MergeStep: int64(next), Now: store.Now(), ID: u.ID}); err != nil {
		return err
	}
	u.MergeStep = int64(next)
	st := steps[next]
	if len(openRepos(st)) == 0 {
		// Everything is merged: the last step releases as usual.
		return p.release(ctx, job, u)
	}
	label := fmt.Sprintf("step %d of %d (%s)", next+1, len(steps), stepNames(st))
	if st.Update == "" {
		_, err := p.transition(ctx, u, domain.StateAwaitingMerge, "system", label+" is ready to merge")
		return err
	}
	// The update gets its own count of review rounds.
	if err := p.Store.Q.SetUnitReviewIteration(ctx, db.SetUnitReviewIterationParams{ReviewIteration: 0, Now: store.Now(), ID: u.ID}); err != nil {
		return err
	}
	u, err := p.transition(ctx, u, domain.StateDeveloping, "system", label+": making its update first")
	if err != nil {
		return err
	}
	return p.enqueue(ctx, JobDevelop, u, developPayload{Step: next + 1})
}

// mergedSoFar describes what the steps before a step merged, for the round
// that prepares it.
func (p *Pipeline) mergedSoFar(ctx context.Context, steps []step, upTo int) []string {
	var out []string
	for _, st := range steps[:upTo] {
		for _, ur := range st.Repos {
			if !merged(ur) {
				continue
			}
			line := fmt.Sprintf("%s#%d, merged as %s into %s", ur.FullName, ur.PrNumber, ur.MergeSha, ur.DefaultBranch)
			if ur.MergedAt.Valid {
				line += " at " + ur.MergedAt.Time.UTC().Format(time.RFC3339)
			}
			if repo, err := p.Store.Q.GetRepo(ctx, ur.RepoID); err == nil && ur.MergeSha != "" {
				if tags, err := p.Git.TagsContaining(ctx, repo.ClonePath, ur.MergeSha); err == nil && len(tags) > 0 {
					slices.Sort(tags)
					line += "; tags containing it: " + strings.Join(tags, ", ")
				}
			}
			out = append(out, line)
		}
	}
	return out
}

// MergePlanView is a unit's merge plan as people see it.
type MergePlanView struct {
	// Current is the step being merged or prepared, from 0.
	Current int             `json:"current"`
	Steps   []MergeStepView `json:"steps"`
}

// MergeStepView is one step of a MergePlanView.
type MergeStepView struct {
	Index   int      `json:"index"`
	Repos   []string `json:"repos"`
	WaitFor string   `json:"wait_for"`
	Update  string   `json:"update,omitempty"`
	// State is merged, current or pending.
	State string `json:"state"`
}

// MergePlan resolves a unit's merge plan against its targets.
func (p *Pipeline) MergePlan(ctx context.Context, u db.Unit) (MergePlanView, error) {
	steps, _, err := p.unitSteps(ctx, u)
	if err != nil {
		return MergePlanView{}, err
	}
	cur := currentStep(u, steps).Index
	view := MergePlanView{Current: cur, Steps: []MergeStepView{}}
	for _, st := range steps {
		v := MergeStepView{Index: st.Index, Repos: []string{}, WaitFor: st.WaitFor, Update: st.Update, State: "pending"}
		for _, ur := range st.Repos {
			v.Repos = append(v.Repos, ur.FullName)
		}
		switch {
		case len(openRepos(st)) == 0 && len(st.Repos) > 0 && st.Repos[0].PrNumber > 0:
			v.State = "merged"
		case st.Index == cur:
			v.State = "current"
		}
		view.Steps = append(view.Steps, v)
	}
	return view, nil
}
