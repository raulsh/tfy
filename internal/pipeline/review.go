package pipeline

import (
	"cmp"
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/raulsh/tfy/internal/domain"
	"github.com/raulsh/tfy/internal/prompts"
	"github.com/raulsh/tfy/internal/store"
	"github.com/raulsh/tfy/internal/store/db"
)

// ReviewMeta is the review run's structured output, stored with the review
// document alongside the commits it looked at.
type ReviewMeta struct {
	Verdict  string              `json:"verdict"`
	Summary  string              `json:"summary"`
	Criteria []CriterionVerdict  `json:"criteria"`
	Findings []Finding           `json:"findings"`
	Round    int                 `json:"round"`
	Reviewed map[string]string   `json:"reviewed"` // repo → commit
	Spec     []prompts.Criterion `json:"spec_criteria"`
	Decision string              `json:"decision"` // approve | request_changes, after tfy's own rules
	// By names the person who asked for changes; it is empty for the
	// review run's reviews.
	By string `json:"by,omitempty"`
}

// CriterionVerdict is the reviewer's call on one acceptance criterion.
type CriterionVerdict struct {
	ID       string `json:"id"`
	Status   string `json:"status"`
	Evidence string `json:"evidence"`
}

// Finding is one problem the reviewer raised.
type Finding struct {
	Severity string `json:"severity"`
	Repo     string `json:"repo"`
	File     string `json:"file"`
	Line     int    `json:"line,omitempty"`
	Message  string `json:"message"`
}

// maxDiff bounds the diff files handed to the reviewer; it can read the code
// itself for the rest.
const maxDiff = 400 << 10

// decide turns the review into tfy's decision. Only concrete
// problems send work back: an unmet or partial criterion, or a blocker or
// major finding. A criterion the reviewer could not verify (it cannot run
// builds or tests; CI does) does not, whatever the reviewer's verdict.
func (m *ReviewMeta) decide() string {
	for _, c := range m.Criteria {
		if c.Status == "unmet" || c.Status == "partial" {
			return "request_changes"
		}
	}
	for _, f := range m.Findings {
		if f.Severity == "blocker" || f.Severity == "major" {
			return "request_changes"
		}
	}
	return "approve"
}

// review checks the published change against the spec, then approves it for
// merging or sends it back to development.
func (p *Pipeline) review(ctx context.Context, job db.Job, u db.Unit) error {
	urs, err := p.Store.Q.ListUnitRepos(ctx, u.ID)
	if err != nil {
		return err
	}
	// The merge run's update is reviewed on its own: the rest was approved.
	targets, updating, err := p.activeTargets(ctx, u)
	if err != nil {
		return err
	}
	spec, err := p.Store.Q.LatestDocument(ctx, db.LatestDocumentParams{UnitID: u.ID, Kind: DocSpec})
	if err != nil {
		return fmt.Errorf("there is no spec to review against")
	}
	var specMeta SpecMeta
	_ = json.Unmarshal([]byte(spec.Meta), &specMeta)
	if err := writeDoc(u, DocSpec, spec.Content); err != nil {
		return err
	}

	reviewDir := filepath.Join(u.WorkspacePath, "docs", "review")
	if err := os.RemoveAll(reviewDir); err != nil {
		return err
	}
	if err := os.MkdirAll(reviewDir, 0o755); err != nil {
		return err
	}
	data := prompts.Review{
		Label: domain.Label(u.Seq), Title: u.Title, Criteria: specMeta.AcceptanceCriteria, Round: int(u.ReviewIteration),
		TestReport: p.lastTestReport(ctx, u),
	}
	var approved map[string]string
	if updating {
		data.Update = p.lastMergeDecision(ctx, u).Reason
		if data.Update == "" {
			data.Update = "(the merge run gave no description)"
		}
		for _, ur := range targetsOf(urs) {
			if merged(ur) {
				data.Merged = append(data.Merged, p.mergedLine(ctx, ur, false))
			}
		}
		approved = p.approvedHeads(ctx, u)
	}
	reviewed := map[string]string{}
	for _, ur := range targets {
		if ur.PrNumber == 0 {
			continue
		}
		head, err := p.followRemoteBranch(ctx, ur)
		if err != nil {
			return err
		}
		diff, err := p.Git.Diff(ctx, ur.CheckoutPath, ur.BaseSha, head)
		if err != nil {
			return err
		}
		if len(diff) > maxDiff {
			diff = diff[:maxDiff] + "\n\n[diff truncated: read the files in the checkout for the rest]\n"
		}
		log, _ := p.Git.Log(ctx, ur.CheckoutPath, ur.BaseSha, head)
		file := dirOf(ur) + ".diff"
		if err := os.WriteFile(filepath.Join(reviewDir, file), []byte(diff), 0o644); err != nil {
			return err
		}
		commits := 0
		if log != "" {
			commits = len(strings.Split(log, "\n"))
		}
		checks := ur.ChecksState
		if pr, err := p.GH.PRView(ctx, ur.FullName, int(ur.PrNumber)); err == nil {
			checks = pr.ChecksState()
		}
		if checks == "" {
			checks = "none"
		}
		rd := prompts.ReviewDiff{Dir: dirOf(ur), FullName: ur.FullName, Branch: ur.Branch, File: file, Commits: commits, Checks: checks}
		if from := cmp.Or(approved[ur.FullName], ur.ReviewedSha); updating && from != "" && from != head {
			// The update alone, on top of what was approved.
			update, err := p.Git.Diff(ctx, ur.CheckoutPath, from, head)
			if err != nil {
				return err
			}
			rd.UpdateFile = dirOf(ur) + ".update.diff"
			if err := os.WriteFile(filepath.Join(reviewDir, rd.UpdateFile), []byte(update), 0o644); err != nil {
				return err
			}
		}
		data.Diffs = append(data.Diffs, rd)
		reviewed[ur.FullName] = head
	}
	if len(data.Diffs) == 0 {
		return fmt.Errorf("there is no pull request to review")
	}
	data.Requested = p.personRequests(ctx, u)

	req := runRequest{Unit: u, Kind: "review", Schema: prompts.Schema("review")}
	if req.Prompt, req.PromptVersion, err = prompts.Render("review", data); err != nil {
		return err
	}
	if req.System, err = systemPrompt(urs); err != nil {
		return err
	}
	run, out, err := p.runClaude(ctx, req)
	if err != nil {
		return err
	}
	if out.Status != "succeeded" {
		return p.runFailed(ctx, u, run, out)
	}
	var meta ReviewMeta
	if err := json.Unmarshal([]byte(run.Result), &meta); err != nil || meta.Verdict == "" {
		return fmt.Errorf("the review run returned no usable verdict")
	}
	meta.Round = int(u.ReviewIteration) + 1
	meta.Reviewed = reviewed
	meta.Spec = specMeta.AcceptanceCriteria
	meta.Decision = meta.decide()
	// What the run saw counts as reviewed only now that it has a verdict:
	// a review cancelled half-way leaves the last one standing.
	for _, ur := range targets {
		if head, ok := reviewed[ur.FullName]; ok {
			if err := p.Store.Q.SetUnitRepoReviewed(ctx, db.SetUnitRepoReviewedParams{ReviewedSha: head, Now: store.Now(), UnitID: u.ID, RepoID: ur.RepoID}); err != nil {
				return err
			}
		}
	}
	metaJSON, _ := json.Marshal(meta)
	doc, err := p.saveDoc(ctx, u, DocReview, renderReview(u, meta), string(metaJSON), "claude", run.ID)
	if err != nil {
		return err
	}
	p.postReview(ctx, u, targets, doc.Content)

	if meta.Decision == "approve" {
		if updating {
			if u, err = p.transition(ctx, u, domain.StateMerging, "claude", fmt.Sprintf("review round %d approved the merge run's update", meta.Round)); err != nil {
				return err
			}
			return p.enqueue(ctx, JobMerge, u, mergePayload{Notes: []string{"The review approved your update of " + repoNames(targets) + ", and tfy pushed it."}})
		}
		_, err := p.transition(ctx, u, domain.StateAwaitingMerge, "claude", fmt.Sprintf("review round %d approved", meta.Round))
		return err
	}
	project, err := p.Store.Q.GetProject(ctx, u.ProjectID)
	if err != nil {
		return err
	}
	settings := domain.ParseProjectSettings(project.Settings)
	if int(u.ReviewIteration) >= settings.MaxReviewIterations {
		p.flag(ctx, u.ID, domain.AttentionReviewBlocked,
			fmt.Sprintf("review round %d still requests changes after %d automatic rounds: iterate anyway, override, or revise the spec", meta.Round, settings.MaxReviewIterations))
		return &stopError{fmt.Errorf("review blocked")}
	}
	return p.sendBack(ctx, u, findingsBrief(meta), "", "claude", fmt.Sprintf("review round %d requested changes", meta.Round))
}

// sendBack starts another development round to address findings or, while
// the merge run drives a merge, another decision of the merge run, which
// made the update and so makes the fix. requestedBy names the person who
// asked for the changes, when it was not the review run.
func (p *Pipeline) sendBack(ctx context.Context, u db.Unit, findings, requestedBy, actor, why string) error {
	if err := p.Store.Q.SetUnitReviewIteration(ctx, db.SetUnitReviewIterationParams{ReviewIteration: u.ReviewIteration + 1, Now: store.Now(), ID: u.ID}); err != nil {
		return err
	}
	if u.MergeRound > 0 {
		u, err := p.transition(ctx, u, domain.StateMerging, actor, why)
		if err != nil {
			return err
		}
		return p.enqueue(ctx, JobMerge, u, mergePayload{Findings: findings, RequestedBy: requestedBy})
	}
	u, err := p.transition(ctx, u, domain.StateDeveloping, actor, why)
	if err != nil {
		return err
	}
	return p.enqueue(ctx, JobDevelop, u, developPayload{Findings: findings, RequestedBy: requestedBy})
}

// requestChanges sends the work back with what a person asks for, after a
// review approved it or while a review is not running. A blocked review's
// findings go back too. The person's request is kept as a review of its
// own: later reviews treat it as part of what was asked.
func (p *Pipeline) requestChanges(ctx context.Context, u db.Unit, feedback, actor string) error {
	blocked := u.Attention == string(domain.AttentionReviewBlocked)
	if feedback == "" && !blocked {
		return &InvalidError{Msg: "say what should change"}
	}
	last, _ := p.latestReview(ctx, u)
	if feedback != "" {
		p.activity(ctx, u.ID, actor, "feedback", feedback, nil)
		urs, err := p.Store.Q.ListUnitRepos(ctx, u.ID)
		if err != nil {
			return err
		}
		req := ReviewMeta{
			Verdict: "request_changes", Decision: "request_changes", By: actor, Summary: feedback, Round: max(last.Round, 1),
			Findings: []Finding{{Severity: "major", Repo: "all", Message: feedback}}, Reviewed: map[string]string{},
		}
		for _, ur := range targetsOf(urs) {
			if ur.PrNumber > 0 && !merged(ur) {
				req.Reviewed[ur.FullName] = ur.HeadSha
			}
		}
		raw, _ := json.Marshal(req)
		if _, err := p.saveDoc(ctx, u, DocReview, renderReview(u, req), string(raw), actor, ""); err != nil {
			return err
		}
	}
	if !blocked {
		return p.sendBack(ctx, u, feedback, actor, actor, actor+" requested changes")
	}
	if feedback != "" {
		last.Findings = append(last.Findings, Finding{Severity: "major", Repo: "all", Message: feedback})
	}
	return p.sendBack(ctx, u, findingsBrief(last), "", actor, fmt.Sprintf("another round for review round %d", last.Round))
}

// reviewVerdict is what the latest review of a repository saw and decided.
type reviewVerdict struct {
	head     string
	approved bool
}

// reviewVerdicts are, by repository, the commit the latest review that
// looked at it saw, and whether that review approved.
func (p *Pipeline) reviewVerdicts(ctx context.Context, u db.Unit) map[string]reviewVerdict {
	out := map[string]reviewVerdict{}
	versions, err := p.Store.Q.ListDocumentVersions(ctx, db.ListDocumentVersionsParams{UnitID: u.ID, Kind: DocReview})
	if err != nil {
		return out
	}
	for _, v := range versions { // newest first
		doc, err := p.Store.Q.GetDocumentVersion(ctx, db.GetDocumentVersionParams{UnitID: u.ID, Kind: DocReview, Version: v.Version})
		if err != nil {
			continue
		}
		var m ReviewMeta
		if json.Unmarshal([]byte(doc.Meta), &m) != nil {
			continue
		}
		for repo, head := range m.Reviewed {
			if _, seen := out[repo]; !seen {
				out[repo] = reviewVerdict{head: head, approved: m.Decision == "approve"}
			}
		}
	}
	return out
}

// approvalHolds reports whether every open pull request is at the commit a
// review approved, with no review since that asked for changes.
func (p *Pipeline) approvalHolds(ctx context.Context, u db.Unit) bool {
	urs, err := p.Store.Q.ListUnitRepos(ctx, u.ID)
	if err != nil {
		return false
	}
	verdicts := p.reviewVerdicts(ctx, u)
	open := 0
	for _, ur := range targetsOf(urs) {
		if ur.PrNumber == 0 || merged(ur) {
			continue
		}
		open++
		if v := verdicts[ur.FullName]; !v.approved || ur.HeadSha == "" || v.head != ur.HeadSha {
			return false
		}
	}
	return open > 0
}

// backToMerge undoes a review asked for by mistake, once GitHub confirms
// that the pull requests are still at the commits the last approval saw.
func (p *Pipeline) backToMerge(ctx context.Context, u db.Unit, actor string) (db.Unit, error) {
	if _, err := p.syncPRs(ctx, u); err != nil {
		return u, err
	}
	if !p.approvalHolds(ctx, u) {
		return u, &ConflictError{Msg: "the pull requests changed since the last approval: review them again"}
	}
	urs, err := p.Store.Q.ListUnitRepos(ctx, u.ID)
	if err != nil {
		return u, err
	}
	for _, ur := range targetsOf(urs) {
		if ur.PrNumber > 0 && !merged(ur) {
			// Merging checks against what the approval saw.
			if err := p.Store.Q.SetUnitRepoReviewed(ctx, db.SetUnitRepoReviewedParams{ReviewedSha: ur.HeadSha, Now: store.Now(), UnitID: u.ID, RepoID: ur.RepoID}); err != nil {
				return u, err
			}
		}
	}
	return p.transition(ctx, u, domain.StateAwaitingMerge, actor, "back to merging: the last approval still covers the pull requests")
}

// personRequests are the changes people asked for since the spec was last
// written, oldest first.
func (p *Pipeline) personRequests(ctx context.Context, u db.Unit) []prompts.Request {
	spec, err := p.Store.Q.LatestDocument(ctx, db.LatestDocumentParams{UnitID: u.ID, Kind: DocSpec})
	if err != nil {
		return nil
	}
	versions, err := p.Store.Q.ListDocumentVersions(ctx, db.ListDocumentVersionsParams{UnitID: u.ID, Kind: DocReview})
	if err != nil {
		return nil
	}
	var out []prompts.Request
	for i := len(versions) - 1; i >= 0; i-- {
		v := versions[i]
		if v.Author == "claude" || v.CreatedAt.Before(spec.CreatedAt) {
			continue
		}
		doc, err := p.Store.Q.GetDocumentVersion(ctx, db.GetDocumentVersionParams{UnitID: u.ID, Kind: DocReview, Version: v.Version})
		if err != nil {
			continue
		}
		var m ReviewMeta
		if json.Unmarshal([]byte(doc.Meta), &m) == nil && m.By != "" && strings.TrimSpace(m.Summary) != "" {
			out = append(out, prompts.Request{By: m.By, Text: m.Summary})
		}
	}
	return out
}

// followRemoteBranch brings the checkout up to the pull request's head on
// GitHub, so commits pushed there by people are reviewed too. It returns the
// commit under review.
func (p *Pipeline) followRemoteBranch(ctx context.Context, ur db.ListUnitReposRow) (string, error) {
	repo, err := p.Store.Q.GetRepo(ctx, ur.RepoID)
	if err != nil {
		return "", err
	}
	if repo, err = p.syncManagedClone(ctx, repo); err != nil {
		return "", err
	}
	local, err := p.Git.RevParse(ctx, ur.CheckoutPath, "HEAD")
	if err != nil {
		return "", err
	}
	remoteRef := "refs/remotes/pr/" + ur.Branch
	if err := p.Git.FetchInto(ctx, ur.CheckoutPath, repo.ClonePath, "refs/heads/"+ur.Branch, remoteRef); err != nil {
		// The branch may be gone from GitHub (deleted after a merge).
		return local, nil
	}
	remote, err := p.Git.RevParse(ctx, ur.CheckoutPath, remoteRef)
	if err != nil || remote == local {
		return local, nil
	}
	if ok, _ := p.Git.IsAncestor(ctx, ur.CheckoutPath, local, remote); ok {
		if err := p.Git.FastForward(ctx, ur.CheckoutPath, remoteRef); err != nil {
			return "", err
		}
		return remote, nil
	}
	if ok, _ := p.Git.IsAncestor(ctx, ur.CheckoutPath, remote, local); ok {
		return local, nil // local commits not pushed yet
	}
	return "", fmt.Errorf("%s: the branch on GitHub and the local checkout have diverged", ur.FullName)
}

// findingsBrief is what the next development round is told to fix.
func findingsBrief(m ReviewMeta) string {
	var b strings.Builder
	for _, c := range m.Criteria {
		if c.Status != "met" {
			fmt.Fprintf(&b, "- %s is %s: %s\n", c.ID, strings.ReplaceAll(c.Status, "_", " "), c.Evidence)
		}
	}
	for _, f := range m.Findings {
		if f.Severity == "nit" {
			continue
		}
		loc := f.Repo
		if f.File != "" {
			loc += "/" + f.File
			if f.Line > 0 {
				loc += fmt.Sprintf(":%d", f.Line)
			}
		}
		fmt.Fprintf(&b, "- [%s] %s: %s\n", f.Severity, loc, f.Message)
	}
	if b.Len() == 0 {
		b.WriteString("- " + m.Summary + "\n")
	}
	return b.String()
}

// renderReview writes the review as markdown, for people and pull requests.
func renderReview(u db.Unit, m ReviewMeta) string {
	if m.By != "" {
		return fmt.Sprintf("# Changes requested by %s\n\n%s\n\n---\n%s · requested in tfy\n", m.By, strings.TrimSpace(m.Summary), domain.Label(u.Seq))
	}
	var b strings.Builder
	verdict := "✅ Approved"
	if m.Decision != "approve" {
		verdict = "🔁 Changes requested"
	}
	fmt.Fprintf(&b, "# Review round %d — %s\n\n%s\n\n", m.Round, verdict, m.Summary)
	texts := map[string]string{}
	for _, c := range m.Spec {
		texts[c.ID] = c.Text
	}
	if len(m.Criteria) > 0 {
		b.WriteString("## Acceptance criteria\n\n| | Criterion | Status | Evidence |\n|---|---|---|---|\n")
		for _, c := range m.Criteria {
			mark := map[string]string{"met": "✅", "partial": "🟡", "unmet": "❌", "not_verifiable": "❔"}[c.Status]
			fmt.Fprintf(&b, "| %s | **%s** %s | %s | %s |\n", mark, c.ID, cell(texts[c.ID]), strings.ReplaceAll(c.Status, "_", " "), cell(c.Evidence))
		}
		b.WriteString("\n")
	}
	if len(m.Findings) > 0 {
		b.WriteString("## Findings\n\n")
		for _, f := range m.Findings {
			loc := f.File
			if f.Line > 0 {
				loc += fmt.Sprintf(":%d", f.Line)
			}
			fmt.Fprintf(&b, "- **%s** `%s` %s — %s\n", f.Severity, f.Repo, loc, f.Message)
		}
		b.WriteString("\n")
	}
	fmt.Fprintf(&b, "---\n%s · reviewed by tfy\n", domain.Label(u.Seq))
	return b.String()
}

func cell(s string) string {
	return strings.ReplaceAll(strings.ReplaceAll(s, "|", "\\|"), "\n", " ")
}

// postReview comments the review on each pull request, when the project
// asks for it. Failures are logged, not fatal.
func (p *Pipeline) postReview(ctx context.Context, u db.Unit, targets []db.ListUnitReposRow, body string) {
	project, err := p.Store.Q.GetProject(ctx, u.ProjectID)
	if err != nil || !domain.ParseProjectSettings(project.Settings).PostReviewToGitHub {
		return
	}
	f, err := os.CreateTemp("", "tfy-review-*.md")
	if err != nil {
		return
	}
	defer os.Remove(f.Name())
	_, _ = f.WriteString(body)
	f.Close()
	for _, ur := range targets {
		if ur.PrNumber == 0 {
			continue
		}
		if err := p.GH.PRComment(ctx, ur.FullName, int(ur.PrNumber), f.Name()); err != nil {
			p.Log.Warn("post review", "repo", ur.FullName, "error", err)
		}
	}
}

// latestReview returns the most recent review's structured output.
func (p *Pipeline) latestReview(ctx context.Context, u db.Unit) (ReviewMeta, bool) {
	doc, err := p.Store.Q.LatestDocument(ctx, db.LatestDocumentParams{UnitID: u.ID, Kind: DocReview})
	if err != nil {
		return ReviewMeta{}, false
	}
	var m ReviewMeta
	return m, json.Unmarshal([]byte(doc.Meta), &m) == nil
}

// lastTestReport summarizes what the last development run said about its
// tests, for the reviewer, who cannot run them.
func (p *Pipeline) lastTestReport(ctx context.Context, u db.Unit) string {
	var lines []string
	for _, r := range p.developReport(ctx, u).Repos {
		switch {
		case !r.TestsRun:
			lines = append(lines, r.Repo+": tests not run")
		case r.TestsPassed:
			lines = append(lines, r.Repo+": tests ran and passed")
		default:
			lines = append(lines, r.Repo+": tests ran and FAILED")
		}
		if r.Notes != "" {
			lines[len(lines)-1] += " (" + r.Notes + ")"
		}
	}
	return strings.Join(lines, "; ")
}
