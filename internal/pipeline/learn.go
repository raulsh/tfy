package pipeline

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/raulsh/tfy/internal/claude"
	"github.com/raulsh/tfy/internal/domain"
	"github.com/raulsh/tfy/internal/jobs"
	"github.com/raulsh/tfy/internal/prompts"
	"github.com/raulsh/tfy/internal/store"
	"github.com/raulsh/tfy/internal/store/db"
)

// A retrospective looks back at a finished unit — review rounds, what people
// asked to change, comments on the pull requests, refused commands, failed
// CI — and asks whether the repositories' conventions for Claude should
// change. When they should, it opens a unit that proposes the change; it
// goes through the pipeline like any other, so a person decides, and the
// change lands as a reviewed pull request in the repository. That is how
// CLAUDE.md, .claude/rules and hooks improve unit after unit.

// LearnMeta is the retrospective's structured output, stored with its
// document, plus the unit it opened.
type LearnMeta struct {
	Summary         string        `json:"summary"`
	Title           string        `json:"title"`
	Changes         []LearnChange `json:"changes"`
	SuggestedUnitID string        `json:"suggested_unit_id,omitempty"`
	SuggestedUnit   string        `json:"suggested_unit,omitempty"`
}

// LearnChange is one proposed change to a repository's conventions.
type LearnChange struct {
	Repo   string `json:"repo"`
	File   string `json:"file"`
	Kind   string `json:"kind"`
	Change string `json:"change"`
	Why    string `json:"why"`
}

// afterDone reads the unit's linked issues again, and starts the
// retrospective of a unit that just finished when its project learns from
// units. Units that change conventions are not looked
// back at, so one change cannot beget another.
func (p *Pipeline) afterDone(ctx context.Context, u db.Unit) {
	// Merging may have closed the linked issues.
	p.unitIssues(ctx, u, true)
	if u.Origin == string(domain.OriginRetrospective) {
		return
	}
	project, err := p.Store.Q.GetProject(ctx, u.ProjectID)
	if err != nil || !domain.ParseProjectSettings(project.Settings).LearnFromUnits {
		return
	}
	if err := p.enqueue(ctx, JobLearn, u, nil); err != nil {
		p.Log.Warn("start the retrospective", "unit", domain.Label(u.Seq), "error", err)
	}
}

// learn runs a finished unit's retrospective. A done unit has nothing left
// to flag, so failures are recorded in its activity rather than as
// attention.
func (p *Pipeline) learn(ctx context.Context, job db.Job, u db.Unit) error {
	if _, err := os.Stat(u.WorkspacePath); errors.Is(err, fs.ErrNotExist) {
		p.activity(ctx, u.ID, "system", "retrospective", "retrospective skipped: the workspace is gone", nil)
		return &stopError{err}
	}
	urs, err := p.Store.Q.ListUnitRepos(ctx, u.ID)
	if err != nil {
		return err
	}
	var checkouts []db.ListUnitReposRow
	for _, ur := range urs {
		if _, err := os.Stat(ur.CheckoutPath); err == nil {
			checkouts = append(checkouts, ur)
		}
	}
	data := p.learnData(ctx, u, checkouts)
	req := runRequest{Unit: u, Kind: "learn", Schema: prompts.Schema("learn")}
	if req.Prompt, req.PromptVersion, err = prompts.Render("learn", data); err != nil {
		return err
	}
	if req.System, err = systemPrompt(checkouts); err != nil {
		return err
	}
	run, out, err := p.runClaude(ctx, req)
	if err != nil {
		return err
	}
	if out.Status == claude.StatusRateLimited {
		until := time.Now().Add(15 * time.Minute)
		if rl := out.RateLimit; rl != nil && rl.ResetsAt > 0 {
			until = time.Unix(rl.ResetsAt, 0)
		}
		p.Jobs.PauseClaude(until)
		return &jobs.RetryError{After: time.Until(until) + time.Minute, Err: fmt.Errorf("rate limited")}
	}
	if out.Status != claude.StatusSucceeded {
		p.activity(ctx, u.ID, "system", "retrospective", fmt.Sprintf("retrospective %s: %s", strings.ReplaceAll(string(out.Status), "_", " "), out.Reason), map[string]string{"run_id": run.ID})
		return &stopError{fmt.Errorf("learn run %s", out.Status)}
	}
	var meta LearnMeta
	if err := json.Unmarshal([]byte(run.Result), &meta); err != nil {
		p.activity(ctx, u.ID, "system", "retrospective", "the retrospective returned nothing usable", map[string]string{"run_id": run.ID})
		return &stopError{err}
	}

	if len(meta.Changes) > 0 {
		child, err := p.newUnit(ctx, unitSpec{
			ProjectID:    u.ProjectID,
			Kind:         domain.KindChore,
			Title:        learnTitle(meta, u),
			Summary:      meta.Summary,
			Description:  learnDescription(meta, u),
			Origin:       domain.OriginRetrospective,
			State:        domain.StateProposed,
			CreatedBy:    "claude",
			ParentUnitID: u.ID,
		})
		if err != nil {
			return err
		}
		meta.SuggestedUnitID, meta.SuggestedUnit = child.ID, domain.Label(child.Seq)
	}
	raw, _ := json.Marshal(meta)
	if _, err := p.saveDoc(ctx, u, DocRetrospective, learnDocument(meta), string(raw), "claude", run.ID); err != nil {
		return err
	}
	msg := "retrospective: the conventions need no change"
	if meta.SuggestedUnit != "" {
		msg = fmt.Sprintf("retrospective: %s proposes %d change(s) to the conventions", meta.SuggestedUnit, len(meta.Changes))
	}
	p.activity(ctx, u.ID, "claude", "retrospective", msg, map[string]string{"run_id": run.ID, "unit_id": meta.SuggestedUnitID})
	p.changed("unit", u.ID)
	return nil
}

// learnData gathers what the retrospective looks at.
func (p *Pipeline) learnData(ctx context.Context, u db.Unit, checkouts []db.ListUnitReposRow) prompts.Learn {
	data := prompts.Learn{
		Label: domain.Label(u.Seq), Title: u.Title, Kind: u.Kind, Summary: u.Summary,
		Repos: promptRepos(checkouts, ""), ReviewRounds: int(u.ReviewIteration),
	}
	urs, _ := p.Store.Q.ListUnitRepos(ctx, u.ID)
	for _, ur := range targetsOf(urs) {
		if ur.PrNumber == 0 {
			continue
		}
		pr := fmt.Sprintf("%s#%d (%s", ur.FullName, ur.PrNumber, orDash(ur.PrState))
		if ur.ChecksState != "" {
			pr += ", checks " + ur.ChecksState
		}
		if ur.ReleaseState != "" {
			pr += ", CI after merge " + ur.ReleaseState
		}
		data.PRs = append(data.PRs, pr+")")
		comments, err := p.GH.PRDiscussion(ctx, ur.FullName, int(ur.PrNumber))
		if err != nil {
			p.Log.Warn("read the pull request discussion", "repo", ur.FullName, "pr", ur.PrNumber, "error", err)
		}
		for _, c := range comments {
			where := fmt.Sprintf("%s#%d %s", ur.FullName, ur.PrNumber, c.Kind)
			if c.Path != "" {
				where += " on " + c.Path
			}
			if c.State != "" && c.State != "COMMENTED" {
				where += " (" + strings.ToLower(c.State) + ")"
			}
			data.Comments = append(data.Comments, prompts.LearnNote{Author: c.Author, Where: where, Text: excerpt(c.Body, 2000)})
		}
	}

	versions, _ := p.Store.Q.ListDocumentVersions(ctx, db.ListDocumentVersionsParams{UnitID: u.ID, Kind: DocReview})
	for i := len(versions) - 1; i >= 0; i-- {
		doc, err := p.Store.Q.GetDocumentVersion(ctx, db.GetDocumentVersionParams{UnitID: u.ID, Kind: DocReview, Version: versions[i].Version})
		if err != nil {
			continue
		}
		var m ReviewMeta
		if json.Unmarshal([]byte(doc.Meta), &m) != nil {
			continue
		}
		r := prompts.LearnReview{Round: m.Round, Decision: strings.ReplaceAll(orDash(m.Decision), "_", " ")}
		for _, c := range m.Criteria {
			if c.Status == "unmet" || c.Status == "partial" {
				r.Unmet = append(r.Unmet, fmt.Sprintf("%s %s: %s", c.ID, c.Status, excerpt(c.Evidence, 400)))
			}
		}
		for _, f := range m.Findings {
			r.Findings = append(r.Findings, fmt.Sprintf("[%s] %s %s: %s", f.Severity, f.Repo, f.File, excerpt(f.Message, 600)))
		}
		data.Reviews = append(data.Reviews, r)
	}

	for _, kind := range []string{DocRequirement, DocSpec} {
		versions, _ := p.Store.Q.ListDocumentVersions(ctx, db.ListDocumentVersionsParams{UnitID: u.ID, Kind: kind})
		for _, v := range versions {
			if v.Author != "claude" {
				data.EditedDocs = append(data.EditedDocs, fmt.Sprintf("%s v%d", kind, v.Version))
			}
		}
	}
	acts, _ := p.Store.Q.ListUnitActivity(ctx, db.ListUnitActivityParams{UnitID: store.NullString(u.ID), Lim: 500})
	for i := len(acts) - 1; i >= 0; i-- {
		if a := acts[i]; a.Kind == "feedback" && strings.TrimSpace(a.Message) != "" {
			data.Feedback = append(data.Feedback, prompts.LearnNote{Author: a.Actor, Where: "tfy", Text: excerpt(a.Message, 2000)})
		}
	}

	denials, _ := p.Store.Q.ListUnitDenials(ctx, db.ListUnitDenialsParams{UnitID: store.NullString(u.ID), Lim: 30})
	for _, d := range denials {
		data.Denials = append(data.Denials, denialLine(d))
	}
	children, _ := p.Store.Q.ListChildUnits(ctx, store.NullString(u.ID))
	for _, c := range children {
		if c.Origin == string(domain.OriginFollowUp) {
			data.FollowUps = append(data.FollowUps, domain.Label(c.Seq)+": "+c.Title)
		}
	}
	return data
}

// denialLine describes one refused command for the retrospective.
func denialLine(d db.ListUnitDenialsRow) string {
	var payload struct {
		ToolName string `json:"tool_name"`
		Message  string `json:"message"`
		Stderr   string `json:"stderr"`
	}
	_ = json.Unmarshal([]byte(d.Payload), &payload)
	if d.Subtype == "hook_response" {
		return fmt.Sprintf("%s run, the guard: %s", d.RunKind, excerpt(strings.TrimSpace(payload.Stderr), 300))
	}
	what := d.Summary
	if what == "" {
		what = payload.Message
	}
	return fmt.Sprintf("%s run, permissions: %s %s", d.RunKind, orDash(payload.ToolName), excerpt(what, 300))
}

func learnTitle(m LearnMeta, u db.Unit) string {
	if t := strings.TrimSpace(m.Title); t != "" {
		return t
	}
	return "Update the Claude conventions after " + domain.Label(u.Seq)
}

// learnDescription is the proposal as the new unit's description: what to
// change in which file, and why.
func learnDescription(m LearnMeta, u db.Unit) string {
	var b strings.Builder
	fmt.Fprintf(&b, "The retrospective of %s (%s) proposes these changes to the repositories' conventions for Claude.\n", domain.Label(u.Seq), u.Title)
	if s := strings.TrimSpace(m.Summary); s != "" {
		b.WriteString("\n" + s + "\n")
	}
	for i, c := range m.Changes {
		fmt.Fprintf(&b, "\n### %d. %s: %s (%s)\n\n%s\n\nWhy: %s\n", i+1, c.Repo, c.File, c.Kind, strings.TrimSpace(c.Change), strings.TrimSpace(c.Why))
	}
	return b.String()
}

// learnDocument is the retrospective as the finished unit shows it.
func learnDocument(m LearnMeta) string {
	var b strings.Builder
	b.WriteString("## Retrospective\n\n")
	if s := strings.TrimSpace(m.Summary); s != "" {
		b.WriteString(s + "\n\n")
	}
	if len(m.Changes) == 0 {
		b.WriteString("The conventions need no change.\n")
		return b.String()
	}
	fmt.Fprintf(&b, "Proposed in %s:\n\n", m.SuggestedUnit)
	for _, c := range m.Changes {
		fmt.Fprintf(&b, "- **%s: %s** (%s). %s\n", c.Repo, c.File, c.Kind, strings.TrimSpace(c.Why))
	}
	return b.String()
}

// excerpt shortens s to about n bytes, keeping its lines.
func excerpt(s string, n int) string {
	s = strings.TrimSpace(s)
	if len(s) <= n {
		return s
	}
	cut := s[:n]
	for !utf8.ValidString(cut) {
		cut = cut[:len(cut)-1]
	}
	return cut + "…"
}

func orDash(s string) string {
	if s == "" {
		return "-"
	}
	return s
}
