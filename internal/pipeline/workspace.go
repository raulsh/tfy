package pipeline

import (
	"context"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path"
	"path/filepath"
	"strings"
	"sync"

	"github.com/raulsh/thefactory/internal/domain"
	"github.com/raulsh/thefactory/internal/prompts"
	"github.com/raulsh/thefactory/internal/store"
	"github.com/raulsh/thefactory/internal/store/db"
)

// Document kinds and the files they live in, relative to the workspace.
const (
	DocRequirement  = "requirement"
	DocSpec         = "spec"
	DocReview       = "review"
	DocReleaseNotes = "release_notes"
)

var docFiles = map[string]string{
	DocRequirement: "docs/requirement.md",
	DocSpec:        "docs/spec.md",
}

// DocKinds lists the document kinds a unit can have.
var DocKinds = []string{DocRequirement, DocSpec, DocReview, DocReleaseNotes}

func (p *Pipeline) repoLock(fullName string) *sync.Mutex {
	m, _ := p.repoLocks.LoadOrStore(fullName, &sync.Mutex{})
	return m.(*sync.Mutex)
}

// syncManagedClone makes sure the fetch-only bare clone of repo exists and is
// current, and returns the refreshed repo row.
func (p *Pipeline) syncManagedClone(ctx context.Context, repo db.Repo) (db.Repo, error) {
	mu := p.repoLock(repo.FullName)
	mu.Lock()
	defer mu.Unlock()

	clonePath := p.Paths.ManagedClone(repo.FullName)
	if _, err := os.Stat(filepath.Join(clonePath, "HEAD")); errors.Is(err, fs.ErrNotExist) {
		if err := os.MkdirAll(filepath.Dir(clonePath), 0o755); err != nil {
			return repo, err
		}
		if err := p.GH.CloneBare(ctx, repo.FullName, clonePath); err != nil {
			return repo, fmt.Errorf("clone %s: %w", repo.FullName, err)
		}
	}
	if err := p.Git.Fetch(ctx, clonePath); err != nil {
		return repo, fmt.Errorf("fetch %s: %w", repo.FullName, err)
	}
	url, err := p.Git.RemoteURL(ctx, clonePath, "origin")
	if err != nil {
		return repo, err
	}
	if url != repo.CloneUrl || clonePath != repo.ClonePath {
		if err := p.Store.Q.UpdateRepoClone(ctx, db.UpdateRepoCloneParams{CloneUrl: url, ClonePath: clonePath, DefaultBranch: repo.DefaultBranch, ID: repo.ID}); err != nil {
			return repo, err
		}
		repo.CloneUrl, repo.ClonePath = url, clonePath
	}
	return repo, nil
}

// checkoutDirs names each repo's directory in a workspace: its short name,
// or owner-name where two repos share one.
func checkoutDirs(repos []db.Repo) map[string]string {
	count := map[string]int{}
	for _, r := range repos {
		count[path.Base(r.FullName)]++
	}
	dirs := make(map[string]string, len(repos))
	for _, r := range repos {
		name := path.Base(r.FullName)
		if count[name] > 1 {
			name = strings.ReplaceAll(r.FullName, "/", "-")
		}
		dirs[r.ID] = name
	}
	return dirs
}

// ensureCheckouts gives the unit a checkout of every project repo, detached
// at the tip of its default branch. Existing checkouts are left alone: they
// may hold work.
func (p *Pipeline) ensureCheckouts(ctx context.Context, u db.Unit) ([]db.ListUnitReposRow, error) {
	repos, err := p.Store.Q.ListReposByProject(ctx, u.ProjectID)
	if err != nil {
		return nil, err
	}
	if len(repos) == 0 {
		return nil, errors.New("the project has no repositories; link at least one GitHub repository to it")
	}
	existing, err := p.Store.Q.ListUnitRepos(ctx, u.ID)
	if err != nil {
		return nil, err
	}
	have := map[string]db.ListUnitReposRow{}
	for _, ur := range existing {
		have[ur.RepoID] = ur
	}
	dirs := checkoutDirs(repos)
	for _, repo := range repos {
		if ur, ok := have[repo.ID]; ok && ur.CheckoutPath != "" {
			if _, err := os.Stat(filepath.Join(ur.CheckoutPath, ".git")); err == nil {
				continue
			}
		}
		repo, err := p.syncManagedClone(ctx, repo)
		if err != nil {
			return nil, err
		}
		dst := filepath.Join(u.WorkspacePath, dirs[repo.ID])
		if err := os.RemoveAll(dst); err != nil {
			return nil, err
		}
		sha, err := p.Git.CheckoutLocal(ctx, repo.ClonePath, dst, repo.DefaultBranch)
		if err != nil {
			return nil, fmt.Errorf("check out %s: %w", repo.FullName, err)
		}
		if err := p.Store.Q.UpsertUnitRepo(ctx, db.UpsertUnitRepoParams{UnitID: u.ID, RepoID: repo.ID, CheckoutPath: dst, BaseSha: sha, Now: store.Now()}); err != nil {
			return nil, err
		}
	}
	return p.Store.Q.ListUnitRepos(ctx, u.ID)
}

func dirOf(ur db.ListUnitReposRow) string { return filepath.Base(ur.CheckoutPath) }

func promptRepos(urs []db.ListUnitReposRow, branch string) []prompts.Repo {
	out := make([]prompts.Repo, 0, len(urs))
	for _, ur := range urs {
		out = append(out, prompts.Repo{Dir: dirOf(ur), FullName: ur.FullName, DefaultBranch: ur.DefaultBranch, Branch: branch})
	}
	return out
}

// maxInstructions bounds how much of a repo's CLAUDE.md/AGENTS.md goes into
// the system prompt.
const maxInstructions = 16 << 10

// repoInstructions reads the top-level agent instruction files of each
// checkout. With --setting-sources "" the CLI does not load them itself.
func repoInstructions(urs []db.ListUnitReposRow) []prompts.Instructions {
	var out []prompts.Instructions
	for _, ur := range urs {
		for _, name := range []string{"CLAUDE.md", ".claude/CLAUDE.md", "AGENTS.md"} {
			b, err := os.ReadFile(filepath.Join(ur.CheckoutPath, name))
			if err != nil || len(strings.TrimSpace(string(b))) == 0 {
				continue
			}
			if len(b) > maxInstructions {
				b = append(b[:maxInstructions], []byte("\n[truncated]")...)
			}
			out = append(out, prompts.Instructions{Repo: ur.FullName, File: name, Content: string(b)})
		}
	}
	return out
}

// systemPrompt renders the stable part of a unit's prompt.
func systemPrompt(urs []db.ListUnitReposRow) (string, error) {
	text, _, err := prompts.Render("system", prompts.System{Repos: promptRepos(urs, ""), Instructions: repoInstructions(urs)})
	return text, err
}

// readDoc reads a unit document from its workspace file.
func readDoc(u db.Unit, kind string) (string, error) {
	rel, ok := docFiles[kind]
	if !ok {
		return "", fmt.Errorf("document %s has no file", kind)
	}
	b, err := os.ReadFile(filepath.Join(u.WorkspacePath, rel))
	return string(b), err
}

// writeDoc writes a unit document to its workspace file.
func writeDoc(u db.Unit, kind, content string) error {
	rel, ok := docFiles[kind]
	if !ok {
		return nil // stored only in the database
	}
	full := filepath.Join(u.WorkspacePath, rel)
	if err := os.MkdirAll(filepath.Dir(full), 0o755); err != nil {
		return err
	}
	return os.WriteFile(full, []byte(content), 0o644)
}

// snapshotDoc stores the workspace file as a new version when it changed.
func (p *Pipeline) snapshotDoc(ctx context.Context, u db.Unit, kind, meta, runID string) (db.Document, error) {
	content, err := readDoc(u, kind)
	if errors.Is(err, fs.ErrNotExist) {
		return db.Document{}, fmt.Errorf("the run finished without writing %s", docFiles[kind])
	}
	if err != nil {
		return db.Document{}, err
	}
	if strings.TrimSpace(content) == "" {
		return db.Document{}, fmt.Errorf("the run left %s empty", docFiles[kind])
	}
	return p.saveDoc(ctx, u, kind, content, meta, "claude", runID)
}

func (p *Pipeline) saveDoc(ctx context.Context, u db.Unit, kind, content, meta, author, runID string) (db.Document, error) {
	if meta == "" {
		meta = "{}"
	}
	if latest, err := p.Store.Q.LatestDocument(ctx, db.LatestDocumentParams{UnitID: u.ID, Kind: kind}); err == nil &&
		latest.Content == content && latest.Meta == meta {
		return latest, nil
	}
	id := newID()
	doc, err := p.Store.Q.CreateDocument(context.WithoutCancel(ctx), db.CreateDocumentParams{
		ID: id, UnitID: u.ID, Kind: kind, Content: content, Meta: meta, Author: author, RunID: runID, Now: store.Now(),
	})
	if err != nil {
		return doc, err
	}
	p.activity(ctx, u.ID, author, "document", fmt.Sprintf("%s v%d by %s", kind, doc.Version, author), map[string]any{"kind": kind, "version": doc.Version})
	return doc, nil
}

// editedByUser reports whether the latest version of a document is a user's
// edit, which the next run must preserve.
func (p *Pipeline) editedByUser(ctx context.Context, u db.Unit, kind string) bool {
	latest, err := p.Store.Q.LatestDocument(ctx, db.LatestDocumentParams{UnitID: u.ID, Kind: kind})
	return err == nil && latest.Author != "claude"
}

// workspacePath is where a new unit's workspace lives.
func (p *Pipeline) workspacePath(seq int64, title string) string {
	return filepath.Join(p.Paths.Workspaces(), fmt.Sprintf("u%d-%s", seq, domain.Slug(title, 30)))
}

func removeAll(path string) error { return os.RemoveAll(path) }
