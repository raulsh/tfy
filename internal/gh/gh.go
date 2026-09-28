// Package gh wraps the GitHub CLI, which already holds the user's GitHub
// credentials.
package gh

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"strconv"
	"strings"
	"time"
)

// Client runs gh.
type Client struct {
	Bin string
}

// Error carries gh's stderr.
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
	return fmt.Sprintf("gh %s: %s", strings.Join(e.Args[:min(len(e.Args), 3)], " "), msg)
}

func (e *Error) Unwrap() error { return e.Err }

func (c *Client) bin() string {
	if c.Bin != "" {
		return c.Bin
	}
	return "gh"
}

func (c *Client) run(ctx context.Context, args ...string) ([]byte, error) {
	cmd := exec.CommandContext(ctx, c.bin(), args...)
	cmd.Env = append(os.Environ(), "GH_PROMPT_DISABLED=1", "NO_COLOR=1", "GIT_TERMINAL_PROMPT=0")
	var stdout, stderr bytes.Buffer
	cmd.Stdout, cmd.Stderr = &stdout, &stderr
	if err := cmd.Run(); err != nil {
		return nil, &Error{Args: args, Stderr: stderr.String(), Err: err}
	}
	return stdout.Bytes(), nil
}

func (c *Client) runJSON(ctx context.Context, v any, args ...string) error {
	out, err := c.run(ctx, args...)
	if err != nil {
		return err
	}
	if err := json.Unmarshal(out, v); err != nil {
		return fmt.Errorf("gh %s: decode: %w", args[0], err)
	}
	return nil
}

// User returns the authenticated login.
func (c *Client) User(ctx context.Context) (string, error) {
	out, err := c.run(ctx, "api", "user", "--jq", ".login")
	return strings.TrimSpace(string(out)), err
}

// Orgs lists the organizations the user belongs to.
func (c *Client) Orgs(ctx context.Context) ([]string, error) {
	out, err := c.run(ctx, "api", "user/orgs", "--paginate", "--jq", ".[].login")
	if err != nil {
		return nil, err
	}
	return strings.Fields(string(out)), nil
}

// Repo describes a repository.
type Repo struct {
	NameWithOwner    string `json:"nameWithOwner"`
	Description      string `json:"description"`
	URL              string `json:"url"`
	SSHURL           string `json:"sshUrl"`
	IsPrivate        bool   `json:"isPrivate"`
	Visibility       string `json:"visibility"`
	IsArchived       bool   `json:"isArchived"`
	DefaultBranchRef struct {
		Name string `json:"name"`
	} `json:"defaultBranchRef"`
	UpdatedAt time.Time `json:"updatedAt"`
}

const repoFields = "nameWithOwner,description,url,sshUrl,isPrivate,visibility,isArchived,defaultBranchRef,updatedAt"

// RepoView describes one repository.
func (c *Client) RepoView(ctx context.Context, fullName string) (*Repo, error) {
	var r Repo
	if err := c.runJSON(ctx, &r, "repo", "view", fullName, "--json", repoFields); err != nil {
		return nil, err
	}
	return &r, nil
}

// RepoList lists an owner's repositories ("" for the user's own).
func (c *Client) RepoList(ctx context.Context, owner string, limit int) ([]Repo, error) {
	args := []string{"repo", "list"}
	if owner != "" {
		args = append(args, owner)
	}
	args = append(args, "--no-archived", "--limit", strconv.Itoa(limit), "--json", repoFields)
	var rs []Repo
	return rs, c.runJSON(ctx, &rs, args...)
}

// CloneBare clones a repository without a working tree, with the protocol
// and credentials gh is configured for.
func (c *Client) CloneBare(ctx context.Context, fullName, path string) error {
	_, err := c.run(ctx, "repo", "clone", fullName, path, "--", "--bare", "--quiet")
	return err
}

// Check is one entry of statusCheckRollup: a check run or a commit status.
type Check struct {
	Typename   string `json:"__typename"`
	Name       string `json:"name"`
	Context    string `json:"context"`
	Status     string `json:"status"`
	Conclusion string `json:"conclusion"`
	State      string `json:"state"`
	DetailsURL string `json:"detailsUrl"`
	TargetURL  string `json:"targetUrl"`
}

// PR is a pull request as `gh pr view --json` reports it.
type PR struct {
	Number           int        `json:"number"`
	URL              string     `json:"url"`
	Title            string     `json:"title"`
	State            string     `json:"state"` // OPEN, CLOSED, MERGED
	IsDraft          bool       `json:"isDraft"`
	Mergeable        string     `json:"mergeable"`
	MergeStateStatus string     `json:"mergeStateStatus"`
	HeadRefName      string     `json:"headRefName"`
	HeadRefOid       string     `json:"headRefOid"`
	MergedAt         *time.Time `json:"mergedAt"`
	MergeCommit      *struct {
		Oid string `json:"oid"`
	} `json:"mergeCommit"`
	StatusCheckRollup []Check `json:"statusCheckRollup"`
}

// ChecksState summarizes the checks: failure, pending, success, or "" when
// there are none.
func (p *PR) ChecksState() string {
	if len(p.StatusCheckRollup) == 0 {
		return ""
	}
	pending := false
	for _, c := range p.StatusCheckRollup {
		v := strings.ToUpper(c.Conclusion)
		if v == "" {
			v = strings.ToUpper(c.State)
		}
		if v == "" && c.Status != "" && !strings.EqualFold(c.Status, "COMPLETED") {
			v = "PENDING"
		}
		switch v {
		case "FAILURE", "ERROR", "CANCELLED", "TIMED_OUT", "ACTION_REQUIRED", "STARTUP_FAILURE":
			return "failure"
		case "PENDING", "QUEUED", "IN_PROGRESS", "EXPECTED", "WAITING", "REQUESTED", "":
			pending = true
		}
	}
	if pending {
		return "pending"
	}
	return "success"
}

// MergeSHA is the merge commit, if merged.
func (p *PR) MergeSHA() string {
	if p.MergeCommit == nil {
		return ""
	}
	return p.MergeCommit.Oid
}

const prFields = "number,url,title,state,isDraft,mergeable,mergeStateStatus,headRefName,headRefOid,mergedAt,mergeCommit,statusCheckRollup"

// PRView describes one pull request.
func (c *Client) PRView(ctx context.Context, repo string, number int) (*PR, error) {
	var p PR
	if err := c.runJSON(ctx, &p, "pr", "view", strconv.Itoa(number), "--repo", repo, "--json", prFields); err != nil {
		return nil, err
	}
	return &p, nil
}

// PRForBranch finds the pull request whose head is branch, in any state.
func (c *Client) PRForBranch(ctx context.Context, repo, branch string) (*PR, error) {
	var ps []PR
	if err := c.runJSON(ctx, &ps, "pr", "list", "--repo", repo, "--head", branch, "--state", "all",
		"--json", "number,url,title,state,isDraft,headRefName,headRefOid"); err != nil {
		return nil, err
	}
	for i := range ps {
		if ps[i].HeadRefName == branch {
			return &ps[i], nil
		}
	}
	return nil, nil
}

// PRCreate opens a pull request and returns its URL.
func (c *Client) PRCreate(ctx context.Context, repo, base, head, title, bodyFile string, draft bool) (string, error) {
	args := []string{"pr", "create", "--repo", repo, "--base", base, "--head", head, "--title", title, "--body-file", bodyFile}
	if draft {
		args = append(args, "--draft")
	}
	out, err := c.run(ctx, args...)
	if err != nil {
		return "", err
	}
	lines := strings.Fields(string(out))
	if len(lines) == 0 {
		return "", fmt.Errorf("gh pr create printed no URL")
	}
	return lines[len(lines)-1], nil
}

// PREdit replaces a pull request's description, and its title unless title
// is empty.
func (c *Client) PREdit(ctx context.Context, repo string, number int, title, bodyFile string) error {
	args := []string{"pr", "edit", strconv.Itoa(number), "--repo", repo, "--body-file", bodyFile}
	if title != "" {
		args = append(args, "--title", title)
	}
	_, err := c.run(ctx, args...)
	return err
}

// Comment is something a person wrote on a pull request.
type Comment struct {
	Author string `json:"author"`
	Kind   string `json:"kind"` // comment, review, or inline
	State  string `json:"state,omitempty"`
	Path   string `json:"path,omitempty"`
	Body   string `json:"body"`
}

// PRDiscussion returns what people (not bots) wrote on a pull request: its
// conversation, review summaries, and inline review comments.
func (c *Client) PRDiscussion(ctx context.Context, repo string, number int) ([]Comment, error) {
	type author struct {
		Login string `json:"login"`
	}
	var pr struct {
		Comments []struct {
			Author author `json:"author"`
			Body   string `json:"body"`
		} `json:"comments"`
		Reviews []struct {
			Author author `json:"author"`
			Body   string `json:"body"`
			State  string `json:"state"`
		} `json:"reviews"`
	}
	if err := c.runJSON(ctx, &pr, "pr", "view", strconv.Itoa(number), "--repo", repo, "--json", "comments,reviews"); err != nil {
		return nil, err
	}
	var inline []struct {
		User struct {
			Login string `json:"login"`
			Type  string `json:"type"`
		} `json:"user"`
		Body string `json:"body"`
		Path string `json:"path"`
	}
	if err := c.runJSON(ctx, &inline, "api", fmt.Sprintf("repos/%s/pulls/%d/comments?per_page=100", repo, number)); err != nil {
		return nil, err
	}
	bot := func(login string) bool { return strings.HasSuffix(login, "[bot]") }
	var out []Comment
	for _, x := range pr.Comments {
		if !bot(x.Author.Login) && strings.TrimSpace(x.Body) != "" {
			out = append(out, Comment{Author: x.Author.Login, Kind: "comment", Body: x.Body})
		}
	}
	for _, x := range pr.Reviews {
		if !bot(x.Author.Login) && (strings.TrimSpace(x.Body) != "" || x.State == "CHANGES_REQUESTED") {
			out = append(out, Comment{Author: x.Author.Login, Kind: "review", State: x.State, Body: x.Body})
		}
	}
	for _, x := range inline {
		if !bot(x.User.Login) && x.User.Type != "Bot" {
			out = append(out, Comment{Author: x.User.Login, Kind: "inline", Path: x.Path, Body: x.Body})
		}
	}
	return out, nil
}

// PRReady marks a draft ready for review.
func (c *Client) PRReady(ctx context.Context, repo string, number int) error {
	_, err := c.run(ctx, "pr", "ready", strconv.Itoa(number), "--repo", repo)
	return err
}

// PRMerge merges a pull request, but only if its head is still matchHead.
func (c *Client) PRMerge(ctx context.Context, repo string, number int, method, matchHead string, deleteBranch, admin bool) error {
	args := []string{"pr", "merge", strconv.Itoa(number), "--repo", repo, "--" + method}
	if matchHead != "" {
		args = append(args, "--match-head-commit", matchHead)
	}
	if deleteBranch {
		args = append(args, "--delete-branch")
	}
	if admin {
		// Bypasses the base branch's rules, for a user allowed to.
		args = append(args, "--admin")
	}
	_, err := c.run(ctx, args...)
	return err
}

// RulesRefused reports whether gh refused a merge because the base branch's
// rules forbid it for now, such as a required approval or an out-of-date
// branch: gh then suggests --admin, which lets an administrator merge
// anyway.
func RulesRefused(err error) bool {
	var e *Error
	return errors.As(err, &e) && strings.Contains(e.Stderr, "--admin")
}

// PRClose closes a pull request.
func (c *Client) PRClose(ctx context.Context, repo string, number int, deleteBranch bool) error {
	args := []string{"pr", "close", strconv.Itoa(number), "--repo", repo}
	if deleteBranch {
		args = append(args, "--delete-branch")
	}
	_, err := c.run(ctx, args...)
	return err
}

// PRComment adds a comment to a pull request.
func (c *Client) PRComment(ctx context.Context, repo string, number int, bodyFile string) error {
	_, err := c.run(ctx, "pr", "comment", strconv.Itoa(number), "--repo", repo, "--body-file", bodyFile)
	return err
}

// WorkflowRun is one Actions run on a commit.
type WorkflowRun struct {
	DatabaseID int    `json:"databaseId"`
	Name       string `json:"name"`
	Status     string `json:"status"`
	Conclusion string `json:"conclusion"`
	URL        string `json:"url"`
}

// RunsForCommit lists the Actions runs on a commit.
func (c *Client) RunsForCommit(ctx context.Context, repo, sha string) ([]WorkflowRun, error) {
	var rs []WorkflowRun
	return rs, c.runJSON(ctx, &rs, "run", "list", "--repo", repo, "--commit", sha, "--json", "databaseId,name,status,conclusion,url")
}

// Issue is a GitHub issue with its conversation.
type Issue struct {
	Number int    `json:"number"`
	Title  string `json:"title"`
	Body   string `json:"body"`
	State  string `json:"state"` // OPEN | CLOSED
	URL    string `json:"url"`
	Author struct {
		Login string `json:"login"`
		IsBot bool   `json:"is_bot"`
	} `json:"author"`
	Labels []struct {
		Name string `json:"name"`
	} `json:"labels"`
	Comments []struct {
		Author struct {
			Login string `json:"login"`
			IsBot bool   `json:"is_bot"`
		} `json:"author"`
		Body      string    `json:"body"`
		CreatedAt time.Time `json:"createdAt"`
	} `json:"comments"`
	UpdatedAt time.Time `json:"updatedAt"`
}

// ErrNotAnIssue is returned for a number that is a pull request: gh resolves
// those as issues too.
var ErrNotAnIssue = fmt.Errorf("that number is a pull request, not an issue")

// IssueView reads an issue and its comments.
func (c *Client) IssueView(ctx context.Context, repo string, number int) (*Issue, error) {
	var is Issue
	if err := c.runJSON(ctx, &is, "issue", "view", strconv.Itoa(number), "--repo", repo,
		"--json", "number,title,body,state,url,author,labels,comments,updatedAt"); err != nil {
		return nil, err
	}
	if strings.Contains(is.URL, "/pull/") || is.State == "MERGED" {
		return nil, ErrNotAnIssue
	}
	return &is, nil
}

// IssueSummary is an issue as a list shows it.
type IssueSummary struct {
	Number int    `json:"number"`
	Title  string `json:"title"`
	URL    string `json:"url"`
	Labels []struct {
		Name string `json:"name"`
	} `json:"labels"`
	UpdatedAt time.Time `json:"updatedAt"`
}

// IssueList lists a repository's open issues, most recently updated first,
// optionally matching a search.
func (c *Client) IssueList(ctx context.Context, repo, search string, limit int) ([]IssueSummary, error) {
	args := []string{"issue", "list", "--repo", repo, "--state", "open", "--limit", strconv.Itoa(limit),
		"--json", "number,title,url,labels,updatedAt"}
	if search != "" {
		args = append(args, "--search", search)
	}
	var out []IssueSummary
	return out, c.runJSON(ctx, &out, args...)
}

// IssueComment adds a comment to an issue and returns its URL.
func (c *Client) IssueComment(ctx context.Context, repo string, number int, bodyFile string) (string, error) {
	out, err := c.run(ctx, "issue", "comment", strconv.Itoa(number), "--repo", repo, "--body-file", bodyFile)
	return strings.TrimSpace(string(out)), err
}

// IssueEdit replaces an issue's description, and its title unless title is
// empty.
func (c *Client) IssueEdit(ctx context.Context, repo string, number int, title, bodyFile string) error {
	args := []string{"issue", "edit", strconv.Itoa(number), "--repo", repo, "--body-file", bodyFile}
	if title != "" {
		args = append(args, "--title", title)
	}
	_, err := c.run(ctx, args...)
	return err
}
