package pipeline

import (
	"context"
	"encoding/json"
	"errors"
	"path/filepath"
	"strings"
	"testing"

	"github.com/raulsh/tfy/internal/domain"
	"github.com/raulsh/tfy/internal/store/db"
)

// A review asked for by mistake and cancelled does not cost the approval:
// while the pull requests are where the approval saw them, the unit goes
// back to awaiting merge. Once someone pushes, it needs the review.
func TestBackToMergeAfterACancelledReview(t *testing.T) {
	h := newHarness(t)
	ctx := context.Background()
	u := h.approvedUnit("Report degraded health")
	u = h.waitState(u.ID, domain.StateAwaitingMerge)
	// What Review again and Cancel leave behind.
	u, err := h.p.transition(ctx, u, domain.StateReviewing, "user", "review requested")
	if err != nil {
		t.Fatal(err)
	}
	h.p.flag(ctx, u.ID, domain.AttentionInterrupted, "cancelled")
	u, _ = h.st.Q.GetUnit(ctx, u.ID)

	actions := h.p.Actions(ctx, u, false)
	if !contains(actions, ActionBackToMerge) || contains(actions, ActionMerge) {
		t.Fatalf("actions = %v", actions)
	}
	if u, err = h.p.Act(ctx, u.ID, ActionBackToMerge, ActionInput{}); err != nil {
		t.Fatal(err)
	}
	if u.State != string(domain.StateAwaitingMerge) || u.Attention != "" || !contains(h.p.Actions(ctx, u, false), ActionMerge) {
		t.Fatalf("unit %s / %q, actions %v", u.State, u.Attention, h.p.Actions(ctx, u, false))
	}

	// Someone pushes to the pull request, a review is asked for and cancelled:
	// the approval no longer covers what would merge.
	urs, _ := h.st.Q.ListUnitRepos(ctx, u.ID)
	clone := filepath.Join(t.TempDir(), "human")
	run(t, "", "git", "clone", "-q", "-b", urs[0].Branch, filepath.Join(h.remotes, "acme", "app.git"), clone)
	run(t, clone, "git", "-c", "user.name=h", "-c", "user.email=h@h", "commit", "-q", "--allow-empty", "-m", "tweak by a human")
	run(t, clone, "git", "push", "-q", "origin", urs[0].Branch)
	if u, err = h.p.transition(ctx, u, domain.StateReviewing, "user", "review requested"); err != nil {
		t.Fatal(err)
	}
	if _, err := h.p.syncPRs(ctx, u); err != nil {
		t.Fatal(err)
	}
	if contains(h.p.Actions(ctx, u, false), ActionBackToMerge) {
		t.Error("going back to merging must not skip a pushed commit's review")
	}
	var conflict *ConflictError
	if _, err := h.p.backToMerge(ctx, u, "user"); !errors.As(err, &conflict) {
		t.Errorf("back to merge after a push: %v", err)
	}
}

// A person reviewing the pull requests asks for changes after the review
// approved: development makes them, as that person's request, and the next
// review counts them as asked for.
func TestRequestChangesAfterApproval(t *testing.T) {
	h := newHarness(t)
	ctx := context.Background()
	u := h.approvedUnit("Report degraded health")
	u = h.waitState(u.ID, domain.StateAwaitingMerge)
	if _, err := h.p.Act(ctx, u.ID, ActionIterate, ActionInput{}); err == nil {
		t.Fatal("a request for changes needs to say what should change")
	}
	approval := h.latest(u.ID, DocReview)
	ask := "Log the database error before answering degraded."
	if _, err := h.p.Act(ctx, u.ID, ActionIterate, ActionInput{Feedback: ask, Actor: "ana"}); err != nil {
		t.Fatal(err)
	}
	u = h.waitState(u.ID, domain.StateAwaitingMerge)

	dev := h.read(filepath.Join(h.control, "prompt-develop.txt"))
	if !strings.Contains(dev, "# Make the changes ana asked for") || !strings.Contains(dev, "<requested-changes>\n"+ask) ||
		!strings.Contains(dev, "even where they go beyond docs/spec.md") {
		t.Errorf("development must get the request as ana's:\n%s", dev)
	}
	review := h.read(filepath.Join(h.control, "prompt-review.txt"))
	if !strings.Contains(review, "asked for these changes, beyond docs/spec.md") || !strings.Contains(review, "<request by=\"ana\">\n"+ask) {
		t.Errorf("the next review must know the change was asked for:\n%s", review)
	}
	versions, _ := h.st.Q.ListDocumentVersions(ctx, db.ListDocumentVersionsParams{UnitID: u.ID, Kind: DocReview})
	var request db.ListDocumentVersionsRow
	for _, v := range versions {
		if v.Version == approval.Version+1 {
			request = v
		}
	}
	doc, err := h.st.Q.GetDocumentVersion(ctx, db.GetDocumentVersionParams{UnitID: u.ID, Kind: DocReview, Version: request.Version})
	if err != nil || request.Author != "ana" {
		t.Fatalf("the request must be kept as ana's review (%v): %+v", err, request)
	}
	var meta ReviewMeta
	_ = json.Unmarshal([]byte(doc.Meta), &meta)
	if meta.By != "ana" || meta.Decision != "request_changes" || !strings.Contains(doc.Content, "Changes requested by ana") {
		t.Errorf("request = %+v\n%s", meta, doc.Content)
	}
	if latest := h.latest(u.ID, DocReview); latest.Version != request.Version+1 || latest.Author != "claude" {
		t.Errorf("the review run reviews the changes after the request (v%d by %s)", latest.Version, latest.Author)
	}
	runs, _ := h.st.Q.ListRuns(ctx, db.ListRunsParams{UnitID: u.ID, Lim: 20})
	var develops []db.ListRunsRow
	for _, r := range runs {
		if r.Kind == "develop" {
			develops = append(develops, r)
		}
	}
	if len(develops) != 2 || develops[0].ParentRunID != develops[1].ID {
		t.Error("the development round for the request resumes the development session")
	}
}

// Once some pull requests merged, a request for changes goes to the open
// ones only.
func TestRequestChangesLeavesMergedRepositoriesAlone(t *testing.T) {
	h := newHarness(t)
	ctx := context.Background()
	u := h.twoRepoUnit()
	// The api's pull request is merged on GitHub.
	prs := loadPRs("acme/api")
	merged := "2026-09-27T12:00:00Z"
	prs[0].State, prs[0].MergedAt = "MERGED", &merged
	prs[0].MergeCommit = &struct {
		Oid string `json:"oid"`
	}{Oid: remoteHead("acme/api", prs[0].HeadRefName)}
	savePRs("acme/api", prs)
	if _, err := h.p.syncPRs(ctx, u); err != nil {
		t.Fatal(err)
	}
	if _, err := h.p.Act(ctx, u.ID, ActionIterate, ActionInput{Feedback: "Name the marker provisioned_at."}); err != nil {
		t.Fatal(err)
	}
	u = h.waitStateFor(u.ID, domain.StateAwaitingMerge, mergeWait)
	settings := h.read(filepath.Join(h.p.Paths.RunDir(lastRunID(t, h, u.ID, "develop")), "settings.json"))
	if strings.Contains(settings, filepath.Join(u.WorkspacePath, "api")) || !strings.Contains(settings, filepath.Join(u.WorkspacePath, "app")) {
		t.Errorf("the round works on the open pull request's checkout only:\n%s", settings)
	}
	if review := h.read(filepath.Join(h.control, "prompt-review.txt")); strings.Contains(review, "api.diff") || !strings.Contains(review, "app.diff") {
		t.Errorf("only the open pull request is reviewed again:\n%s", review)
	}
}

// The review posted on the pull requests is short: an approval says why,
// a request for changes says only what sent the work back.
func TestReviewCommentIsBrief(t *testing.T) {
	criteria := []CriterionVerdict{
		{ID: "AC-1", Status: "met", Evidence: "health.go:12 returns degraded"},
		{ID: "AC-2", Status: "unmet", Evidence: "no test covers the timeout"},
		{ID: "AC-3", Status: "not_verifiable", Evidence: "needs a live database"},
	}
	findings := []Finding{
		{Severity: "major", Repo: "acme/app", File: "health.go", Line: 30, Message: "the error is swallowed"},
		{Severity: "minor", Repo: "acme/app", File: "health.go", Message: "rename err2"},
		{Severity: "nit", Repo: "acme/app", Message: "trailing space"},
	}

	approved := renderReview(ReviewMeta{Decision: "approve", Round: 1, Summary: "Ready: health reports degraded when the database is down.",
		Criteria: criteria[:1], Findings: findings[1:]})
	if approved != "✅ **Approved** · review round 1\n\nReady: health reports degraded when the database is down.\n" {
		t.Errorf("approval:\n%s", approved)
	}

	changes := renderReview(ReviewMeta{Decision: "request_changes", Round: 2, Summary: "The timeout path is untested and hides the error.",
		Criteria: criteria, Findings: findings})
	for _, want := range []string{"🔁 **Changes requested** · review round 2", "The timeout path is untested",
		"- AC-2 is unmet: no test covers the timeout", "- **major** `acme/app/health.go:30`: the error is swallowed"} {
		if !strings.Contains(changes, want) {
			t.Errorf("request for changes is missing %q:\n%s", want, changes)
		}
	}
	for _, noise := range []string{"AC-1", "AC-3", "rename err2", "trailing space", "|", "tfy"} {
		if strings.Contains(changes, noise) {
			t.Errorf("request for changes has %q:\n%s", noise, changes)
		}
	}
}
