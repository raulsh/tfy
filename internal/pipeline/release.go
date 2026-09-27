package pipeline

import (
	"context"
	"encoding/json"
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

// Release states of a repository's merge commit.
const (
	ReleasePending = "pending"
	ReleaseSuccess = "success"
	ReleaseFailure = "failure"
	ReleaseNone    = "none" // no CI ran
)

// releaseWait bounds how long release waits for CI on the merge commits.
const (
	releasePoll     = time.Minute
	releaseAttempts = 120
)

// release writes the release notes, then follows CI on every merge commit:
// green everywhere finishes the unit, red asks for a follow-up.
func (p *Pipeline) release(ctx context.Context, job db.Job, u db.Unit) error {
	urs, err := p.Store.Q.ListUnitRepos(ctx, u.ID)
	if err != nil {
		return err
	}
	var merged []db.ListUnitReposRow
	for _, ur := range targetsOf(urs) {
		if ur.PrState == "merged" && ur.MergeSha != "" {
			merged = append(merged, ur)
		}
	}
	if _, err := p.Store.Q.LatestDocument(ctx, db.LatestDocumentParams{UnitID: u.ID, Kind: DocReleaseNotes}); store.IsNotFound(err) {
		if err := p.writeReleaseNotes(ctx, u, merged); err != nil {
			// Notes are nice to have; CI tracking goes on without them.
			p.Log.Warn("release notes", "unit", domain.Label(u.Seq), "error", err)
		}
	}

	pending, failed := false, []string{}
	for _, ur := range merged {
		runs, err := p.GH.RunsForCommit(ctx, ur.FullName, ur.MergeSha)
		if err != nil {
			return err
		}
		state := releaseState(runs)
		raw, _ := json.Marshal(runs)
		if err := p.Store.Q.SetUnitRepoRelease(ctx, db.SetUnitRepoReleaseParams{ReleaseState: state, ReleaseRuns: string(raw), Now: store.Now(), UnitID: u.ID, RepoID: ur.RepoID}); err != nil {
			return err
		}
		switch state {
		case ReleasePending:
			pending = true
		case ReleaseFailure:
			for _, r := range runs {
				if failedConclusion(r.Conclusion) {
					failed = append(failed, fmt.Sprintf("%s: %s", ur.FullName, r.Name))
				}
			}
		}
	}
	p.changed("unit", u.ID)
	if len(failed) > 0 {
		p.flag(ctx, u.ID, domain.AttentionCIFailed, "CI failed on the merged code — "+strings.Join(failed, "; "))
		return &stopError{fmt.Errorf("CI failed")}
	}
	if pending {
		if job.Attempts < releaseAttempts {
			return &jobs.RetryError{After: releasePoll, Err: fmt.Errorf("CI still running on the merge commits")}
		}
		p.flag(ctx, u.ID, domain.AttentionFailed, "CI did not finish within two hours; check it and mark the unit released")
		return &stopError{fmt.Errorf("CI timed out")}
	}
	_, err = p.transition(ctx, u, domain.StateDone, "system", "released: CI is green on every merge commit")
	return err
}

func failedConclusion(c string) bool {
	switch strings.ToLower(c) {
	case "failure", "cancelled", "timed_out", "startup_failure", "action_required":
		return true
	}
	return false
}

func releaseState(runs []gh.WorkflowRun) string {
	if len(runs) == 0 {
		return ReleaseNone
	}
	state := ReleaseSuccess
	for _, r := range runs {
		if !strings.EqualFold(r.Status, "completed") {
			state = ReleasePending
			continue
		}
		if failedConclusion(r.Conclusion) {
			return ReleaseFailure
		}
	}
	return state
}

// writeReleaseNotes asks Claude for user-facing notes about the merged
// change.
func (p *Pipeline) writeReleaseNotes(ctx context.Context, u db.Unit, merged []db.ListUnitReposRow) error {
	data := prompts.Release{Label: domain.Label(u.Seq), Title: u.Title, Summary: u.Summary}
	if doc, err := p.Store.Q.LatestDocument(ctx, db.LatestDocumentParams{UnitID: u.ID, Kind: DocSpec}); err == nil {
		var spec SpecMeta
		if json.Unmarshal([]byte(doc.Meta), &spec) == nil && spec.Summary != "" {
			data.Summary = spec.Summary
		}
	}
	if fbs, err := p.Store.Q.ListFeedbackByUnit(ctx, store.NullString(u.ID)); err == nil {
		for _, f := range fbs {
			if f.AuthorName != "" && !slices.Contains(data.Reporters, f.AuthorName) {
				data.Reporters = append(data.Reporters, f.AuthorName)
			}
		}
	}
	var commits []string
	for _, ur := range merged {
		data.PRs = append(data.PRs, fmt.Sprintf("%s#%d", ur.FullName, ur.PrNumber))
		if ur.CheckoutPath != "" && ur.BaseSha != "" {
			if log, err := p.Git.Log(ctx, ur.CheckoutPath, ur.BaseSha, "HEAD"); err == nil && log != "" {
				commits = append(commits, log)
			}
		}
	}
	data.Commits = strings.Join(commits, "\n")
	req := runRequest{Unit: u, Kind: "release"}
	var err error
	if req.Prompt, req.PromptVersion, err = prompts.Render("release", data); err != nil {
		return err
	}
	run, out, err := p.runClaude(ctx, req)
	if err != nil {
		return err
	}
	if out.Status != "succeeded" || strings.TrimSpace(run.Result) == "" {
		return fmt.Errorf("release notes run %s: %s", out.Status, out.Reason)
	}
	_, err = p.saveDoc(ctx, u, DocReleaseNotes, strings.TrimSpace(run.Result)+"\n", "{}", "claude", run.ID)
	return err
}

// followUp opens a bugfix unit for a release whose CI failed, and closes
// the released unit.
func (p *Pipeline) followUp(ctx context.Context, u db.Unit, actor string) (db.Unit, error) {
	child, err := p.newUnit(ctx, unitSpec{
		ProjectID:    u.ProjectID,
		Kind:         domain.KindBugfix,
		Title:        "Fix CI after " + domain.Label(u.Seq) + ": " + u.Title,
		Description:  fmt.Sprintf("CI failed after %s was merged.\n\n%s", domain.Label(u.Seq), u.AttentionDetail),
		Origin:       domain.OriginFollowUp,
		State:        domain.StateDefining,
		CreatedBy:    actor,
		ParentUnitID: u.ID,
	})
	if err != nil {
		return u, err
	}
	if err := p.enqueue(ctx, JobDefine, child, definePayload{}); err != nil {
		return u, err
	}
	return p.transition(ctx, u, domain.StateDone, actor, "follow-up "+domain.Label(child.Seq)+" opened for the failing CI")
}
