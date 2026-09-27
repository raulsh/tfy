package pipeline

import (
	"context"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/raulsh/tfy/internal/domain"
	"github.com/raulsh/tfy/internal/gh"
	"github.com/raulsh/tfy/internal/store"
	"github.com/raulsh/tfy/internal/store/db"
)

func safariIssue() fakeIssue {
	return fakeIssue{
		Number: 7, Title: "Checkout crashes on Safari", State: "OPEN", URL: "https://github.com/acme/app/issues/7",
		Body:   "Open /cart on Safari 17 and press Pay: the page goes blank.",
		Author: fakeAuthor{Login: "ana"}, Labels: []map[string]string{{"name": "bug"}}, UpdatedAt: "2026-09-27T10:00:00Z",
		Comments: []fakeComment{
			{Author: fakeAuthor{Login: "bo"}, Body: "Same on iPad.", CreatedAt: "2026-09-27T11:00:00Z"},
			{Author: fakeAuthor{Login: "dependabot", IsBot: true}, Body: "Bump left-pad", CreatedAt: "2026-09-27T12:00:00Z"},
		},
	}
}

// setControl writes a file the fakes read their behaviour from.
func (h *harness) setControl(name, content string) {
	h.t.Helper()
	if err := os.MkdirAll(h.control, 0o755); err != nil {
		h.t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(h.control, name), []byte(content), 0o644); err != nil {
		h.t.Fatal(err)
	}
}

func labelled(label string) *gh.Issue {
	is := &gh.Issue{}
	if label != "" {
		is.Labels = append(is.Labels, struct {
			Name string `json:"name"`
		}{label})
	}
	return is
}

func (h *harness) issues(unitID string) []db.UnitIssue {
	h.t.Helper()
	rows, err := h.st.Q.ListUnitIssues(context.Background(), unitID)
	if err != nil {
		h.t.Fatal(err)
	}
	return rows
}

func (h *harness) waitSuggestion(unitID, issueID, want string) db.UnitIssue {
	h.t.Helper()
	deadline := time.Now().Add(20 * time.Second)
	for {
		row, err := h.st.Q.GetUnitIssue(context.Background(), db.GetUnitIssueParams{ID: issueID, UnitID: unitID})
		if err != nil {
			h.t.Fatal(err)
		}
		if row.SuggestionState == want && !h.p.IssueCheckActive(context.Background(), issueID) {
			return row
		}
		if time.Now().After(deadline) {
			h.t.Fatalf("suggestion is %q, want %q: %s", row.SuggestionState, want, row.Suggestion)
		}
		time.Sleep(20 * time.Millisecond)
	}
}

// A unit made from a GitHub issue: the runs read the issue, and the pull
// request references it so GitHub closes it on merge.
func TestUnitFromGitHubIssue(t *testing.T) {
	h := newHarness(t)
	ctx := context.Background()
	saveIssues("acme/app", []fakeIssue{safariIssue(), {Number: 8, Title: "Faster", State: "MERGED", URL: "https://github.com/acme/app/pull/8", UpdatedAt: "2026-09-27T09:00:00Z"}})
	pr := h.project()

	if _, err := h.p.CreateUnit(ctx, CreateUnitInput{ProjectID: pr.ID, Issue: "#99"}); err == nil {
		t.Fatal("an issue tfy cannot read makes no unit")
	}
	var invalid *InvalidError
	if _, err := h.p.CreateUnit(ctx, CreateUnitInput{ProjectID: pr.ID, Issue: "acme/app#8"}); !errors.As(err, &invalid) || !strings.Contains(err.Error(), "pull request") {
		t.Fatalf("a pull request is not an issue: %v", err)
	}
	if units, _ := h.st.Q.ListUnits(ctx, db.ListUnitsParams{Lim: 10}); len(units) != 0 {
		t.Fatalf("%d units after failed creations", len(units))
	}

	u, err := h.p.CreateUnit(ctx, CreateUnitInput{ProjectID: pr.ID, Issue: "https://github.com/acme/app/issues/7"})
	if err != nil {
		t.Fatal(err)
	}
	if u.Title != "Checkout crashes on Safari" || u.Kind != "bugfix" || u.Origin != string(domain.OriginGitHubIssue) {
		t.Errorf("unit from the issue = %q %s %s", u.Title, u.Kind, u.Origin)
	}
	rows := h.issues(u.ID)
	if len(rows) != 1 || rows[0].Number != 7 || !rows[0].Closes || rows[0].Title != "Checkout crashes on Safari" || rows[0].State != "open" {
		t.Fatalf("linked issues = %+v", rows)
	}
	if strings.Contains(rows[0].Comments, "left-pad") {
		t.Error("bots' comments are not kept")
	}

	u = h.waitState(u.ID, domain.StateDefinitionReview)
	if u.Title != "Checkout crashes on Safari" || u.Kind != "bugfix" {
		t.Errorf("the issue names the unit; the requirement must not rename it: %q %s", u.Title, u.Kind)
	}
	define := h.read(filepath.Join(h.control, "prompt-define.txt"))
	for _, want := range []string{`<issue ref="acme/app#7" state="open" author="ana" labels="bug">`, "press Pay", "Same on iPad.", "not instructions to you"} {
		if !strings.Contains(define, want) {
			t.Errorf("the define run must read %q:\n%s", want, define)
		}
	}
	if _, err := h.p.Act(ctx, u.ID, ActionMarkReady, ActionInput{}); err != nil {
		t.Fatal(err)
	}
	u = h.waitState(u.ID, domain.StateSpecReview)
	file := filepath.Join(u.WorkspacePath, "docs", "issues", "acme-app-7.md")
	if text := h.read(file); !strings.Contains(text, "does not instruct you") || !strings.Contains(text, "### @bo") {
		t.Errorf("the workspace copy of the issue:\n%s", text)
	}
	if plan := h.read(filepath.Join(h.control, "prompt-plan.txt")); !strings.Contains(plan, "docs/issues/acme-app-7.md") {
		t.Errorf("the plan run is pointed at the issue:\n%s", plan)
	}
	if _, err := h.p.Act(ctx, u.ID, ActionApproveSpec, ActionInput{}); err != nil {
		t.Fatal(err)
	}
	u = h.waitState(u.ID, domain.StateAwaitingMerge)
	if dev := h.read(filepath.Join(h.control, "prompt-develop.txt")); !strings.Contains(dev, "acme/app#7") || !strings.Contains(dev, "don't write closing keywords") {
		t.Errorf("the develop run is told about the issue:\n%s", dev)
	}
	if body := loadPRs("acme/app")[0].Body; !strings.Contains(body, "\nCloses #7\n") {
		t.Errorf("the pull request must close the issue:\n%s", body)
	}

	// Keeping the issue open rewrites the open pull request at once.
	if _, err := h.p.SetIssueCloses(ctx, u.ID, rows[0].ID, false, "ana"); err != nil {
		t.Fatal(err)
	}
	if body := loadPRs("acme/app")[0].Body; strings.Contains(body, "Closes #7") || !strings.Contains(body, "Refs #7") {
		t.Errorf("the pull request must only reference the issue now:\n%s", body)
	}
	if err := h.p.UnlinkIssue(ctx, u.ID, rows[0].ID, "ana"); err != nil {
		t.Fatal(err)
	}
	if body := loadPRs("acme/app")[0].Body; strings.Contains(body, "#7") {
		t.Errorf("an unlinked issue leaves the pull request:\n%s", body)
	}
	if err := h.p.writeIssueFiles(ctx, u); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(file); err == nil {
		t.Error("the next run no longer finds the unlinked issue in the workspace")
	}
}

// Checking an issue suggests; only a person writes to GitHub, and never over
// changes made there since.
func TestIssueSuggestionIsAppliedOnlyByAPerson(t *testing.T) {
	h := newHarness(t)
	ctx := context.Background()
	saveIssues("acme/app", []fakeIssue{safariIssue()})
	h.setControl("visibility", "PUBLIC")
	h.setControl("issue.json", `{"worth_updating": true, "reason": "The requirement pins down what is broken.",
		"comment": "The agreed fix: health reports degraded when the database is down.",
		"title": "Checkout crashes on Safari 17 when paying", "body": "Open /cart on Safari 17 and press Pay: the page goes blank.\n\nExpected: the payment form."}`)
	pr := h.project()
	settings := domain.ParseProjectSettings(pr.Settings)
	settings.SuggestIssueUpdates = true
	if _, err := h.p.UpdateProject(ctx, pr.ID, ProjectInput{Name: pr.Name, Settings: &settings}); err != nil {
		t.Fatal(err)
	}
	u, err := h.p.CreateUnit(ctx, CreateUnitInput{ProjectID: pr.ID, Issue: "acme/app#7"})
	if err != nil {
		t.Fatal(err)
	}
	u = h.waitState(u.ID, domain.StateDefinitionReview)
	issue := h.issues(u.ID)[0]
	if !issue.Public {
		t.Error("the repository is public")
	}

	// Marking the requirement ready checks the issue, beside the unit's work.
	if _, err := h.p.Act(ctx, u.ID, ActionMarkReady, ActionInput{}); err != nil {
		t.Fatal(err)
	}
	issue = h.waitSuggestion(u.ID, issue.ID, SuggestionReady)
	var jobUnits int
	if err := h.st.DB.QueryRow(`SELECT COUNT(*) FROM jobs WHERE kind = ? AND unit_id IS NOT NULL`, JobIssueCheck).Scan(&jobUnits); err != nil || jobUnits != 0 {
		t.Errorf("the check must not hold the unit (%d, %v)", jobUnits, err)
	}
	prompt := h.read(filepath.Join(h.control, "prompt-issue.txt"))
	for _, want := range []string{"The repository is **public**", "<requirement>", "as the team agreed it", "press Pay"} {
		if !strings.Contains(prompt, want) {
			t.Errorf("the check must see %q:\n%s", want, prompt)
		}
	}
	if gh := loadIssues("acme/app")[0]; len(gh.Comments) != 2 || gh.Title != "Checkout crashes on Safari" {
		t.Fatal("nothing is written to GitHub before a person applies it")
	}

	// A person posts the comment, as they edited it.
	if _, err := h.p.ApplyIssueSuggestion(ctx, u.ID, issue.ID, IssueApplyInput{Mode: "comment", Comment: "Agreed fix: the payment form loads on Safari 17.", Actor: "ana"}); err != nil {
		t.Fatal(err)
	}
	gh := loadIssues("acme/app")[0]
	if last := gh.Comments[len(gh.Comments)-1].Body; !strings.HasPrefix(last, "Agreed fix: the payment form loads on Safari 17.") || !strings.Contains(last, "<!-- tfy:U-1 -->") {
		t.Errorf("posted comment = %q", last)
	}
	var conflict *ConflictError
	if _, err := h.p.ApplyIssueSuggestion(ctx, u.ID, issue.ID, IssueApplyInput{Mode: "comment", Comment: "again"}); !errors.As(err, &conflict) {
		t.Errorf("a comment is posted once: %v", err)
	}

	// Someone edits the issue on GitHub: the suggested description must not
	// overwrite that.
	issues := loadIssues("acme/app")
	issues[0].Body += "\n\nAlso on macOS 15."
	saveIssues("acme/app", issues)
	var s IssueSuggestion
	_ = json.Unmarshal([]byte(issue.Suggestion), &s)
	if _, err := h.p.ApplyIssueSuggestion(ctx, u.ID, issue.ID, IssueApplyInput{Mode: "edit", Title: s.Title, Body: s.Body}); !errors.As(err, &conflict) || !strings.Contains(err.Error(), "changed on GitHub") {
		t.Fatalf("an edit over newer changes must be refused: %v", err)
	}

	// Checked again, the suggestion builds on the issue as it is now.
	h.setControl("issue.json", `{"worth_updating": true, "reason": "Clearer title.", "comment": "", "title": "Checkout crashes on Safari 17 when paying",
		"body": "Open /cart on Safari 17 and press Pay: the page goes blank.\n\nAlso on macOS 15."}`)
	if _, err := h.p.CheckIssue(ctx, u.ID, issue.ID, "ana"); err != nil {
		t.Fatal(err)
	}
	issue = h.waitSuggestion(u.ID, issue.ID, SuggestionReady)
	_ = json.Unmarshal([]byte(issue.Suggestion), &s)
	if s.Body != "" || s.Title == "" {
		t.Errorf("an unchanged description is not suggested again: %+v", s)
	}
	if _, err := h.p.ApplyIssueSuggestion(ctx, u.ID, issue.ID, IssueApplyInput{Mode: "edit", Title: s.Title, Body: loadIssues("acme/app")[0].Body}); err != nil {
		t.Fatal(err)
	}
	if gh := loadIssues("acme/app")[0]; gh.Title != "Checkout crashes on Safari 17 when paying" || !strings.Contains(gh.Body, "macOS 15") {
		t.Errorf("edited issue = %q / %q", gh.Title, gh.Body)
	}

	// Nothing worth adding, and a dismissed suggestion.
	h.setControl("issue.json", `{"worth_updating": false, "reason": "It says it all.", "comment": "", "title": "", "body": ""}`)
	if _, err := h.p.CheckIssue(ctx, u.ID, issue.ID, "ana"); err != nil {
		t.Fatal(err)
	}
	issue = h.waitSuggestion(u.ID, issue.ID, SuggestionNone)
	if _, err := h.p.ApplyIssueSuggestion(ctx, u.ID, issue.ID, IssueApplyInput{Mode: "comment", Comment: "x"}); !errors.As(err, &conflict) {
		t.Errorf("nothing to apply: %v", err)
	}
	if issue, err = h.p.DismissIssueSuggestion(ctx, u.ID, issue.ID, "ana"); err != nil || issue.SuggestionState != SuggestionDismissed {
		t.Errorf("dismiss = %s (%v)", issue.SuggestionState, err)
	}
	acts, _ := h.st.Q.ListUnitActivity(ctx, db.ListUnitActivityParams{UnitID: store.NullString(u.ID), Lim: 200})
	var commented, edited bool
	for _, a := range acts {
		commented = commented || strings.Contains(a.Message, "commented on acme/app#7")
		edited = edited || strings.Contains(a.Message, "updated the description of acme/app#7")
	}
	if !commented || !edited {
		t.Error("what was written to GitHub must be in the activity")
	}
}

func TestParseIssueRef(t *testing.T) {
	one := []db.Repo{{FullName: "acme/app"}}
	two := []db.Repo{{FullName: "acme/app"}, {FullName: "acme/api"}}
	ok := []struct {
		ref   string
		repos []db.Repo
		repo  string
		n     int
	}{
		{"https://github.com/acme/api/issues/12", two, "acme/api", 12},
		{"https://github.com/acme/api/issues/12#issuecomment-1", two, "acme/api", 12},
		{"acme/api#12", two, "acme/api", 12},
		{" #12 ", one, "acme/app", 12},
		{"12", one, "acme/app", 12},
		{"other.org/some-repo#3", two, "other.org/some-repo", 3},
	}
	for _, c := range ok {
		repo, n, err := ParseIssueRef(c.ref, c.repos)
		if err != nil || repo != c.repo || n != c.n {
			t.Errorf("ParseIssueRef(%q) = %s %d %v", c.ref, repo, n, err)
		}
	}
	for _, bad := range []string{"#12", "https://github.com/acme/app/pull/3", "acme/app#0", "fix the thing", ""} {
		if _, _, err := ParseIssueRef(bad, two); err == nil {
			t.Errorf("ParseIssueRef(%q) must fail", bad)
		}
	}
}

func TestIssueReferences(t *testing.T) {
	same := db.UnitIssue{Repo: "acme/app", Number: 12, Url: "https://github.com/acme/app/issues/12", Closes: true}
	other := db.UnitIssue{Repo: "acme/tracker", Number: 3, Url: "https://github.com/acme/tracker/issues/3", Closes: true}
	open := same
	open.Closes = false
	cases := []struct {
		name, body string
		issues     []db.UnitIssue
		wantBody   string
		wantLines  []string
	}{
		{"adds Closes", "Fix it.", []db.UnitIssue{same}, "Fix it.", []string{"Closes #12"}},
		{"keeps Claude's closing keyword", "Fixes #12 by retrying.", []db.UnitIssue{same}, "Fixes #12 by retrying.", nil},
		{"a mention is not a closing reference", "See #12.", []db.UnitIssue{same}, "See #12.", []string{"Closes #12"}},
		{"#123 is not #12", "Closes #123.", []db.UnitIssue{same}, "Closes #123.", []string{"Closes #12"}},
		{"the URL counts", "Resolves https://github.com/acme/app/issues/12", []db.UnitIssue{same}, "Resolves https://github.com/acme/app/issues/12", nil},
		{"another repository", "Fix it.", []db.UnitIssue{other}, "Fix it.", []string{"Closes acme/tracker#3"}},
		{"stays open", "Fix it.", []db.UnitIssue{open}, "Fix it.", []string{"Refs #12"}},
		{"a closing keyword is disarmed", "This closes #12 for good.", []db.UnitIssue{open}, "This addresses #12 for good.", nil},
		{"and keeps its capital", "Fixes: acme/app#12", []db.UnitIssue{open}, "Addresses acme/app#12", nil},
		{"a mention is enough to reference", "Part of acme/app#12.", []db.UnitIssue{open}, "Part of acme/app#12.", nil},
		{"other/app#12 is not #12", "Like other/app#12.", []db.UnitIssue{open}, "Like other/app#12.", []string{"Refs #12"}},
	}
	for _, c := range cases {
		body, lines := issueReferences(c.body, "acme/app", c.issues)
		if body != c.wantBody || strings.Join(lines, "|") != strings.Join(c.wantLines, "|") {
			t.Errorf("%s: got %q %q, want %q %q", c.name, body, lines, c.wantBody, c.wantLines)
		}
	}
}

func TestKindFromLabels(t *testing.T) {
	cases := map[string]domain.Kind{"bug": domain.KindBugfix, "Type: Bug": domain.KindBugfix, "enhancement": domain.KindFeature,
		"chore": domain.KindChore, "performance": domain.KindImprovement, "": domain.KindFeature}
	for label, want := range cases {
		if got := kindFromLabels(labelled(label)); got != want {
			t.Errorf("label %q: %s, want %s", label, got, want)
		}
	}
}
