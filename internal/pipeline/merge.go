package pipeline

// A person's Merge hands the unit's approved pull requests to a merge run.
// The run decides each next step: merge some of them, update the open ones
// for what merged (such as moving a pin to a merge commit), wait for a
// release or a tag, or stop. tfy carries the decision out with its own
// checks, then asks again with what happened, until everything is merged.
//
// The run changes checkouts as a development run does, and like one it can
// neither merge nor push: merges and pushes are tfy's, and an update is
// published and reviewed before anything more merges.

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"slices"
	"strings"
	"time"

	"github.com/raulsh/tfy/internal/domain"
	"github.com/raulsh/tfy/internal/gh"
	"github.com/raulsh/tfy/internal/jobs"
	"github.com/raulsh/tfy/internal/prompts"
	"github.com/raulsh/tfy/internal/store"
	"github.com/raulsh/tfy/internal/store/db"
)

// The merge run's decisions.
const (
	DecideMerge   = "merge"   // merge these pull requests now, together
	DecideUpdate  = "update"  // the run committed a change to open pull requests
	DecideWait    = "wait"    // until merged repositories are released or tagged
	DecideBlocked = "blocked" // it cannot go on safely
)

// What a wait decision waits for.
const (
	WaitReleased = "released" // CI, deploys included, succeeded on the merge commits
	WaitTagged   = "tagged"   // a tag contains each merge commit
)

// mergeRunStates are where a unit is while the merge run drives its merge:
// merging, and publishing and reviewing an update between two merges.
var mergeRunStates = []domain.State{domain.StateMerging, domain.StatePublishing, domain.StateReviewing}

// maxMergeRounds bounds the merge run's decisions in one merge.
const maxMergeRounds = 10

// MergeDecision is the merge run's structured output.
type MergeDecision struct {
	Action  string   `json:"action"`
	Repos   []string `json:"repos"`
	WaitFor string   `json:"wait_for"`
	Reason  string   `json:"reason"`
}

// mergePayload is a merge job's input: a decision still to carry out, or,
// for the next decision, what happened and the findings on the run's update,
// from the review or from RequestedBy.
type mergePayload struct {
	Decision    *MergeDecision `json:"decision,omitempty"`
	Notes       []string       `json:"notes,omitempty"`
	Findings    string         `json:"findings,omitempty"`
	RequestedBy string         `json:"requested_by,omitempty"`
}

// merge asks the merge run for decisions and carries them out until every
// pull request is merged, an update goes to review, or it has to wait.
func (p *Pipeline) merge(ctx context.Context, job db.Job, u db.Unit) error {
	payload := decodePayload[mergePayload](job)
	project, err := p.Store.Q.GetProject(ctx, u.ProjectID)
	if err != nil {
		return err
	}
	settings := domain.ParseProjectSettings(project.Settings)
	d, carried := payload.Decision, payload.Decision != nil
	next := mergePayload{Notes: payload.Notes, Findings: payload.Findings, RequestedBy: payload.RequestedBy}
	for {
		urs, err := p.syncPRs(ctx, u)
		if err != nil {
			return err
		}
		targets := targetsOf(urs)
		var open []db.ListUnitReposRow
		for _, ur := range targets {
			switch {
			case ur.PrNumber == 0 || merged(ur):
			case ur.PrState == "closed":
				return p.mergeBlocked(ctx, u, domain.AttentionPRClosed, fmt.Sprintf("%s #%d was closed without merging", ur.FullName, ur.PrNumber))
			default:
				open = append(open, ur)
			}
		}
		if len(open) == 0 {
			if u, err = p.transition(ctx, u, domain.StateReleasing, "system", "all pull requests merged"); err != nil {
				return err
			}
			return p.enqueue(ctx, JobRelease, u, nil)
		}

		if d == nil {
			if u.MergeRound >= maxMergeRounds {
				return p.mergeBlocked(ctx, u, domain.AttentionMergeBlocked, fmt.Sprintf("the merge run made %d decisions and the pull requests are still not all merged. Merge again to start over, or merge the rest on GitHub.", u.MergeRound))
			}
			if u, d, err = p.decideMerge(ctx, u, urs, settings, next); err != nil {
				return err
			}
			carried = false
		}

		var o mergeOutcome
		switch d.Action {
		case DecideMerge:
			o, err = p.carryMerge(ctx, job, u, d, carried, open, settings)
		case DecideUpdate:
			changed, err := p.updatedTargets(ctx, u, open)
			if err != nil {
				return err
			}
			if len(changed) == 0 {
				o.notes = []string{"You returned update, but no open pull request's checkout has changed. Commit the update, or decide something else."}
				break
			}
			if u, err = p.transition(ctx, u, domain.StatePublishing, "claude", "the merge run updated "+repoNames(changed)); err != nil {
				return err
			}
			return p.enqueue(ctx, JobPublish, u, nil)
		case DecideWait:
			o, err = p.carryWait(ctx, u, d, targets)
		default:
			return p.mergeBlocked(ctx, u, domain.AttentionMergeBlocked, "the merge run stopped: "+d.Reason)
		}
		if err != nil {
			return err
		}
		if o.wait != "" {
			return p.later(ctx, job, u, d, carried, o.after, o.wait)
		}
		next, d = mergePayload{Notes: o.notes}, nil
	}
}

// mergeOutcome is how carrying out a decision went: done, with what the
// next decision should hear, or not yet, waiting for something.
type mergeOutcome struct {
	notes []string
	wait  string
	after time.Duration
}

// decideMerge prepares the checkouts and asks the merge run for its next
// decision, telling it what in holds.
func (p *Pipeline) decideMerge(ctx context.Context, u db.Unit, urs []db.ListUnitReposRow, settings domain.ProjectSettings, in mergePayload) (db.Unit, *MergeDecision, error) {
	targets := targetsOf(urs)
	var open []db.ListUnitReposRow
	for _, ur := range targets {
		if ur.PrNumber > 0 && !merged(ur) {
			open = append(open, ur)
		}
	}
	for _, ur := range open {
		if err := p.prepareBranch(ctx, u, ur, ur.Branch); err != nil {
			return u, nil, err
		}
	}
	// What merged is what the open pull requests may now depend on.
	p.refreshDependencies(ctx, urs, open)
	if spec, err := p.Store.Q.LatestDocument(ctx, db.LatestDocumentParams{UnitID: u.ID, Kind: DocSpec}); err == nil {
		if err := writeDoc(u, DocSpec, spec.Content); err != nil {
			return u, nil, err
		}
	}

	data := prompts.Merge{Label: domain.Label(u.Seq), Title: u.Title, MergeMethod: settings.MergeMethod, Notes: in.Notes, Findings: in.Findings, RequestedBy: in.RequestedBy}
	for _, ur := range targets {
		switch {
		case ur.PrNumber == 0:
		case merged(ur):
			data.Merged = append(data.Merged, p.mergedLine(ctx, ur, true))
		default:
			data.Open = append(data.Open, openLine(ur))
		}
	}
	req := runRequest{Unit: u, Kind: "merge", Schema: prompts.Schema("merge")}
	for _, ur := range open {
		req.Trusted = append(req.Trusted, dirOf(ur))
	}
	// Within one merge the run keeps its session, and with it what it
	// found out so far.
	if u.MergeRound > 0 {
		if prev, err := p.Store.Q.LastSessionRun(ctx, db.LastSessionRunParams{UnitID: store.NullString(u.ID), Kind: "merge"}); err == nil {
			req.Resume, data.Continue = &prev, true
		}
	}
	var err error
	if req.Prompt, req.PromptVersion, err = prompts.Render("merge", data); err != nil {
		return u, nil, err
	}
	if req.System, err = systemPrompt(urs); err != nil {
		return u, nil, err
	}
	run, out, err := p.runClaude(ctx, req)
	if err != nil {
		return u, nil, err
	}
	if out.Status != "succeeded" {
		return u, nil, p.runFailed(ctx, u, run, out)
	}
	var d MergeDecision
	if err := json.Unmarshal([]byte(run.Result), &d); err != nil || !slices.Contains([]string{DecideMerge, DecideUpdate, DecideWait, DecideBlocked}, d.Action) {
		return u, nil, fmt.Errorf("the merge run returned no usable decision")
	}
	u.MergeRound++
	if err := p.Store.Q.SetUnitMergeRound(ctx, db.SetUnitMergeRoundParams{MergeRound: u.MergeRound, Now: store.Now(), ID: u.ID}); err != nil {
		return u, nil, err
	}
	p.activity(ctx, u.ID, "claude", "merge", describeDecision(d), map[string]string{"run_id": run.ID})
	return u, &d, nil
}

func describeDecision(d MergeDecision) string {
	var what string
	switch d.Action {
	case DecideMerge:
		what = "merge " + strings.Join(d.Repos, ", ")
	case DecideUpdate:
		what = "update " + strings.Join(d.Repos, ", ")
	case DecideWait:
		what = fmt.Sprintf("wait until %s is %s", strings.Join(d.Repos, ", "), d.WaitFor)
	default:
		what = "stop"
	}
	if r := strings.TrimSpace(d.Reason); r != "" {
		what += ": " + r
	}
	return what
}

// openLine describes an open pull request for the merge run.
func openLine(ur db.ListUnitReposRow) string {
	line := fmt.Sprintf("%s/ is %s#%d, branch %s, head %s", dirOf(ur), ur.FullName, ur.PrNumber, ur.Branch, ur.HeadSha)
	if ur.ReviewedSha != "" && ur.ReviewedSha == ur.HeadSha {
		line += ", reviewed"
	} else {
		line += ", with commits the review has not seen"
	}
	checks := ur.ChecksState
	if checks == "" {
		checks = "none"
	}
	return line + "; checks: " + checks
}

// mergedLine describes a merged pull request: its merge commit, the tags
// containing it as last fetched, and, with ci set, CI on it.
func (p *Pipeline) mergedLine(ctx context.Context, ur db.ListUnitReposRow, ci bool) string {
	line := fmt.Sprintf("%s/ is %s#%d, merged into %s as %s", dirOf(ur), ur.FullName, ur.PrNumber, ur.DefaultBranch, ur.MergeSha)
	if ur.MergedAt.Valid {
		line += " at " + ur.MergedAt.Time.UTC().Format(time.RFC3339)
	}
	if tags := p.tagsContaining(ctx, ur, false); len(tags) > 0 {
		line += "; tags containing it: " + strings.Join(tags, ", ")
	}
	if ci && ur.MergeSha != "" {
		if runs, err := p.GH.RunsForCommit(ctx, ur.FullName, ur.MergeSha); err == nil {
			line += "; CI on the merge commit: " + releaseState(runs)
		}
	}
	return line
}

// carryMerge merges the pull requests a decision names, together: only
// when each is reviewed as it stands, green and free of conflicts. What
// stops one stops them all, and the next decision hears why. A person may
// have chosen to merge as an administrator: GitHub's rules for the base
// branch are then bypassed, and checks still running are not waited for,
// since a required one may never report; tfy's own checks still hold.
func (p *Pipeline) carryMerge(ctx context.Context, job db.Job, u db.Unit, d *MergeDecision, carried bool, open []db.ListUnitReposRow, settings domain.ProjectSettings) (mergeOutcome, error) {
	picked := pickRepos(open, d.Repos)
	if len(picked) == 0 {
		return mergeOutcome{notes: []string{fmt.Sprintf("You asked to merge %s, but none of them has an open pull request.", strings.Join(d.Repos, ", "))}}, nil
	}
	type pending struct {
		ur db.ListUnitReposRow
		pr *gh.PR
	}
	var todo []pending
	var problems []string
	var wait mergeOutcome
	for _, ur := range picked {
		pr, err := p.GH.PRView(ctx, ur.FullName, int(ur.PrNumber))
		if err != nil {
			return mergeOutcome{}, err
		}
		if pr.State == "MERGED" {
			continue
		}
		if ur.ReviewedSha != "" && pr.HeadRefOid != "" && pr.HeadRefOid != ur.ReviewedSha {
			return mergeOutcome{}, p.mergeBlocked(ctx, u, domain.AttentionHeadChanged, fmt.Sprintf("%s #%d has commits the review did not see; review it again", ur.FullName, pr.Number))
		}
		switch pr.ChecksState() {
		case "failure":
			problems = append(problems, fmt.Sprintf("%s#%d has failing checks. If what merged caused them, fix it with an update; otherwise return blocked.", ur.FullName, pr.Number))
			continue
		case "pending":
			if u.MergeAdmin {
				break
			}
			if wait.wait == "" {
				wait = mergeOutcome{wait: fmt.Sprintf("the checks on %s #%d", ur.FullName, pr.Number), after: releasePoll}
			}
			continue
		}
		switch pr.Mergeable {
		case "CONFLICTING":
			problems = append(problems, fmt.Sprintf("%s#%d conflicts with %s. Merge base/%s into its branch and resolve the conflict as an update, or return blocked.", ur.FullName, pr.Number, ur.DefaultBranch, ur.DefaultBranch))
			continue
		case "UNKNOWN", "":
			// GitHub computes mergeability lazily; ask again shortly.
			if !carried || job.Attempts < 6 {
				if wait.wait == "" {
					wait = mergeOutcome{wait: fmt.Sprintf("GitHub to tell whether %s #%d can merge", ur.FullName, pr.Number), after: 10 * time.Second}
				}
				continue
			}
		}
		todo = append(todo, pending{ur, pr})
	}
	// A known problem is worth hearing now, not after the others' checks.
	if len(problems) > 0 {
		return mergeOutcome{notes: append([]string{"tfy merged nothing:"}, problems...)}, nil
	}
	if wait.wait != "" {
		return wait, nil
	}

	var done []string
	for _, t := range todo {
		if t.pr.IsDraft {
			if err := p.GH.PRReady(ctx, t.ur.FullName, t.pr.Number); err != nil {
				return mergeOutcome{}, p.partial(ctx, u, done, fmt.Errorf("mark %s #%d ready: %w", t.ur.FullName, t.pr.Number, err))
			}
		}
		head := t.ur.ReviewedSha
		if head == "" {
			head = t.pr.HeadRefOid
		}
		if err := p.GH.PRMerge(ctx, t.ur.FullName, t.pr.Number, settings.MergeMethod, head, settings.DeleteBranch, u.MergeAdmin); err != nil {
			if !u.MergeAdmin && gh.RulesRefused(err) {
				// Only a person can choose to bypass the rules: back to them.
				detail := fmt.Sprintf("GitHub's rules for %s's %s refuse to merge #%d yet, such as a required approval or an out-of-date branch. Meet them and merge again, or merge as an administrator to bypass them.", t.ur.FullName, t.ur.DefaultBranch, t.pr.Number)
				if len(done) > 0 {
					detail = "Merged " + strings.Join(done, ", ") + ". " + detail
				}
				return mergeOutcome{}, p.mergeBlocked(ctx, u, domain.AttentionBranchRules, detail)
			}
			return mergeOutcome{}, p.partial(ctx, u, done, fmt.Errorf("merge %s #%d: %w", t.ur.FullName, t.pr.Number, err))
		}
		done = append(done, fmt.Sprintf("%s#%d", t.ur.FullName, t.pr.Number))
		how := settings.MergeMethod
		if u.MergeAdmin {
			how += ", as an administrator"
		}
		p.activity(ctx, u.ID, "system", "pr", fmt.Sprintf("merged %s #%d (%s)", t.ur.FullName, t.pr.Number, how), map[string]string{"url": t.pr.URL})
	}
	if len(done) == 0 {
		return mergeOutcome{notes: []string{"Those pull requests were merged already."}}, nil
	}
	return mergeOutcome{notes: []string{"tfy merged " + strings.Join(done, ", ") + "."}}, nil
}

// carryWait checks whether the merged repositories a decision names are
// released or tagged.
func (p *Pipeline) carryWait(ctx context.Context, u db.Unit, d *MergeDecision, targets []db.ListUnitReposRow) (mergeOutcome, error) {
	var done []db.ListUnitReposRow
	for _, ur := range targets {
		if merged(ur) {
			done = append(done, ur)
		}
	}
	picked := pickRepos(done, d.Repos)
	if len(picked) == 0 {
		return mergeOutcome{notes: []string{fmt.Sprintf("You asked to wait for %s, but none of them is merged.", strings.Join(d.Repos, ", "))}}, nil
	}
	switch d.WaitFor {
	case WaitReleased:
		pending, failed, err := p.trackCI(ctx, u, picked)
		if err != nil {
			return mergeOutcome{}, err
		}
		if len(failed) > 0 {
			return mergeOutcome{}, p.mergeBlocked(ctx, u, domain.AttentionCIFailed, "CI failed on merged code the next merge waits for — "+strings.Join(failed, "; "))
		}
		if pending {
			return mergeOutcome{wait: "CI on the merge commits of " + repoNames(picked), after: releasePoll}, nil
		}
		return mergeOutcome{notes: []string{"CI succeeded on the merge commits of " + repoNames(picked) + "."}}, nil
	case WaitTagged:
		var tagged []string
		for _, ur := range picked {
			tags := p.tagsContaining(ctx, ur, true)
			if len(tags) == 0 {
				return mergeOutcome{wait: "a tag with the merged code of " + ur.FullName, after: releasePoll}, nil
			}
			tagged = append(tagged, fmt.Sprintf("%s is in %s", ur.FullName, strings.Join(tags, ", ")))
		}
		return mergeOutcome{notes: []string{"Tagged: " + strings.Join(tagged, "; ") + "."}}, nil
	}
	return mergeOutcome{notes: []string{"A wait needs wait_for: released or tagged."}}, nil
}

// later carries a decision out once what it waits for is there. A job that
// came with the decision polls again; otherwise a new job takes it over,
// which counts its own attempts.
func (p *Pipeline) later(ctx context.Context, job db.Job, u db.Unit, d *MergeDecision, carried bool, after time.Duration, what string) error {
	if !carried {
		_, err := p.Jobs.Enqueue(ctx, jobs.EnqueueOpts{
			Kind: JobMerge, UnitID: u.ID, ProjectID: u.ProjectID, Payload: mergePayload{Decision: d},
			DedupeKey: "unit:" + u.ID, After: after,
		})
		return err
	}
	if time.Duration(job.Attempts)*after < 2*time.Hour {
		return &jobs.RetryError{After: after, Err: fmt.Errorf("waiting for %s", what)}
	}
	return p.mergeBlocked(ctx, u, domain.AttentionMergeBlocked, fmt.Sprintf("still waiting for %s after two hours; check it, then merge again", what))
}

// tagsContaining lists the tags on GitHub that contain a repository's merge
// commit, as its managed clone last fetched them; with sync set, it fetches
// them first.
func (p *Pipeline) tagsContaining(ctx context.Context, ur db.ListUnitReposRow, sync bool) []string {
	if ur.MergeSha == "" {
		return nil
	}
	repo, err := p.Store.Q.GetRepo(ctx, ur.RepoID)
	if err != nil {
		return nil
	}
	if sync {
		if repo, err = p.syncManagedClone(ctx, repo); err != nil {
			return nil
		}
	}
	tags, err := p.Git.TagsContaining(ctx, repo.ClonePath, ur.MergeSha)
	if err != nil {
		return nil // the merge commit is not fetched yet
	}
	slices.Sort(tags)
	return tags
}

// updatedTargets are the open pull requests whose checkouts moved on from
// what the last approving review saw: the merge run's update.
func (p *Pipeline) updatedTargets(ctx context.Context, u db.Unit, open []db.ListUnitReposRow) ([]db.ListUnitReposRow, error) {
	approved := p.approvedHeads(ctx, u)
	var out []db.ListUnitReposRow
	for _, ur := range open {
		base := approved[ur.FullName]
		if base == "" {
			base = ur.ReviewedSha
		}
		head, err := p.Git.RevParse(ctx, ur.CheckoutPath, "HEAD")
		if err != nil {
			return nil, err
		}
		if dirty, _ := p.Git.Dirty(ctx, ur.CheckoutPath); dirty || head != base {
			out = append(out, ur)
		}
	}
	return out, nil
}

// approvedHeads are the commits the latest approving review saw, by
// repository.
func (p *Pipeline) approvedHeads(ctx context.Context, u db.Unit) map[string]string {
	versions, err := p.Store.Q.ListDocumentVersions(ctx, db.ListDocumentVersionsParams{UnitID: u.ID, Kind: DocReview})
	if err != nil {
		return nil
	}
	for _, v := range versions {
		doc, err := p.Store.Q.GetDocumentVersion(ctx, db.GetDocumentVersionParams{UnitID: u.ID, Kind: DocReview, Version: v.Version})
		if err != nil {
			continue
		}
		var m ReviewMeta
		if json.Unmarshal([]byte(doc.Meta), &m) == nil && m.Decision == "approve" {
			return m.Reviewed
		}
	}
	return nil
}

// lastMergeDecision is what the unit's latest merge run decided.
func (p *Pipeline) lastMergeDecision(ctx context.Context, u db.Unit) MergeDecision {
	var d MergeDecision
	if run, err := p.Store.Q.LastSessionRun(ctx, db.LastSessionRunParams{UnitID: store.NullString(u.ID), Kind: "merge"}); err == nil {
		_ = json.Unmarshal([]byte(run.Result), &d)
	}
	return d
}

func merged(ur db.ListUnitReposRow) bool { return ur.PrState == "merged" }

// pickRepos are the repositories of urs that names mention, by directory
// name or owner/name.
func pickRepos(urs []db.ListUnitReposRow, names []string) []db.ListUnitReposRow {
	var out []db.ListUnitReposRow
	for _, ur := range urs {
		if slices.ContainsFunc(names, func(n string) bool {
			n = strings.TrimSuffix(strings.TrimSpace(n), "/")
			return strings.EqualFold(n, dirOf(ur)) || strings.EqualFold(n, ur.FullName)
		}) {
			out = append(out, ur)
		}
	}
	return out
}

func repoNames(urs []db.ListUnitReposRow) string {
	var names []string
	for _, ur := range urs {
		names = append(names, ur.FullName)
	}
	return strings.Join(names, ", ")
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
