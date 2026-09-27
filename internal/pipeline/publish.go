package pipeline

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"strconv"
	"strings"

	"github.com/raulsh/thefactory/internal/domain"
	"github.com/raulsh/thefactory/internal/store"
	"github.com/raulsh/thefactory/internal/store/db"
)

// Publish states of a unit's repository.
const (
	PublishPending = "pending"
	PublishPushed  = "pushed"
	PublishPROpen  = "pr_open"
)

// publish pushes each changed target branch and opens its pull request. It
// is idempotent: pushing the same commits again is a no-op, and an existing
// pull request for the branch is reused rather than duplicated.
func (p *Pipeline) publish(ctx context.Context, job db.Job, u db.Unit) error {
	urs, err := p.Store.Q.ListUnitRepos(ctx, u.ID)
	if err != nil {
		return err
	}
	targets := targetsOf(urs)
	project, err := p.Store.Q.GetProject(ctx, u.ProjectID)
	if err != nil {
		return err
	}
	settings := domain.ParseProjectSettings(project.Settings)
	var spec SpecMeta
	if doc, err := p.Store.Q.LatestDocument(ctx, db.LatestDocumentParams{UnitID: u.ID, Kind: DocSpec}); err == nil {
		_ = json.Unmarshal([]byte(doc.Meta), &spec)
	}

	var published []db.ListUnitReposRow
	for _, ur := range targets {
		if ur.Branch == "" {
			return fmt.Errorf("%s has no branch; develop did not run", ur.FullName)
		}
		if _, err := p.Git.CommitAll(ctx, ur.CheckoutPath, fmt.Sprintf("%s: %s\n\nUncommitted changes left by the development run.", u.Kind, u.Title)); err != nil {
			return fmt.Errorf("commit leftovers in %s: %w", ur.FullName, err)
		}
		ahead, err := p.Git.Ahead(ctx, ur.CheckoutPath, ur.BaseSha, "HEAD")
		if err != nil {
			return err
		}
		if ahead == 0 {
			p.activity(ctx, u.ID, "system", "publish", ur.FullName+": no changes to publish", nil)
			continue
		}
		head, err := p.Git.RevParse(ctx, ur.CheckoutPath, "HEAD")
		if err != nil {
			return err
		}
		if err := p.push(ctx, ur); err != nil {
			return err
		}
		if err := p.Store.Q.SetUnitRepoPublish(ctx, db.SetUnitRepoPublishParams{
			PublishState: PublishPushed, HeadSha: head, PrNumber: ur.PrNumber, PrUrl: ur.PrUrl, PrState: ur.PrState,
			Now: store.Now(), UnitID: u.ID, RepoID: ur.RepoID,
		}); err != nil {
			return err
		}
		number, url, err := p.ensurePR(ctx, u, ur, spec, settings.DraftPRs, nil)
		if err != nil {
			return err
		}
		if err := p.Store.Q.SetUnitRepoPublish(ctx, db.SetUnitRepoPublishParams{
			PublishState: PublishPROpen, HeadSha: head, PrNumber: int64(number), PrUrl: url, PrState: "open",
			Now: store.Now(), UnitID: u.ID, RepoID: ur.RepoID,
		}); err != nil {
			return err
		}
		ur.PrNumber, ur.PrUrl = int64(number), url
		published = append(published, ur)
		p.activity(ctx, u.ID, "system", "pr", fmt.Sprintf("%s: pull request #%d", ur.FullName, number), map[string]string{"url": url})
		p.changed("unit", u.ID)
	}

	if len(published) == 0 {
		p.flag(ctx, u.ID, domain.AttentionNoChanges, "the development run committed nothing in the target repositories")
		return &stopError{fmt.Errorf("nothing to publish")}
	}
	if len(published) > 1 {
		// Cross-link the pull requests of a multi-repo change.
		for _, ur := range published {
			if _, _, err := p.ensurePR(ctx, u, ur, spec, settings.DraftPRs, published); err != nil {
				p.Log.Warn("link sibling pull requests", "unit", u.ID, "repo", ur.FullName, "error", err)
			}
		}
	}
	if u, err = p.transition(ctx, u, domain.StateReviewing, "system", fmt.Sprintf("%d pull request(s) open", len(published))); err != nil {
		return err
	}
	return p.enqueue(ctx, JobReview, u, nil)
}

// push publishes the checkout's HEAD with the user's credentials: through
// gh's credential helper for HTTPS remotes, the user's SSH setup otherwise.
func (p *Pipeline) push(ctx context.Context, ur db.ListUnitReposRow) error {
	repo, err := p.Store.Q.GetRepo(ctx, ur.RepoID)
	if err != nil {
		return err
	}
	if repo.CloneUrl == "" {
		if repo, err = p.syncManagedClone(ctx, repo); err != nil {
			return err
		}
	}
	if err := p.Git.PushBranchWith(ctx, ur.CheckoutPath, repo.CloneUrl, ur.Branch, p.credentialArgs(repo.CloneUrl)); err != nil {
		return fmt.Errorf("push %s to %s: %w", ur.Branch, repo.FullName, err)
	}
	return nil
}

func (p *Pipeline) credentialArgs(url string) []string {
	if !strings.HasPrefix(url, "https://") {
		return nil
	}
	gh := p.Config.GHBin
	if gh == "" {
		gh = "gh"
	}
	return []string{"-c", "credential.helper=", "-c", "credential.helper=!" + gh + " auth git-credential"}
}

// ensurePR returns the branch's pull request, opening it if there is none.
// With siblings set, it rewrites the description to link them.
func (p *Pipeline) ensurePR(ctx context.Context, u db.Unit, ur db.ListUnitReposRow, spec SpecMeta, draft bool, siblings []db.ListUnitReposRow) (int, string, error) {
	body, err := os.CreateTemp("", "thefactory-pr-*.md")
	if err != nil {
		return 0, "", err
	}
	defer os.Remove(body.Name())
	if _, err := body.WriteString(prBody(u, ur, spec, siblings)); err != nil {
		return 0, "", err
	}
	body.Close()

	existing, err := p.GH.PRForBranch(ctx, ur.FullName, ur.Branch)
	if err != nil {
		return 0, "", err
	}
	if existing != nil {
		if siblings != nil || existing.State == "OPEN" {
			if err := p.GH.PREditBody(ctx, ur.FullName, existing.Number, body.Name()); err != nil {
				return 0, "", err
			}
		}
		return existing.Number, existing.URL, nil
	}
	url, err := p.GH.PRCreate(ctx, ur.FullName, ur.DefaultBranch, ur.Branch, u.Title, body.Name(), draft)
	if err != nil {
		return 0, "", err
	}
	return prNumber(url), url, nil
}

func prNumber(url string) int {
	i := strings.LastIndex(url, "/")
	n, _ := strconv.Atoi(url[i+1:])
	return n
}

func prBody(u db.Unit, ur db.ListUnitReposRow, spec SpecMeta, siblings []db.ListUnitReposRow) string {
	var b strings.Builder
	summary := spec.Summary
	if summary == "" {
		summary = u.Summary
	}
	if summary != "" {
		b.WriteString(summary + "\n\n")
	}
	if len(spec.AcceptanceCriteria) > 0 {
		b.WriteString("### Acceptance criteria\n\n")
		for _, c := range spec.AcceptanceCriteria {
			fmt.Fprintf(&b, "- [ ] **%s** %s\n", c.ID, c.Text)
		}
		b.WriteString("\n")
	}
	var others []string
	for _, s := range siblings {
		if s.RepoID != ur.RepoID && s.PrUrl != "" {
			others = append(others, fmt.Sprintf("- %s: %s", s.FullName, s.PrUrl))
		}
	}
	if len(others) > 0 {
		b.WriteString("### Part of a change across repositories\n\n" + strings.Join(others, "\n") + "\n\n")
	}
	fmt.Fprintf(&b, "---\n%s · %s · opened by thefactory\n", domain.Label(u.Seq), u.Kind)
	return b.String()
}
