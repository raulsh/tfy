package pipeline

import (
	"context"
	"encoding/json"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/raulsh/tfy/internal/domain"
	"github.com/raulsh/tfy/internal/store"
	"github.com/raulsh/tfy/internal/store/db"
)

// twoRepoUnit links acme/api and acme/app to a project, and takes a change
// to both through to awaiting merge.
func (h *harness) twoRepoUnit() db.Unit {
	h.t.Helper()
	ctx := context.Background()
	h.addRemote("acme/api")
	pr := h.project()
	if _, err := h.p.LinkRepo(ctx, pr.ID, "acme/api"); err != nil {
		h.t.Fatal(err)
	}
	h.setControl("plan.json", `{"summary": "Add the marker to the api, then use it in the app.", "target_repos": ["api", "app"],
		"acceptance_criteria": [{"id": "AC-1", "text": "the api has the marker"}], "new_dependencies": []}`)
	u := h.approvedUnit("Provisioned marker")
	return h.waitStateFor(u.ID, domain.StateAwaitingMerge, mergeWait)
}

// mergeWait bounds a whole merge: several merge runs, and an update
// published and reviewed, each a fake process that is slow under -race.
const mergeWait = 2 * time.Minute

// mergePrompts are the prompts of the unit's merge runs so far, in order.
func (h *harness) mergePrompts() []string {
	h.t.Helper()
	log := h.read(filepath.Join(h.control, "prompts-merge.log"))
	return strings.Split(strings.TrimSuffix(log, "\n----\n"), "\n----\n")
}

func (h *harness) mergeRuns(unitID string) []db.ListRunsRow {
	h.t.Helper()
	runs, err := h.st.Q.ListRuns(context.Background(), db.ListRunsParams{UnitID: unitID, Lim: 50})
	if err != nil {
		h.t.Fatal(err)
	}
	var out []db.ListRunsRow
	for i := len(runs) - 1; i >= 0; i-- {
		if runs[i].Kind == "merge" {
			out = append(out, runs[i])
		}
	}
	return out
}

// One Merge, and the merge run takes it from there: the dependency first,
// then the repository using it gets its pin moved to what merged —
// fetched through the checkouts, with no credentials — reviewed on its own,
// and merged after.
func TestMergeRunOrdersAndUpdates(t *testing.T) {
	h := newHarness(t)
	ctx := context.Background()
	u := h.twoRepoUnit()
	if dev := h.read(filepath.Join(h.control, "prompt-develop.txt")); !strings.Contains(dev, "depend on its commit on branch") {
		t.Errorf("development knows pins move when the pull requests merge:\n%s", dev)
	}
	h.setControl("merges", `{"action": "merge", "repos": ["api"], "wait_for": "", "reason": "api goes first: app pins it."}
{"action": "update", "repos": ["app"], "wait_for": "", "reason": "Pin acme/api to the commit it merged as."}`)

	if _, err := h.p.Act(ctx, u.ID, ActionMerge, ActionInput{}); err != nil {
		t.Fatal(err)
	}
	u = h.waitStateFor(u.ID, domain.StateDone, mergeWait)
	api, app := loadPRs("acme/api")[0], loadPRs("acme/app")[0]
	if api.State != "MERGED" || app.State != "MERGED" {
		t.Fatalf("api %s, app %s", api.State, app.State)
	}
	mergeSha := api.MergeCommit.Oid
	deps, err := exec.Command("git", "-C", filepath.Join(h.remotes, "acme", "app.git"), "show", "main:deps.txt").Output()
	if err != nil || strings.TrimSpace(string(deps)) != "acme/api "+mergeSha {
		t.Errorf("app must merge pinning api's merge commit %s, got %q (%v)", mergeSha, deps, err)
	}

	prompts := h.mergePrompts()
	if len(prompts) != 3 {
		t.Fatalf("%d merge runs, want 3 (merge api, update app, merge app)", len(prompts))
	}
	for _, want := range []string{"# Merge U-", "docs/spec.md", "- api/ is acme/api#1, branch", "- app/ is acme/app#1, branch", "tfy merges with squash"} {
		if !strings.Contains(prompts[0], want) {
			t.Errorf("the first merge run must see %q:\n%s", want, prompts[0])
		}
	}
	for _, want := range []string{"# Continue merging", "tfy merged acme/api#1.", "- api/ is acme/api#1, merged into main as " + mergeSha, "- app/ is acme/app#1"} {
		if !strings.Contains(prompts[1], want) {
			t.Errorf("the second merge run must see %q:\n%s", want, prompts[1])
		}
	}
	if !strings.Contains(prompts[2], "The review approved your update of acme/app") {
		t.Errorf("the third merge run hears the update was approved:\n%s", prompts[2])
	}
	runs := h.mergeRuns(u.ID)
	if runs[1].ParentRunID != runs[0].ID || runs[2].ParentRunID != runs[1].ID {
		t.Error("within a merge, each merge run resumes the one before")
	}
	if u.MergeRound != 0 {
		t.Errorf("merge round = %d once done, want 0", u.MergeRound)
	}

	// The merge run is shaped like a development run: an explicit tool
	// list, the guard, and the Stop hook for the open pull requests'
	// checkouts only.
	settings := h.read(filepath.Join(h.p.Paths.RunDir(runs[0].ID), "settings.json"))
	if !strings.Contains(settings, "hook-guard") || !strings.Contains(settings, "hook-stop") ||
		!strings.Contains(settings, filepath.Join(u.WorkspacePath, "app")) {
		t.Errorf("the merge run must be guarded and commit its work:\n%s", settings)
	}
	if last := h.read(filepath.Join(h.p.Paths.RunDir(runs[2].ID), "settings.json")); strings.Contains(last, filepath.Join(u.WorkspacePath, "api")) {
		t.Errorf("once api merged, the merge run may change only the open pull requests:\n%s", last)
	}
	var args []string
	_ = json.Unmarshal([]byte(h.read(filepath.Join(h.control, "args-merge.json"))), &args)
	if flagValue(args, "--tools") != "Bash,Read,Write,Edit" || flagValue(args, "--permission-mode") != "auto" {
		t.Errorf("merge run args: %v", args)
	}

	mirror := h.read(filepath.Join(h.control, "mirror.txt"))
	if !strings.Contains(mirror, mergeSha) || strings.Contains(mirror, "push error: <nil>") {
		t.Errorf("git must serve the merged api from the checkout, and refuse pushes:\n%s", mirror)
	}
	if out, _ := exec.Command("git", "-C", filepath.Join(h.remotes, "acme", "api.git"), "branch", "--list", "sneaky").Output(); len(out) > 0 {
		t.Fatal("a push through the mirror reached GitHub")
	}
	review := h.read(filepath.Join(h.control, "prompt-review.txt"))
	for _, want := range []string{"This round reviews an update made while merging", "Pin acme/api to the commit it merged as.", "docs/review/app.update.diff", "- api/ is acme/api#1, merged into main as " + mergeSha} {
		if !strings.Contains(review, want) {
			t.Errorf("the update's review must see %q:\n%s", want, review)
		}
	}
	if strings.Contains(review, "api.diff") {
		t.Errorf("the update is reviewed on its own:\n%s", review)
	}
	if update := h.read(filepath.Join(u.WorkspacePath, "docs", "review", "app.update.diff")); !strings.Contains(update, "+acme/api "+mergeSha) || strings.Contains(update, "health.txt") {
		t.Errorf("the update's diff holds the pin alone:\n%s", update)
	}
	if notes := h.latest(u.ID, DocReleaseNotes); notes.Version != 1 {
		t.Errorf("release notes are written once, at the end (v%d)", notes.Version)
	}
}

// When the review asks for changes to the update, the merge run fixes it:
// the same session hears the findings.
func TestMergeRunFixesItsUpdate(t *testing.T) {
	h := newHarness(t)
	ctx := context.Background()
	u := h.twoRepoUnit()
	h.verdicts("request_changes", "approve")
	h.setControl("merges", `{"action": "merge", "repos": ["acme/api"], "wait_for": "", "reason": "api first."}
{"action": "update", "repos": ["app"], "wait_for": "", "reason": "Pin the merged api."}
{"action": "update", "repos": ["app"], "wait_for": "", "reason": "Pin the merged api, fixed."}`)
	if _, err := h.p.Act(ctx, u.ID, ActionMerge, ActionInput{}); err != nil {
		t.Fatal(err)
	}
	u = h.waitStateFor(u.ID, domain.StateDone, mergeWait)
	prompts := h.mergePrompts()
	if len(prompts) != 4 || !strings.Contains(prompts[2], "The review of your update asked for changes") || !strings.Contains(prompts[2], "say degraded, not ok") {
		t.Fatalf("the merge run must hear the review's findings (%d runs):\n%s", len(prompts), strings.Join(prompts, "\n----\n"))
	}
	if u.ReviewIteration != 1 {
		t.Errorf("review iteration = %d, want 1: the update's own round", u.ReviewIteration)
	}
	if loadPRs("acme/app")[0].State != "MERGED" {
		t.Error("app must merge once the fixed update is approved")
	}
}

// A wait for CI that fails stops the merge before the next pull request;
// the next Merge starts over, with a fresh session.
func TestMergeRunWaitsForCI(t *testing.T) {
	h := newHarness(t)
	ctx := context.Background()
	u := h.twoRepoUnit()
	h.setControl("merges", `{"action": "merge", "repos": ["api"], "wait_for": "", "reason": "api first."}
{"action": "wait", "repos": ["api"], "wait_for": "released", "reason": "app needs the api deployed."}`)
	h.setControl("runs.json", `[{"databaseId": 1, "name": "deploy", "status": "completed", "conclusion": "failure", "url": "https://ci/1"}]`)
	if _, err := h.p.Act(ctx, u.ID, ActionMerge, ActionInput{}); err != nil {
		t.Fatal(err)
	}
	u = h.waitStateFor(u.ID, domain.StateAwaitingMerge, mergeWait)
	if u.Attention != string(domain.AttentionCIFailed) || !strings.Contains(u.AttentionDetail, "the next merge waits for") {
		t.Fatalf("attention = %s: %s", u.Attention, u.AttentionDetail)
	}
	if loadPRs("acme/app")[0].State != "OPEN" {
		t.Fatal("app must not merge while the api's CI fails")
	}
	if u.MergeRound != 0 {
		t.Errorf("merge round = %d after the merge stopped, want 0", u.MergeRound)
	}

	h.setControl("runs.json", `[{"databaseId": 2, "name": "deploy", "status": "completed", "conclusion": "success", "url": "https://ci/2"}]`)
	if _, err := h.p.Act(ctx, u.ID, ActionMerge, ActionInput{}); err != nil {
		t.Fatal(err)
	}
	u = h.waitStateFor(u.ID, domain.StateDone, mergeWait)
	runs := h.mergeRuns(u.ID)
	if last := runs[len(runs)-1]; last.ParentRunID != "" {
		t.Error("a new merge starts a new session")
	}
	if prompts := h.mergePrompts(); !strings.Contains(prompts[len(prompts)-1], "CI on the merge commit: success") {
		t.Errorf("the merge run sees CI on what merged:\n%s", prompts[len(prompts)-1])
	}
}

// What keeps a pull request from merging, such as a conflict, goes back to
// the merge run, which may resolve it or stop; nothing merges meanwhile.
func TestMergeRunHearsWhyNothingMerged(t *testing.T) {
	h := newHarness(t)
	ctx := context.Background()
	u := h.approvedUnit("Report degraded health")
	u = h.waitState(u.ID, domain.StateAwaitingMerge)
	h.setControl("mergeable", "CONFLICTING")
	h.setControl("merges", `{"action": "merge", "repos": ["app"], "wait_for": "", "reason": "One repository."}
{"action": "blocked", "repos": [], "wait_for": "", "reason": "The conflict needs a person."}`)
	if _, err := h.p.Act(ctx, u.ID, ActionMerge, ActionInput{}); err != nil {
		t.Fatal(err)
	}
	u = h.waitStateFor(u.ID, domain.StateAwaitingMerge, mergeWait)
	if prompts := h.mergePrompts(); len(prompts) != 2 || !strings.Contains(prompts[1], "tfy merged nothing:") || !strings.Contains(prompts[1], "acme/app#1 conflicts with main") {
		t.Fatalf("the merge run must hear about the conflict:\n%s", strings.Join(prompts, "\n----\n"))
	}
	if u.Attention != string(domain.AttentionMergeBlocked) || loadPRs("acme/app")[0].State != "OPEN" {
		t.Fatalf("unit %s / %s: %s; pull request %s", u.State, u.Attention, u.AttentionDetail, loadPRs("acme/app")[0].State)
	}
}

// A commit merged a moment ago shows no CI until GitHub starts its
// workflows: until then, no runs means not yet rather than none.
func TestCIGraceAfterMerge(t *testing.T) {
	h := newHarness(t)
	ctx := context.Background()
	for _, c := range []struct {
		ago     time.Duration
		pending bool
	}{{30 * time.Second, true}, {10 * time.Minute, false}} {
		ur := db.ListUnitReposRow{FullName: "acme/app", MergeSha: "abc", MergedAt: store.NullTime(time.Now().Add(-c.ago))}
		pending, failed, err := h.p.trackCI(ctx, db.Unit{ID: "u"}, []db.ListUnitReposRow{ur})
		if err != nil || len(failed) > 0 || pending != c.pending {
			t.Errorf("merged %s ago with no runs: pending %v, want %v (%v)", c.ago, pending, c.pending, err)
		}
	}
}

// When GitHub's rules for the base branch refuse the merge, such as a
// required approval, the unit goes back to a person, who may choose to merge
// as an administrator. That choice lasts for that merge only.
func TestMergeAsAdministrator(t *testing.T) {
	h := newHarness(t)
	ctx := context.Background()
	u := h.approvedUnit("Report degraded health")
	u = h.waitState(u.ID, domain.StateAwaitingMerge)
	h.setControl("branch-rules", "")
	if _, err := h.p.Act(ctx, u.ID, ActionMerge, ActionInput{}); err != nil {
		t.Fatal(err)
	}
	u = h.waitStateFor(u.ID, domain.StateAwaitingMerge, mergeWait)
	if u.Attention != string(domain.AttentionBranchRules) || !strings.Contains(u.AttentionDetail, "refuse to merge #1") || !strings.Contains(u.AttentionDetail, "merge as an administrator") {
		t.Fatalf("attention = %s: %s", u.Attention, u.AttentionDetail)
	}
	if loadPRs("acme/app")[0].State != "OPEN" || !contains(AvailableActions(u, false), ActionMerge) {
		t.Fatalf("nothing merged, and merging again is offered: %v", AvailableActions(u, false))
	}

	if _, err := h.p.Act(ctx, u.ID, ActionMerge, ActionInput{Admin: true}); err != nil {
		t.Fatal(err)
	}
	u = h.waitStateFor(u.ID, domain.StateDone, mergeWait)
	if loadPRs("acme/app")[0].State != "MERGED" {
		t.Fatal("an administrator's merge bypasses the rules")
	}
	acts, _ := h.st.Q.ListUnitActivity(ctx, db.ListUnitActivityParams{UnitID: store.NullString(u.ID), Lim: 100})
	var saw bool
	for _, a := range acts {
		saw = saw || a.Kind == "pr" && strings.Contains(a.Message, "(squash, as an administrator)")
	}
	if !saw {
		t.Error("the activity must say the merge bypassed the rules")
	}
	if u.MergeAdmin {
		t.Error("the choice to merge as an administrator ends with the merge")
	}
}

// A merge run that cannot go on safely returns the unit to a person.
func TestMergeRunStops(t *testing.T) {
	h := newHarness(t)
	ctx := context.Background()
	u := h.approvedUnit("Report degraded health")
	u = h.waitState(u.ID, domain.StateAwaitingMerge)
	h.setControl("merges", `{"action": "blocked", "repos": [], "wait_for": "", "reason": "The spec wants a migration run by hand first."}`)
	if _, err := h.p.Act(ctx, u.ID, ActionMerge, ActionInput{}); err != nil {
		t.Fatal(err)
	}
	u = h.waitState(u.ID, domain.StateAwaitingMerge)
	if u.Attention != string(domain.AttentionMergeBlocked) || !strings.Contains(u.AttentionDetail, "migration run by hand") {
		t.Fatalf("attention = %s: %s", u.Attention, u.AttentionDetail)
	}
	if loadPRs("acme/app")[0].State != "OPEN" {
		t.Fatal("nothing merges when the merge run stops")
	}
	if !contains(AvailableActions(u, false), ActionMerge) {
		t.Errorf("merging again must be possible: %v", AvailableActions(u, false))
	}
}
