package pipeline

// Conventions are how a project wants its work done: how commits, pull
// requests and code are written. Applying them is Claude Code's job, so tfy
// keeps them where Claude Code reads them:
//
//   - each repository's CLAUDE.md, .claude/rules and .claude/settings.json
//     (hooks, attribution), versioned and reviewed with the code, and the
//     same for the people who use Claude Code in the repository by hand;
//   - the project's conventions in tfy, for what spans its repositories.
//
// A unit's runs start in its workspace, a folder holding one checkout per
// repository. Before every run tfy makes that folder a Claude Code project
// whose CLAUDE.md imports what each repository's own session would load at
// start-up, and whose settings carry the repositories' hooks. The rest
// (path-scoped rules, nested CLAUDE.md files, imports) Claude Code does on
// its own.

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path"
	"path/filepath"
	"slices"
	"strings"

	"gopkg.in/yaml.v3"

	"github.com/raulsh/tfy/internal/claude"
	"github.com/raulsh/tfy/internal/domain"
	"github.com/raulsh/tfy/internal/store"
	"github.com/raulsh/tfy/internal/store/db"
)

const workspaceMemoryNote = "<!-- Written by tfy before every run: changes made here are lost. -->"

// claudeProject is what prepareClaudeProject set up for a run.
type claudeProject struct {
	// RepoAttribution is set when a repository declares its own
	// attribution; tfy's default must then stay out of the way.
	RepoAttribution bool
	// Hooks counts the repository hooks the run will have.
	Hooks int
	// Notes explain repository hooks that were left out.
	Notes []string
}

// prepareClaudeProject writes the unit workspace's CLAUDE.md and
// .claude/settings.json. Both are rewritten before every run, and anything
// else under .claude is removed, so nothing an agent changes there reaches
// the next run. withHooks adds the repositories' hooks; runs that do not work
// in the repositories do without them.
func (p *Pipeline) prepareClaudeProject(ctx context.Context, u db.Unit, urs []db.ListUnitReposRow, withHooks bool) (claudeProject, error) {
	var cp claudeProject
	project, err := p.Store.Q.GetProject(ctx, u.ProjectID)
	if err != nil {
		return cp, err
	}
	var checkouts []db.ListUnitReposRow
	for _, ur := range urs {
		if _, err := os.Stat(filepath.Join(ur.CheckoutPath, ".git")); err == nil {
			checkouts = append(checkouts, ur)
		}
	}
	ws := u.WorkspacePath
	if err := os.WriteFile(filepath.Join(ws, "CLAUDE.md"), []byte(workspaceMemory(u, project, checkouts)), 0o644); err != nil {
		return cp, err
	}
	// CLAUDE.md files above the workspace load too; the ones tfy's own
	// directories could hold are not anybody's instructions.
	for _, stray := range []string{
		filepath.Join(ws, "CLAUDE.local.md"),
		filepath.Join(p.Paths.Workspaces(), "CLAUDE.md"), filepath.Join(p.Paths.Workspaces(), "CLAUDE.local.md"),
		filepath.Join(p.Paths.Root, "CLAUDE.md"), filepath.Join(p.Paths.Root, "CLAUDE.local.md"),
	} {
		_ = os.Remove(stray)
	}

	settings := map[string]any{}
	if withHooks {
		hooks := map[string][]any{}
		for _, ur := range checkouts {
			repoSettings, note, err := p.trustedRepoSettings(ctx, ur)
			if err != nil {
				return cp, err
			}
			if note != "" {
				cp.Notes = append(cp.Notes, note)
			}
			if repoSettings == nil {
				continue
			}
			cp.Hooks += mergeRepoHooks(hooks, repoSettings["hooks"], ur.CheckoutPath)
			for _, key := range []string{"attribution", "includeCoAuthoredBy"} {
				if v, ok := repoSettings[key]; ok && !cp.RepoAttribution {
					settings[key] = v
					if key == "attribution" || v == false {
						cp.RepoAttribution = true
					}
				}
			}
		}
		if len(hooks) > 0 {
			settings["hooks"] = hooks
		}
	}
	dir := filepath.Join(ws, ".claude")
	if err := os.RemoveAll(dir); err != nil {
		return cp, err
	}
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return cp, err
	}
	data, err := json.MarshalIndent(settings, "", "  ")
	if err != nil {
		return cp, err
	}
	return cp, os.WriteFile(filepath.Join(dir, "settings.json"), data, 0o644)
}

// workspaceMemory is the workspace's CLAUDE.md: the project's conventions,
// then, per checkout, imports of the instruction files a session started in
// that repository would load at once.
func workspaceMemory(u db.Unit, project db.Project, checkouts []db.ListUnitReposRow) string {
	var b strings.Builder
	b.WriteString(workspaceMemoryNote + "\n\n")
	fmt.Fprintf(&b, "# Workspace of %s\n\n", domain.Label(u.Seq))
	b.WriteString("This folder is not a repository: docs/ holds the unit's documents")
	if len(checkouts) > 0 {
		b.WriteString(", and every other folder is a checkout of one of the project's repositories. Each repository's own instructions for Claude are imported below and apply to work inside it; its path-scoped rules load when you work on matching files.")
	} else {
		b.WriteString(".")
	}
	b.WriteString("\n")
	if conv := strings.TrimSpace(project.Conventions); conv != "" {
		fmt.Fprintf(&b, "\n## Conventions of %s\n\nThey apply to every repository of the project. Where a repository's own instructions say otherwise, follow the repository.\n\n%s\n", project.Name, conv)
	}
	for _, ur := range checkouts {
		dir := dirOf(ur)
		fmt.Fprintf(&b, "\n## %s/ (%s)\n\n", dir, ur.FullName)
		imports := repoMemoryFiles(ur.CheckoutPath)
		if len(imports) == 0 {
			b.WriteString("The repository has no instructions for Claude.\n")
			continue
		}
		for _, rel := range imports {
			fmt.Fprintf(&b, "@%s/%s\n", dir, rel)
		}
	}
	return b.String()
}

// repoMemoryFiles lists, relative to a checkout, what Claude Code loads when
// a session starts in it: CLAUDE.md and .claude/CLAUDE.md (AGENTS.md when
// there is neither), and the rules in .claude/rules that are not scoped to
// paths. Scoped rules load natively when Claude works on matching files.
func repoMemoryFiles(checkout string) []string {
	var out []string
	for _, name := range []string{"CLAUDE.md", ".claude/CLAUDE.md"} {
		if nonEmptyFile(filepath.Join(checkout, name)) {
			out = append(out, name)
		}
	}
	if len(out) == 0 && nonEmptyFile(filepath.Join(checkout, "AGENTS.md")) {
		out = append(out, "AGENTS.md")
	}
	root := filepath.Join(checkout, ".claude", "rules")
	var rules []string
	_ = filepath.WalkDir(root, func(p string, d fs.DirEntry, err error) error {
		if err != nil || d.IsDir() || !strings.HasSuffix(d.Name(), ".md") {
			return nil
		}
		rel, _ := filepath.Rel(checkout, p)
		rel = filepath.ToSlash(rel)
		// An import ends at whitespace.
		if strings.ContainsAny(rel, " \t") {
			return nil
		}
		if b, err := os.ReadFile(p); err == nil && len(rulePaths(b)) == 0 {
			rules = append(rules, rel)
		}
		return nil
	})
	slices.Sort(rules)
	return append(out, rules...)
}

func nonEmptyFile(path string) bool {
	b, err := os.ReadFile(path)
	return err == nil && strings.TrimSpace(string(b)) != ""
}

// rulePaths returns the `paths` globs of a rule's frontmatter: the files the
// rule is scoped to. None means the rule always applies.
func rulePaths(content []byte) []string {
	s := strings.ReplaceAll(string(content), "\r\n", "\n")
	if !strings.HasPrefix(s, "---\n") {
		return nil
	}
	front, _, ok := strings.Cut(s[4:], "\n---")
	if !ok {
		return nil
	}
	var fm struct {
		Paths any `yaml:"paths"`
	}
	if yaml.Unmarshal([]byte(front), &fm) != nil {
		return nil
	}
	switch v := fm.Paths.(type) {
	case string:
		if strings.TrimSpace(v) != "" {
			return []string{v}
		}
	case []any:
		var out []string
		for _, x := range v {
			if s, ok := x.(string); ok && s != "" {
				out = append(out, s)
			}
		}
		return out
	}
	return nil
}

// trustedRepoSettings reads a repository's .claude/settings.json from its
// default branch — what the maintainers reviewed and merged — rather than
// from the checkout, which an agent can change. Hook scripts do run from the
// checkout, so they must match the default branch too: when .claude/hooks
// differs, the repository's hooks are left out of the run and the note
// says why.
func (p *Pipeline) trustedRepoSettings(ctx context.Context, ur db.ListUnitReposRow) (map[string]any, string, error) {
	repo, err := p.Store.Q.GetRepo(ctx, ur.RepoID)
	if err != nil {
		return nil, "", err
	}
	if repo.ClonePath == "" {
		return nil, "", nil
	}
	files, err := p.Git.ListFiles(ctx, repo.ClonePath, repo.DefaultBranch, ".claude/settings.json")
	if err != nil || len(files) == 0 {
		return nil, "", nil // no settings, or a branch the clone does not have yet
	}
	raw, err := p.Git.ReadBlob(ctx, repo.ClonePath, files[0].Blob)
	if err != nil {
		return nil, "", err
	}
	var settings map[string]any
	if err := json.Unmarshal([]byte(raw), &settings); err != nil {
		return nil, fmt.Sprintf("%s: .claude/settings.json on %s is not valid JSON; its hooks were left out", ur.FullName, repo.DefaultBranch), nil
	}
	if settings["hooks"] != nil {
		same, err := p.hooksMatchDefault(ctx, repo.ClonePath, repo.DefaultBranch, ur.CheckoutPath)
		if err != nil {
			return nil, "", err
		}
		if !same {
			delete(settings, "hooks")
			return settings, fmt.Sprintf("%s: .claude/hooks in the checkout differs from %s, so the repository's hooks were left out of this run", ur.FullName, repo.DefaultBranch), nil
		}
	}
	return settings, "", nil
}

// hooksMatchDefault reports whether the checkout's .claude/hooks holds
// exactly the files of the default branch's.
func (p *Pipeline) hooksMatchDefault(ctx context.Context, clone, branch, checkout string) (bool, error) {
	entries, err := p.Git.ListFiles(ctx, clone, branch, ".claude/hooks")
	if err != nil {
		return false, err
	}
	want := map[string]string{}
	for _, e := range entries {
		if e.Mode == "120000" {
			return false, nil // a symlink's target is not what git hashes
		}
		want[e.Path] = e.Blob
	}
	var files []string
	root := filepath.Join(checkout, ".claude", "hooks")
	err = filepath.WalkDir(root, func(p string, d fs.DirEntry, err error) error {
		if err != nil {
			if errors.Is(err, fs.ErrNotExist) {
				return nil
			}
			return err
		}
		if d.IsDir() {
			return nil
		}
		rel, _ := filepath.Rel(checkout, p)
		files = append(files, filepath.ToSlash(rel))
		return nil
	})
	if err != nil {
		return false, err
	}
	if len(files) != len(want) {
		return false, nil
	}
	ids, err := p.Git.HashFiles(ctx, checkout, files)
	if err != nil {
		return false, err
	}
	for i, f := range files {
		if want[f] != ids[i] {
			return false, nil
		}
	}
	return true, nil
}

// mergeRepoHooks adds a repository's hooks (the "hooks" value of its
// settings) to hooks, and returns how many it added. Command hooks are
// rewritten to run as they would in a session started in the repository:
// from its checkout, with CLAUDE_PROJECT_DIR pointing there. Everything else
// about a hook is kept as the repository wrote it.
func mergeRepoHooks(hooks map[string][]any, repoHooks any, checkout string) int {
	events, ok := repoHooks.(map[string]any)
	if !ok {
		return 0
	}
	n := 0
	for event, v := range events {
		matchers, ok := v.([]any)
		if !ok {
			continue
		}
		for _, m := range matchers {
			matcher, ok := m.(map[string]any)
			if !ok {
				continue
			}
			list, _ := matcher["hooks"].([]any)
			var kept []any
			for _, h := range list {
				hook, ok := h.(map[string]any)
				if !ok {
					continue
				}
				if t, _ := hook["type"].(string); t == "" || t == "command" {
					cmd, _ := hook["command"].(string)
					if strings.TrimSpace(cmd) == "" {
						continue
					}
					hook["command"] = repoHookCommand(checkout, cmd)
				}
				kept = append(kept, hook)
				n++
			}
			if len(kept) == 0 {
				continue
			}
			matcher["hooks"] = kept
			hooks[event] = append(hooks[event], matcher)
		}
	}
	return n
}

// repoHookCommand wraps a repository hook's command so that it runs from the
// checkout with CLAUDE_PROJECT_DIR set to it, as in the repository's own
// sessions; the braces keep a compound command together.
func repoHookCommand(checkout, command string) string {
	q := shellQuote(checkout)
	return "export CLAUDE_PROJECT_DIR=" + q + " && cd " + q + " && {\n" + command + "\n}"
}

func shellQuote(s string) string {
	return "'" + strings.ReplaceAll(s, "'", `'\''`) + "'"
}

// hookCommand is the command line of one of tfy's own hooks: the stable copy
// of the binary with args.
func (p *Pipeline) hookCommand(args ...string) string {
	parts := []string{shellQuote(p.Paths.GuardBin())}
	for _, a := range args {
		parts = append(parts, shellQuote(a))
	}
	return strings.Join(parts, " ")
}

// runSettings builds the --settings file of a guarded run: the guard on
// every shell command, the deny rules, auto mode's trust, tfy's default
// attribution unless a repository set its own, and, for development, the
// Stop hook that sends Claude back to commit what it left.
func (p *Pipeline) runSettings(req runRequest, cp claudeProject, profile claude.Profile) claude.Settings {
	settings := claude.GuardSettings(p.hookCommand("hook-guard", "--stage", req.Kind), req.Trusted)
	if !p.Config.CommitAttribution && !cp.RepoAttribution {
		settings.Attribution = &claude.Attribution{}
	}
	if profile.Name == "develop" && len(req.Trusted) > 0 {
		args := []string{"hook-stop"}
		for _, dir := range req.Trusted {
			args = append(args, "--dir", filepath.Join(req.Unit.WorkspacePath, dir))
		}
		settings.AddHook("Stop", "", claude.Hook{Type: "command", Command: p.hookCommand(args...), Timeout: 30})
	}
	return settings
}

// conventionPaths are the files and folders that make up a repository's
// conventions for Claude, as the Conventions view shows them.
var conventionPaths = []string{
	"CLAUDE.md", "AGENTS.md", ".claude/CLAUDE.md", ".claude/rules", ".claude/hooks", ".claude/settings.json",
	".github/pull_request_template.md", ".github/PULL_REQUEST_TEMPLATE.md", ".github/PULL_REQUEST_TEMPLATE",
	"docs/pull_request_template.md", "pull_request_template.md",
}

// RepoConventions is what Claude Code follows in one repository, as its
// default branch has it.
type RepoConventions struct {
	RepoID        string           `json:"repo_id"`
	Repo          string           `json:"repo"`
	DefaultBranch string           `json:"default_branch"`
	Files         []ConventionFile `json:"files"`
	Hooks         []ConventionHook `json:"hooks"`
	Attribution   any              `json:"attribution,omitempty"`
	Error         string           `json:"error,omitempty"`
}

// ConventionFile is one file of a repository's conventions.
type ConventionFile struct {
	Path string `json:"path"`
	// Kind is instructions (CLAUDE.md, AGENTS.md), rule, hook_script,
	// settings, or pr_template.
	Kind    string   `json:"kind"`
	Paths   []string `json:"paths,omitempty"` // a rule's scope
	Content string   `json:"content"`
}

// ConventionHook is one hook of a repository's .claude/settings.json.
type ConventionHook struct {
	Event   string `json:"event"`
	Matcher string `json:"matcher,omitempty"`
	Type    string `json:"type"`
	Command string `json:"command,omitempty"`
	Timeout int    `json:"timeout,omitempty"`
}

// maxConventionFile bounds each file the Conventions view returns.
const maxConventionFile = 64 << 10

// Conventions reads the conventions of every repository of a project from
// their default branches. refresh fetches the managed clones first.
func (p *Pipeline) Conventions(ctx context.Context, projectID string, refresh bool) ([]RepoConventions, error) {
	if _, err := p.Store.Q.GetProject(ctx, projectID); err != nil {
		if store.IsNotFound(err) {
			return nil, &NotFoundError{What: "project"}
		}
		return nil, err
	}
	repos, err := p.Store.Q.ListReposByProject(ctx, projectID)
	if err != nil {
		return nil, err
	}
	out := []RepoConventions{}
	for _, repo := range repos {
		rc := RepoConventions{RepoID: repo.ID, Repo: repo.FullName, DefaultBranch: repo.DefaultBranch, Files: []ConventionFile{}, Hooks: []ConventionHook{}}
		if _, err := os.Stat(filepath.Join(p.Paths.ManagedClone(repo.FullName), "HEAD")); refresh || err != nil {
			if repo, err = p.syncManagedClone(ctx, repo); err != nil {
				rc.Error = err.Error()
				out = append(out, rc)
				continue
			}
		}
		if err := p.readConventions(ctx, repo, &rc); err != nil {
			rc.Error = err.Error()
		}
		out = append(out, rc)
	}
	return out, nil
}

func (p *Pipeline) readConventions(ctx context.Context, repo db.Repo, rc *RepoConventions) error {
	clone := p.Paths.ManagedClone(repo.FullName)
	entries, err := p.Git.ListFiles(ctx, clone, repo.DefaultBranch, conventionPaths...)
	if err != nil {
		return err
	}
	for _, e := range entries {
		kind := conventionKind(e.Path)
		if kind == "" {
			continue
		}
		content, err := p.Git.ReadBlob(ctx, clone, e.Blob)
		if err != nil {
			return err
		}
		if len(content) > maxConventionFile {
			content = content[:maxConventionFile] + "\n[truncated]"
		}
		f := ConventionFile{Path: e.Path, Kind: kind, Content: content}
		if kind == "rule" {
			f.Paths = rulePaths([]byte(content))
		}
		if kind == "settings" {
			var s map[string]any
			if json.Unmarshal([]byte(content), &s) == nil {
				rc.Hooks = listHooks(s["hooks"])
				rc.Attribution = s["attribution"]
			}
		}
		rc.Files = append(rc.Files, f)
	}
	return nil
}

func conventionKind(p string) string {
	switch {
	case p == "CLAUDE.md" || p == "AGENTS.md" || p == ".claude/CLAUDE.md":
		return "instructions"
	case strings.HasPrefix(p, ".claude/rules/") && strings.HasSuffix(p, ".md"):
		return "rule"
	case strings.HasPrefix(p, ".claude/hooks/"):
		return "hook_script"
	case p == ".claude/settings.json":
		return "settings"
	case strings.Contains(strings.ToLower(path.Base(p)), "pull_request_template") || strings.HasPrefix(p, ".github/PULL_REQUEST_TEMPLATE/"):
		return "pr_template"
	}
	return ""
}

func listHooks(v any) []ConventionHook {
	out := []ConventionHook{}
	events, _ := v.(map[string]any)
	names := make([]string, 0, len(events))
	for e := range events {
		names = append(names, e)
	}
	slices.Sort(names)
	for _, event := range names {
		matchers, _ := events[event].([]any)
		for _, m := range matchers {
			matcher, _ := m.(map[string]any)
			pattern, _ := matcher["matcher"].(string)
			list, _ := matcher["hooks"].([]any)
			for _, h := range list {
				hook, _ := h.(map[string]any)
				t, _ := hook["type"].(string)
				if t == "" {
					t = "command"
				}
				cmd, _ := hook["command"].(string)
				if cmd == "" {
					cmd, _ = hook["prompt"].(string)
				}
				timeout, _ := hook["timeout"].(float64)
				out = append(out, ConventionHook{Event: event, Matcher: pattern, Type: t, Command: cmd, Timeout: int(timeout)})
			}
		}
	}
	return out
}
