package pipeline

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"strings"

	"github.com/raulsh/tfy/internal/domain"
	"github.com/raulsh/tfy/internal/store"
	"github.com/raulsh/tfy/internal/store/db"
)

// ProjectInput creates or updates a project.
type ProjectInput struct {
	Name           string `json:"name"`
	Description    string `json:"description"`
	ProductContext string `json:"product_context"`
	// Conventions apply to every repository of the project; nil leaves
	// them as they are.
	Conventions *string                 `json:"conventions"`
	Settings    *domain.ProjectSettings `json:"settings"`
}

// CreateProject registers a project.
func (p *Pipeline) CreateProject(ctx context.Context, in ProjectInput) (db.Project, error) {
	name := strings.TrimSpace(in.Name)
	if name == "" {
		return db.Project{}, &InvalidError{Msg: "a name is required"}
	}
	settings := domain.DefaultProjectSettings()
	if in.Settings != nil {
		settings = domain.ParseProjectSettings(in.Settings.JSON())
	}
	conventions := ""
	if in.Conventions != nil {
		conventions = strings.TrimSpace(*in.Conventions)
	}
	pr, err := p.Store.Q.CreateProject(ctx, db.CreateProjectParams{
		ID: newID(), Name: name, Slug: domain.Slug(name, 40), Description: strings.TrimSpace(in.Description),
		ProductContext: strings.TrimSpace(in.ProductContext), Conventions: conventions, Settings: settings.JSON(), Now: store.Now(),
	})
	if err != nil && strings.Contains(err.Error(), "UNIQUE") {
		return pr, &ConflictError{Msg: "a project with that name already exists"}
	}
	if err == nil {
		p.changed("project", pr.ID)
	}
	return pr, err
}

// UpdateProject changes a project's details and settings.
func (p *Pipeline) UpdateProject(ctx context.Context, id string, in ProjectInput) (db.Project, error) {
	cur, err := p.Store.Q.GetProject(ctx, id)
	if err != nil {
		if store.IsNotFound(err) {
			return cur, &NotFoundError{What: "project"}
		}
		return cur, err
	}
	name := strings.TrimSpace(in.Name)
	if name == "" {
		name = cur.Name
	}
	settings := domain.ParseProjectSettings(cur.Settings)
	if in.Settings != nil {
		settings = domain.ParseProjectSettings(in.Settings.JSON())
	}
	conventions := cur.Conventions
	if in.Conventions != nil {
		conventions = strings.TrimSpace(*in.Conventions)
	}
	pr, err := p.Store.Q.UpdateProject(ctx, db.UpdateProjectParams{
		Name: name, Description: strings.TrimSpace(in.Description), ProductContext: strings.TrimSpace(in.ProductContext),
		Conventions: conventions, Settings: settings.JSON(), Now: store.Now(), ID: id,
	})
	if err == nil {
		p.changed("project", pr.ID)
	}
	return pr, err
}

// DeleteProject removes a project and everything in it.
func (p *Pipeline) DeleteProject(ctx context.Context, id string) error {
	units, err := p.Store.Q.ListUnits(ctx, db.ListUnitsParams{ProjectID: id, Lim: 1000})
	if err != nil {
		return err
	}
	for _, u := range units {
		if p.Busy(ctx, u.ID) {
			return &ConflictError{Msg: fmt.Sprintf("%s still has work running; cancel it first", domain.Label(u.Seq))}
		}
	}
	if err := p.Store.Q.DeleteProject(ctx, id); err != nil {
		return err
	}
	p.changed("project", id)
	return nil
}

var fullNameRE = regexp.MustCompile(`^[A-Za-z0-9_.-]+/[A-Za-z0-9_.-]+$`)

// LinkRepo attaches a GitHub repository to a project, checking it exists and
// reading its default branch. The managed clone is made in the background.
func (p *Pipeline) LinkRepo(ctx context.Context, projectID, fullName string) (db.Repo, error) {
	fullName = strings.TrimSuffix(strings.TrimPrefix(strings.TrimSpace(fullName), "https://github.com/"), ".git")
	if !fullNameRE.MatchString(fullName) {
		return db.Repo{}, &InvalidError{Msg: "give the repository as owner/name"}
	}
	if _, err := p.Store.Q.GetProject(ctx, projectID); err != nil {
		if store.IsNotFound(err) {
			return db.Repo{}, &NotFoundError{What: "project"}
		}
		return db.Repo{}, err
	}
	info, err := p.GH.RepoView(ctx, fullName)
	if err != nil {
		return db.Repo{}, &InvalidError{Msg: fmt.Sprintf("GitHub does not know %s, or gh cannot see it: %v", fullName, err)}
	}
	branch := info.DefaultBranchRef.Name
	if branch == "" {
		branch = "main"
	}
	repo, err := p.Store.Q.CreateRepo(ctx, db.CreateRepoParams{
		ID: newID(), ProjectID: projectID, FullName: info.NameWithOwner, DefaultBranch: branch, Now: store.Now(),
	})
	if err != nil {
		if strings.Contains(err.Error(), "UNIQUE") {
			return repo, &ConflictError{Msg: fullName + " is already linked to this project"}
		}
		return repo, err
	}
	p.changed("project", projectID)
	go func() {
		bg := context.WithoutCancel(ctx)
		if _, err := p.syncManagedClone(bg, repo); err != nil {
			p.Log.Warn("initial clone failed; it will be retried when a unit needs it", "repo", repo.FullName, "error", err)
			return
		}
		p.changed("project", projectID)
	}()
	return repo, nil
}

// UnlinkRepo detaches a repository from a project.
func (p *Pipeline) UnlinkRepo(ctx context.Context, projectID, repoID string) error {
	if err := p.Store.Q.DeleteRepo(ctx, db.DeleteRepoParams{ID: repoID, ProjectID: projectID}); err != nil {
		return err
	}
	p.changed("project", projectID)
	return nil
}

// PrepareAgentEnv writes the files agent runs point at: a gitconfig holding
// only the commit identity, and an empty gh configuration directory.
func (p *Pipeline) PrepareAgentEnv(name, email string) error {
	if name == "" {
		name = "tfy"
	}
	if email == "" {
		email = "tfy@localhost"
	}
	if err := os.MkdirAll(p.Paths.GHConfig(), 0o700); err != nil {
		return err
	}
	if err := os.MkdirAll(filepath.Dir(p.Paths.GitConfig()), 0o700); err != nil {
		return err
	}
	cfg := fmt.Sprintf("[user]\n\tname = %s\n\temail = %s\n[commit]\n\tgpgsign = false\n[init]\n\tdefaultBranch = main\n", name, email)
	return os.WriteFile(p.Paths.GitConfig(), []byte(cfg), 0o644)
}
