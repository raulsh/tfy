// Package git runs the git operations the orchestrator owns: managed clones,
// per-unit checkouts, and publishing. Agents never run these; their runs have
// no remotes and no credentials.
package git

import (
	"bytes"
	"context"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
)

// Git runs the git binary with the user's own environment (so pushes use the
// user's credentials), minus anything that could block on a terminal.
type Git struct {
	Bin string
}

// Error carries git's stderr.
type Error struct {
	Args   []string
	Stderr string
	Err    error
}

func (e *Error) Error() string {
	msg := strings.TrimSpace(e.Stderr)
	if msg == "" {
		msg = e.Err.Error()
	}
	return fmt.Sprintf("git %s: %s", strings.Join(e.Args, " "), msg)
}

func (e *Error) Unwrap() error { return e.Err }

func (g *Git) bin() string {
	if g.Bin != "" {
		return g.Bin
	}
	return "git"
}

func (g *Git) run(ctx context.Context, dir string, args ...string) (string, error) {
	cmd := exec.CommandContext(ctx, g.bin(), args...)
	cmd.Dir = dir
	cmd.Env = append(os.Environ(), "GIT_TERMINAL_PROMPT=0", "GIT_EDITOR=true", "LC_ALL=C")
	var stdout, stderr bytes.Buffer
	cmd.Stdout, cmd.Stderr = &stdout, &stderr
	if err := cmd.Run(); err != nil {
		return "", &Error{Args: args, Stderr: stderr.String(), Err: err}
	}
	return strings.TrimSpace(stdout.String()), nil
}

// Fetch updates a bare managed clone's branches from origin.
func (g *Git) Fetch(ctx context.Context, bare string) error {
	_, err := g.run(ctx, bare, "fetch", "--prune", "--quiet", "origin", "+refs/heads/*:refs/heads/*")
	return err
}

// RemoteURL returns the URL of a remote.
func (g *Git) RemoteURL(ctx context.Context, dir, remote string) (string, error) {
	return g.run(ctx, dir, "remote", "get-url", remote)
}

// CheckoutLocal creates an isolated checkout of src at dst, detached at ref,
// with no remote left behind: `clone --local` hardlinks objects, so it is
// cheap, and it shares no config or locks with other checkouts. Returns the
// checked-out commit.
func (g *Git) CheckoutLocal(ctx context.Context, src, dst, ref string) (string, error) {
	if err := os.MkdirAll(filepath.Dir(dst), 0o755); err != nil {
		return "", err
	}
	if _, err := g.run(ctx, "", "clone", "--quiet", "--local", "--no-checkout", src, dst); err != nil {
		return "", err
	}
	if _, err := g.run(ctx, dst, "checkout", "--quiet", "--detach", "origin/"+ref); err != nil {
		return "", err
	}
	if _, err := g.run(ctx, dst, "remote", "remove", "origin"); err != nil {
		return "", err
	}
	return g.RevParse(ctx, dst, "HEAD")
}

// RevParse resolves a ref to a commit.
func (g *Git) RevParse(ctx context.Context, dir, ref string) (string, error) {
	return g.run(ctx, dir, "rev-parse", "--verify", "--quiet", ref+"^{commit}")
}

// CurrentBranch returns the checked-out branch, or "" when detached.
func (g *Git) CurrentBranch(ctx context.Context, dir string) (string, error) {
	out, err := g.run(ctx, dir, "symbolic-ref", "--quiet", "--short", "HEAD")
	if err != nil {
		var ge *Error
		if asError(err, &ge) {
			return "", nil
		}
		return "", err
	}
	return out, nil
}

// SwitchBranch checks out branch, creating it at start if it does not exist.
func (g *Git) SwitchBranch(ctx context.Context, dir, branch, start string) error {
	if _, err := g.RevParse(ctx, dir, "refs/heads/"+branch); err == nil {
		_, err := g.run(ctx, dir, "switch", "--quiet", branch)
		return err
	}
	_, err := g.run(ctx, dir, "switch", "--quiet", "-c", branch, start)
	return err
}

// FetchInto copies srcRef from another repository (by path or URL) into
// dstRef, e.g. the latest default branch into refs/remotes/base/main.
func (g *Git) FetchInto(ctx context.Context, dir, src, srcRef, dstRef string) error {
	_, err := g.run(ctx, dir, "fetch", "--quiet", "--no-tags", src, "+"+srcRef+":"+dstRef)
	return err
}

// Dirty reports whether the working tree has uncommitted changes.
func (g *Git) Dirty(ctx context.Context, dir string) (bool, error) {
	out, err := g.run(ctx, dir, "status", "--porcelain")
	return out != "", err
}

// CommitAll stages everything and commits it; it reports whether there was
// anything to commit. Commits are never signed: signing would need an agent.
func (g *Git) CommitAll(ctx context.Context, dir, message string) (bool, error) {
	dirty, err := g.Dirty(ctx, dir)
	if err != nil || !dirty {
		return false, err
	}
	if _, err := g.run(ctx, dir, "add", "-A"); err != nil {
		return false, err
	}
	if _, err := g.run(ctx, dir, "-c", "commit.gpgsign=false", "commit", "--quiet", "--no-verify", "-m", message); err != nil {
		return false, err
	}
	return true, nil
}

// Ahead counts commits reachable from head but not from base.
func (g *Git) Ahead(ctx context.Context, dir, base, head string) (int, error) {
	out, err := g.run(ctx, dir, "rev-list", "--count", base+".."+head)
	if err != nil {
		return 0, err
	}
	return strconv.Atoi(out)
}

// PushBranchWith pushes HEAD to branch on url, with extra leading git
// options (e.g. a credential helper). It is never forced: iterations add
// commits on top of what was reviewed.
func (g *Git) PushBranchWith(ctx context.Context, dir, url, branch string, opts []string) error {
	args := append(append([]string{}, opts...), "push", "--quiet", "--no-verify", url, "HEAD:refs/heads/"+branch)
	_, err := g.run(ctx, dir, args...)
	return err
}

// Log returns one-line summaries of the commits in base..head.
func (g *Git) Log(ctx context.Context, dir, base, head string) (string, error) {
	return g.run(ctx, dir, "log", "--no-decorate", "--format=%h %s", base+".."+head)
}

// Diff returns the change from base to head, with a stat header.
func (g *Git) Diff(ctx context.Context, dir, base, head string) (string, error) {
	return g.run(ctx, dir, "diff", "--stat", "--patch", base+"..."+head)
}

func asError(err error, target **Error) bool {
	ge, ok := err.(*Error)
	if ok {
		*target = ge
	}
	return ok
}

// IsAncestor reports whether a is an ancestor of (or equal to) b.
func (g *Git) IsAncestor(ctx context.Context, dir, a, b string) (bool, error) {
	_, err := g.run(ctx, dir, "merge-base", "--is-ancestor", a, b)
	if err == nil {
		return true, nil
	}
	var ge *Error
	if asError(err, &ge) {
		if ee, ok := ge.Err.(*exec.ExitError); ok && ee.ExitCode() == 1 {
			return false, nil
		}
	}
	return false, err
}

// FastForward moves the checked-out branch to target, refusing anything but
// a fast-forward.
func (g *Git) FastForward(ctx context.Context, dir, target string) error {
	_, err := g.run(ctx, dir, "merge", "--ff-only", "--quiet", target)
	return err
}
