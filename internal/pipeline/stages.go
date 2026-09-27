package pipeline

import (
	"context"
	"encoding/json"
	"fmt"
	"slices"
	"strings"

	"github.com/raulsh/thefactory/internal/domain"
	"github.com/raulsh/thefactory/internal/prompts"
	"github.com/raulsh/thefactory/internal/store"
	"github.com/raulsh/thefactory/internal/store/db"
)

type definePayload struct {
	Revision string `json:"revision,omitempty"`
}

type planPayload struct {
	Revision string `json:"revision,omitempty"`
}

type developPayload struct {
	Findings string `json:"findings,omitempty"`
	Retry    bool   `json:"retry,omitempty"`
}

// defineMeta is the define run's structured output.
type defineMeta struct {
	Title         string   `json:"title"`
	Kind          string   `json:"kind"`
	Summary       string   `json:"summary"`
	OpenQuestions []string `json:"open_questions"`
}

// SpecMeta is the plan run's structured output, stored with the spec.
type SpecMeta struct {
	Summary            string              `json:"summary"`
	TargetRepos        []string            `json:"target_repos"`
	AcceptanceCriteria []prompts.Criterion `json:"acceptance_criteria"`
	NewDependencies    []string            `json:"new_dependencies"`
}

func decodePayload[T any](job db.Job) T {
	var v T
	_ = json.Unmarshal([]byte(job.Payload), &v)
	return v
}

// define writes (or revises) the requirement.
func (p *Pipeline) define(ctx context.Context, job db.Job, u db.Unit) error {
	payload := decodePayload[definePayload](job)
	project, err := p.Store.Q.GetProject(ctx, u.ProjectID)
	if err != nil {
		return err
	}
	data := prompts.Define{
		Label:          domain.Label(u.Seq),
		Project:        project.Name,
		About:          project.Description,
		ProductContext: project.ProductContext,
		Kind:           u.Kind,
		Title:          u.Title,
		Description:    u.Description,
		Feedback:       p.unitFeedback(ctx, u),
		Revision:       payload.Revision,
		EditedByUser:   p.editedByUser(ctx, u, DocRequirement),
	}
	req := runRequest{Unit: u, Kind: "define", Schema: prompts.Schema("define")}
	if payload.Revision != "" {
		if prev, err := p.Store.Q.LastSessionRun(ctx, db.LastSessionRunParams{UnitID: store.NullString(u.ID), Kind: "define"}); err == nil {
			req.Resume = &prev
		}
	}
	if req.Prompt, req.PromptVersion, err = prompts.Render("define", data); err != nil {
		return err
	}
	// Define runs have no checkouts; the system prompt names none.
	if req.System, err = systemPrompt(nil); err != nil {
		return err
	}
	run, out, err := p.runClaude(ctx, req)
	if err != nil {
		return err
	}
	if out.Status != "succeeded" {
		return p.runFailed(ctx, u, run, out)
	}
	var meta defineMeta
	_ = json.Unmarshal([]byte(run.Result), &meta)
	if _, err := p.snapshotDoc(ctx, u, DocRequirement, run.Result, run.ID); err != nil {
		return err
	}
	title, kind := u.Title, u.Kind
	if u.Origin != string(domain.OriginDeveloper) && strings.TrimSpace(meta.Title) != "" {
		title = meta.Title
	}
	if k, err := domain.ParseKind(meta.Kind); err == nil && meta.Kind != "" && u.Origin != string(domain.OriginDeveloper) {
		kind = string(k)
	}
	summary := strings.TrimSpace(meta.Summary)
	if summary == "" {
		summary = u.Summary
	}
	if err := p.Store.Q.UpdateUnitDetails(ctx, db.UpdateUnitDetailsParams{Title: title, Kind: kind, Summary: summary, Now: store.Now(), ID: u.ID}); err != nil {
		return err
	}
	_, err = p.transition(ctx, u, domain.StateDefinitionReview, "claude", "requirement drafted")
	return err
}

// plan explores the repositories and writes (or revises) the spec.
func (p *Pipeline) plan(ctx context.Context, job db.Job, u db.Unit) error {
	payload := decodePayload[planPayload](job)
	urs, err := p.ensureCheckouts(ctx, u)
	if err != nil {
		return err
	}
	data := prompts.Plan{
		Label:        domain.Label(u.Seq),
		Title:        u.Title,
		Repos:        promptRepos(urs, ""),
		Revision:     payload.Revision,
		EditedByUser: p.editedByUser(ctx, u, DocSpec),
	}
	req := runRequest{Unit: u, Kind: "plan", Schema: prompts.Schema("plan")}
	if payload.Revision != "" {
		if prev, err := p.Store.Q.LastSessionRun(ctx, db.LastSessionRunParams{UnitID: store.NullString(u.ID), Kind: "plan"}); err == nil {
			req.Resume = &prev
		}
	}
	if req.Prompt, req.PromptVersion, err = prompts.Render("plan", data); err != nil {
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
	var meta SpecMeta
	if err := json.Unmarshal([]byte(run.Result), &meta); err != nil {
		return fmt.Errorf("the plan run returned no usable structured output: %w", err)
	}
	if _, err := p.snapshotDoc(ctx, u, DocSpec, run.Result, run.ID); err != nil {
		return err
	}
	if err := p.markTargets(ctx, u, urs, meta.TargetRepos); err != nil {
		return err
	}
	_, err = p.transition(ctx, u, domain.StateSpecReview, "claude", "specification drafted")
	return err
}

// markTargets flags the checkouts the spec says must change. Names may be
// directory names or owner/name.
func (p *Pipeline) markTargets(ctx context.Context, u db.Unit, urs []db.ListUnitReposRow, targets []string) error {
	matched := 0
	for _, ur := range urs {
		is := slices.ContainsFunc(targets, func(t string) bool {
			t = strings.TrimSuffix(strings.TrimSpace(t), "/")
			return strings.EqualFold(t, dirOf(ur)) || strings.EqualFold(t, ur.FullName)
		})
		if is {
			matched++
		}
		if err := p.Store.Q.SetUnitRepoTarget(ctx, db.SetUnitRepoTargetParams{IsTarget: is, Now: store.Now(), UnitID: u.ID, RepoID: ur.RepoID}); err != nil {
			return err
		}
	}
	if matched == 0 && len(urs) == 1 {
		return p.Store.Q.SetUnitRepoTarget(ctx, db.SetUnitRepoTargetParams{IsTarget: true, Now: store.Now(), UnitID: u.ID, RepoID: urs[0].RepoID})
	}
	if matched == 0 {
		return fmt.Errorf("the spec names no known target repository (got %v)", targets)
	}
	return nil
}

func targetsOf(urs []db.ListUnitReposRow) []db.ListUnitReposRow {
	var out []db.ListUnitReposRow
	for _, ur := range urs {
		if ur.IsTarget {
			out = append(out, ur)
		}
	}
	return out
}

// develop puts each target checkout on the unit's branch and lets Claude
// implement the spec. It commits locally; publish pushes.
func (p *Pipeline) develop(ctx context.Context, job db.Job, u db.Unit) error {
	payload := decodePayload[developPayload](job)
	urs, err := p.ensureCheckouts(ctx, u)
	if err != nil {
		return err
	}
	targets := targetsOf(urs)
	if len(targets) == 0 {
		return fmt.Errorf("the spec marks no repository to change")
	}
	spec, err := p.Store.Q.LatestDocument(ctx, db.LatestDocumentParams{UnitID: u.ID, Kind: DocSpec})
	if err != nil {
		return fmt.Errorf("there is no approved spec")
	}
	var meta SpecMeta
	_ = json.Unmarshal([]byte(spec.Meta), &meta)
	// The spec file is what the agent reads; keep it in step with the
	// approved version.
	if err := writeDoc(u, DocSpec, spec.Content); err != nil {
		return err
	}

	branch := domain.BranchName(u.Seq, u.Title)
	for _, ur := range targets {
		if err := p.prepareBranch(ctx, u, ur, branch); err != nil {
			return err
		}
	}

	data := prompts.Develop{
		Label:           domain.Label(u.Seq),
		Title:           u.Title,
		Branch:          branch,
		Targets:         promptRepos(targets, branch),
		Criteria:        meta.AcceptanceCriteria,
		NewDependencies: meta.NewDependencies,
		Findings:        payload.Findings,
	}
	req := runRequest{Unit: u, Kind: "develop", Schema: prompts.Schema("develop")}
	for _, ur := range targets {
		req.Trusted = append(req.Trusted, dirOf(ur))
	}
	if payload.Findings != "" {
		if prev, err := p.Store.Q.LastSessionRun(ctx, db.LastSessionRunParams{UnitID: store.NullString(u.ID), Kind: "develop"}); err == nil {
			req.Resume = &prev
		}
	}
	if req.Prompt, req.PromptVersion, err = prompts.Render("develop", data); err != nil {
		return err
	}
	if payload.Retry {
		req.Prompt += "\nAn earlier attempt was interrupted, so the branch may already hold part of the work. Check `git -C <dir> log base/<branch>..HEAD` and `git -C <dir> status` first, and continue from there.\n"
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
	if u, err = p.transition(ctx, u, domain.StatePublishing, "claude", "implementation committed"); err != nil {
		return err
	}
	return p.enqueue(ctx, JobPublish, u, nil)
}

// prepareBranch refreshes base/<default> from the managed clone and checks
// out the unit's branch, creating it at the current commit the first time.
func (p *Pipeline) prepareBranch(ctx context.Context, u db.Unit, ur db.ListUnitReposRow, branch string) error {
	repo, err := p.Store.Q.GetRepo(ctx, ur.RepoID)
	if err != nil {
		return err
	}
	if repo, err = p.syncManagedClone(ctx, repo); err != nil {
		return err
	}
	if err := p.Git.FetchInto(ctx, ur.CheckoutPath, repo.ClonePath, "refs/heads/"+repo.DefaultBranch, "refs/remotes/base/"+repo.DefaultBranch); err != nil {
		return fmt.Errorf("refresh base of %s: %w", repo.FullName, err)
	}
	if err := p.Git.SwitchBranch(ctx, ur.CheckoutPath, branch, "HEAD"); err != nil {
		return fmt.Errorf("check out %s in %s: %w", branch, repo.FullName, err)
	}
	return p.Store.Q.SetUnitRepoBranch(ctx, db.SetUnitRepoBranchParams{Branch: branch, Now: store.Now(), UnitID: u.ID, RepoID: ur.RepoID})
}
