package pipeline

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/raulsh/thefactory/internal/domain"
	"github.com/raulsh/thefactory/internal/prompts"
	"github.com/raulsh/thefactory/internal/store"
	"github.com/raulsh/thefactory/internal/store/db"
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
	Decision string              `json:"decision"` // approve | request_changes, after thefactory's own rules
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

// decide turns the review into thefactory's decision. Only concrete
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
	targets := targetsOf(urs)
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
		data.Diffs = append(data.Diffs, prompts.ReviewDiff{Dir: dirOf(ur), FullName: ur.FullName, Branch: ur.Branch, File: file, Commits: commits, Checks: checks})
		reviewed[ur.FullName] = head
		if err := p.Store.Q.SetUnitRepoReviewed(ctx, db.SetUnitRepoReviewedParams{ReviewedSha: head, Now: store.Now(), UnitID: u.ID, RepoID: ur.RepoID}); err != nil {
			return err
		}
	}
	if len(data.Diffs) == 0 {
		return fmt.Errorf("there is no pull request to review")
	}

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
	metaJSON, _ := json.Marshal(meta)
	doc, err := p.saveDoc(ctx, u, DocReview, renderReview(u, meta), string(metaJSON), "claude", run.ID)
	if err != nil {
		return err
	}
	p.postReview(ctx, u, targets, doc.Content)

	if meta.Decision == "approve" {
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
	return p.sendBack(ctx, u, meta, "claude")
}

// sendBack starts another development round with the review's findings.
func (p *Pipeline) sendBack(ctx context.Context, u db.Unit, meta ReviewMeta, actor string) error {
	if err := p.Store.Q.SetUnitReviewIteration(ctx, db.SetUnitReviewIterationParams{ReviewIteration: u.ReviewIteration + 1, Now: store.Now(), ID: u.ID}); err != nil {
		return err
	}
	u, err := p.transition(ctx, u, domain.StateDeveloping, actor, fmt.Sprintf("review round %d requested changes", meta.Round))
	if err != nil {
		return err
	}
	return p.enqueue(ctx, JobDevelop, u, developPayload{Findings: findingsBrief(meta)})
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
	fmt.Fprintf(&b, "---\n%s · reviewed by thefactory\n", domain.Label(u.Seq))
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
	f, err := os.CreateTemp("", "thefactory-review-*.md")
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
	run, err := p.Store.Q.LastSessionRun(ctx, db.LastSessionRunParams{UnitID: store.NullString(u.ID), Kind: "develop"})
	if err != nil {
		return ""
	}
	var out struct {
		Repos []struct {
			Repo        string `json:"repo"`
			Changed     bool   `json:"changed"`
			TestsRun    bool   `json:"tests_run"`
			TestsPassed bool   `json:"tests_passed"`
			Notes       string `json:"notes"`
		} `json:"repos"`
	}
	if json.Unmarshal([]byte(run.Result), &out) != nil {
		return ""
	}
	var lines []string
	for _, r := range out.Repos {
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
