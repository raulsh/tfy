package pipeline

import (
	"context"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"github.com/raulsh/tfy/internal/domain"
	"github.com/raulsh/tfy/internal/store/db"
)

func TestResolveSteps(t *testing.T) {
	api := db.ListUnitReposRow{RepoID: "1", FullName: "acme/api", CheckoutPath: "/w/api"}
	app := db.ListUnitReposRow{RepoID: "2", FullName: "acme/app", CheckoutPath: "/w/app"}
	ui := db.ListUnitReposRow{RepoID: "3", FullName: "acme/ui", CheckoutPath: "/w/ui"}
	targets := []db.ListUnitReposRow{api, app, ui}
	names := func(steps []step) string {
		var out []string
		for _, st := range steps {
			var repos []string
			for _, ur := range st.Repos {
				repos = append(repos, dirOf(ur))
			}
			out = append(out, st.WaitFor+":"+strings.Join(repos, "+"))
		}
		return strings.Join(out, " | ")
	}
	cases := []struct {
		name string
		plan []MergeStep
		want string
	}{
		{"no plan merges everything at once", nil, "merged:api+app+ui"},
		{"steps in order", []MergeStep{{Repos: []string{"api"}}, {Repos: []string{"acme/app"}, WaitFor: "released"}, {Repos: []string{"ui/"}, WaitFor: "TAGGED"}},
			"merged:api | released:app | tagged:ui"},
		{"forgotten targets join the last step", []MergeStep{{Repos: []string{"api"}}, {Repos: []string{"app"}}}, "merged:api | merged:app+ui"},
		{"unknown names and repeats are ignored", []MergeStep{{Repos: []string{"api", "nope"}}, {Repos: []string{"api"}}, {Repos: []string{"app", "ui"}, WaitFor: "whenever"}},
			"merged:api | merged:app+ui"},
		{"the first step waits for nothing", []MergeStep{{Repos: []string{"api"}, WaitFor: "released"}, {Repos: []string{"app", "ui"}}}, "merged:api | merged:app+ui"},
	}
	for _, c := range cases {
		if got := names(resolveSteps(c.plan, targets)); got != c.want {
			t.Errorf("%s: %s, want %s", c.name, got, c.want)
		}
	}
}

// twoRepoPlan links acme/api and acme/app to a project, and makes the plan
// run ask for api to merge first and app to pin it afterwards.
func (h *harness) twoRepoPlan(waitFor string) db.Unit {
	h.t.Helper()
	ctx := context.Background()
	h.addRemote("acme/api")
	pr := h.project()
	if _, err := h.p.LinkRepo(ctx, pr.ID, "acme/api"); err != nil {
		h.t.Fatal(err)
	}
	h.setControl("plan.json", `{"summary": "Add the marker to the api, then use it in the app.", "target_repos": ["api", "app"],
		"acceptance_criteria": [{"id": "AC-1", "text": "the api has the marker"}], "new_dependencies": [],
		"merge_plan": [{"repos": ["api"], "wait_for": "merged", "update": ""},
			{"repos": ["app"], "wait_for": "`+waitFor+`", "update": "Pin acme/api to the commit step 1 merged."}]}`)
	u := h.approvedUnit("Provisioned marker")
	return h.waitState(u.ID, domain.StateAwaitingMerge)
}

// A dependency merges first; the repository using it then gets a round that
// pins what was merged — fetched through the checkouts, with no
// credentials — and is merged after.
func TestMergePlanStepsWithUpdates(t *testing.T) {
	h := newHarness(t)
	ctx := context.Background()
	u := h.twoRepoPlan("merged")
	plan, err := h.p.MergePlan(ctx, u)
	if err != nil || len(plan.Steps) != 2 || plan.Current != 0 || plan.Steps[0].State != "current" {
		t.Fatalf("merge plan = %+v (%v)", plan, err)
	}
	if dev := h.read(filepath.Join(h.control, "prompt-develop.txt")); !strings.Contains(dev, "Step 2: acme/app, once step 1 is merged") ||
		!strings.Contains(dev, "Never commit a replace directive") {
		t.Errorf("the first round knows the plan:\n%s", dev)
	}

	// Merge step 1: the api alone.
	if _, err := h.p.Act(ctx, u.ID, ActionMerge, ActionInput{}); err != nil {
		t.Fatal(err)
	}
	u = h.waitState(u.ID, domain.StateAwaitingMerge)
	if u.MergeStep != 1 {
		t.Fatalf("merge step = %d, want 1", u.MergeStep)
	}
	api, app := loadPRs("acme/api")[0], loadPRs("acme/app")[0]
	if api.State != "MERGED" || app.State != "OPEN" {
		t.Fatalf("after step 1: api %s, app %s", api.State, app.State)
	}
	mergeSha := api.MergeCommit.Oid
	dev := h.read(filepath.Join(h.control, "prompt-develop.txt"))
	for _, want := range []string{"# Merge step 2 of 2", "acme/api#1, merged as " + mergeSha, "Pin acme/api to the commit step 1 merged.", "- app/ (acme/app)"} {
		if !strings.Contains(dev, want) {
			t.Errorf("the update round must see %q:\n%s", want, dev)
		}
	}
	if strings.Contains(dev, "- api/ (") {
		t.Error("the update round works on the step's repositories only")
	}
	mirror := h.read(filepath.Join(h.control, "mirror.txt"))
	if !strings.Contains(mirror, mergeSha) || strings.Contains(mirror, "push error: <nil>") {
		t.Errorf("git must serve the merged api from the checkout, and refuse pushes:\n%s", mirror)
	}
	if out, _ := exec.Command("git", "-C", filepath.Join(h.remotes, "acme", "api.git"), "branch", "--list", "sneaky").Output(); len(out) > 0 {
		t.Fatal("a push through the mirror reached GitHub")
	}
	if review := h.read(filepath.Join(h.control, "prompt-review.txt")); !strings.Contains(review, "This round reviews the update step 2 needed") || !strings.Contains(review, "app.diff") || strings.Contains(review, "api.diff") {
		t.Errorf("the update is reviewed on its own:\n%s", review)
	}
	if body := loadPRs("acme/app")[0].Body; !strings.Contains(body, "acme/api") {
		t.Errorf("the app's pull request still links the api's:\n%s", body)
	}

	// Merge step 2, and the unit is released as a whole.
	if _, err := h.p.Act(ctx, u.ID, ActionMerge, ActionInput{}); err != nil {
		t.Fatal(err)
	}
	u = h.waitState(u.ID, domain.StateDone)
	if app := loadPRs("acme/app")[0]; app.State != "MERGED" {
		t.Errorf("app is %s", app.State)
	}
	if notes := h.latest(u.ID, DocReleaseNotes); notes.Version != 1 {
		t.Errorf("release notes are written once, at the end (v%d)", notes.Version)
	}
}

// A step waiting for its predecessor's CI stops when CI fails; marking it
// released goes on anyway.
func TestMergeStepWaitsForCI(t *testing.T) {
	h := newHarness(t)
	ctx := context.Background()
	u := h.twoRepoPlan("released")
	h.setControl("runs.json", `[{"databaseId": 1, "name": "deploy", "status": "completed", "conclusion": "failure", "url": "https://ci/1"}]`)
	if _, err := h.p.Act(ctx, u.ID, ActionMerge, ActionInput{}); err != nil {
		t.Fatal(err)
	}
	u = h.waitState(u.ID, domain.StateReleasing)
	if u.Attention != string(domain.AttentionCIFailed) || !strings.Contains(u.AttentionDetail, "which step 2 waits for") {
		t.Fatalf("attention = %s: %s", u.Attention, u.AttentionDetail)
	}
	if loadPRs("acme/app")[0].State != "OPEN" {
		t.Fatal("the next step must not start while CI fails")
	}
	if _, err := h.p.Act(ctx, u.ID, ActionMarkRelease, ActionInput{}); err != nil {
		t.Fatal(err)
	}
	u = h.waitState(u.ID, domain.StateAwaitingMerge)
	if u.MergeStep != 1 {
		t.Errorf("merge step = %d, want 1", u.MergeStep)
	}
}
