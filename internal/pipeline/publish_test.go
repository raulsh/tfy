package pipeline

import (
	"context"
	"path/filepath"
	"strings"
	"testing"

	"github.com/raulsh/tfy/internal/domain"
)

// A development run may bring a branch up to date with its default branch
// and then change nothing there. That branch has nothing to publish, and
// GitHub refuses a pull request for it; the other repositories' pull
// requests open, and are reviewed on their own changes alone.
func TestPublishSkipsABranchThatOnlyCaughtUp(t *testing.T) {
	h := newHarness(t)
	ctx := context.Background()
	h.addRemote("acme/api")
	pr := h.project()
	if _, err := h.p.LinkRepo(ctx, pr.ID, "acme/api"); err != nil {
		t.Fatal(err)
	}
	h.setControl("plan.json", `{"summary": "Name things the new way.", "target_repos": ["api", "app"],
		"acceptance_criteria": [{"id": "AC-1", "text": "things have the new names"}], "new_dependencies": []}`)
	u, err := h.p.CreateUnit(ctx, CreateUnitInput{ProjectID: pr.ID, Title: "New names"})
	if err != nil {
		t.Fatal(err)
	}
	h.waitState(u.ID, domain.StateDefinitionReview)
	if _, err := h.p.Act(ctx, u.ID, ActionMarkReady, ActionInput{}); err != nil {
		t.Fatal(err)
	}
	h.waitState(u.ID, domain.StateSpecReview)

	// Both default branches move on after the checkouts were made. The run
	// catches up with them, then changes the app alone.
	for _, repo := range []string{"acme/api", "acme/app"} {
		h.commitToRemote(repo, map[string]string{"upstream.txt": "someone else's work\n"})
	}
	h.setControl("catch-up", "api app")
	h.setControl("untouched", "api")
	if _, err := h.p.Act(ctx, u.ID, ActionApproveSpec, ActionInput{}); err != nil {
		t.Fatal(err)
	}
	u = h.waitState(u.ID, domain.StateAwaitingMerge)

	if prs := loadPRs("acme/api"); len(prs) != 0 {
		t.Errorf("acme/api only caught up, yet has pull requests: %+v", prs)
	}
	if prs := loadPRs("acme/app"); len(prs) != 1 {
		t.Fatalf("acme/app pull requests: %+v", prs)
	}
	diff := h.read(filepath.Join(u.WorkspacePath, "docs", "review", "app.diff"))
	if !strings.Contains(diff, "health.txt") || strings.Contains(diff, "upstream.txt") {
		t.Errorf("the review must see the app's own changes alone:\n%s", diff)
	}
}
