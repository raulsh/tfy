// Package prompts renders the instructions given to each kind of Claude run.
//
// Templates live next to this file and are embedded. Every run records the
// version (a hash of the template) it was given, so a change in behaviour can
// be traced to a change in wording.
package prompts

import (
	"bytes"
	"crypto/sha256"
	"embed"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"strings"
	"text/template"
)

//go:embed *.tmpl *.schema.json
var files embed.FS

var tmpl = template.Must(template.New("").Funcs(template.FuncMap{
	"trim": strings.TrimSpace,
	"join": strings.Join,
}).ParseFS(files, "*.tmpl"))

// Render executes the named template ("define", "plan", …).
func Render(name string, data any) (text, version string, err error) {
	var buf bytes.Buffer
	if err := tmpl.ExecuteTemplate(&buf, name+".tmpl", data); err != nil {
		return "", "", fmt.Errorf("render %s prompt: %w", name, err)
	}
	return collapseBlankLines(buf.String()), Version(name), nil
}

// Version is a short hash of a template's source.
func Version(name string) string {
	src, err := files.ReadFile(name + ".tmpl")
	if err != nil {
		return ""
	}
	sum := sha256.Sum256(src)
	return name + "@" + hex.EncodeToString(sum[:4])
}

// Schema returns the structured-output schema for a run kind, or nil.
func Schema(name string) json.RawMessage {
	b, err := files.ReadFile(name + ".schema.json")
	if err != nil {
		return nil
	}
	return json.RawMessage(b)
}

// collapseBlankLines keeps template conditionals from leaving holes.
func collapseBlankLines(s string) string {
	lines := strings.Split(s, "\n")
	out := make([]string, 0, len(lines))
	blank := 0
	for _, l := range lines {
		if strings.TrimSpace(l) == "" {
			blank++
			if blank > 1 {
				continue
			}
			l = ""
		} else {
			blank = 0
		}
		out = append(out, strings.TrimRight(l, " \t"))
	}
	return strings.TrimSpace(strings.Join(out, "\n")) + "\n"
}

// Repo describes a checkout in a unit's workspace.
type Repo struct {
	Dir           string
	FullName      string
	DefaultBranch string
	Branch        string
}

// Feedback is a user message behind a unit.
type Feedback struct {
	Author  string
	At      string
	Channel string
	Text    string
}

// Issue is a GitHub issue linked to a unit, as tfy last read it.
type Issue struct {
	Ref      string // owner/repo#12
	URL      string
	Title    string
	State    string
	Author   string
	Labels   []string
	Body     string
	Comments []IssueNote
	// File is where the workspace holds its full text (docs/issues/…).
	File string
	// Closes is set when the unit's pull requests close the issue on merge.
	Closes bool
}

// IssueNote is a comment on an issue.
type IssueNote struct {
	Author string
	At     string
	Body   string
}

// Criterion is an acceptance criterion from the spec.
type Criterion struct {
	ID   string `json:"id"`
	Text string `json:"text"`
}

// System is the stable part of a unit's prompt, passed with
// --append-system-prompt. It must not change between runs of one session.
type System struct {
	Repos []Repo
}

// Define is the data for define.tmpl.
type Define struct {
	Label          string
	Project        string
	About          string
	ProductContext string
	Kind           string
	Title          string
	Description    string
	Feedback       []Feedback
	Issues         []Issue
	Revision       string // reviewer feedback when iterating
	EditedByUser   bool
}

// Plan is the data for plan.tmpl.
type Plan struct {
	Label        string
	Title        string
	Repos        []Repo
	Issues       []Issue
	Revision     string
	EditedByUser bool
}

// Develop is the data for develop.tmpl.
type Develop struct {
	Label           string
	Title           string
	Branch          string
	Targets         []Repo
	Criteria        []Criterion
	NewDependencies []string
	Issues          []Issue
	Findings        string // review findings when iterating
	// RequestedBy names the person who asked for the changes in Findings,
	// when it was not the reviewer.
	RequestedBy string
}

// ReviewDiff is one repository's change under review.
type ReviewDiff struct {
	Dir      string
	FullName string
	Branch   string
	File     string // under docs/review/
	Commits  int
	Checks   string // CI on the pull request: success, failure, pending, none
	// UpdateFile, under docs/review/, is the merge run's update alone.
	UpdateFile string
}

// Review is the data for review.tmpl.
type Review struct {
	Label    string
	Title    string
	Diffs    []ReviewDiff
	Criteria []Criterion
	Round    int // 0 for the first review
	// TestReport is what the implementer said about its own test runs.
	TestReport string
	// Update is set when the round reviews what the merge run changed
	// between two merges, as it described it; Merged says what had merged.
	Update string
	Merged []string
	// Requested are the changes people asked for beyond the spec.
	Requested []Request
}

// Request is a change a person asked for after the spec was approved.
type Request struct {
	By   string
	Text string
}

// Merge is the data for merge.tmpl.
type Merge struct {
	Label       string
	Title       string
	MergeMethod string
	// Open and Merged describe the pull requests, one line each.
	Open   []string
	Merged []string
	// Notes say what happened since the last decision.
	Notes []string
	// Findings are the review's, when it asked for changes to an update,
	// or RequestedBy's, when a person did.
	Findings    string
	RequestedBy string
	// Continue is set when the run resumes the session of the last one,
	// which had the instructions already.
	Continue bool
}

// TriageMessage is one Slack message to triage.
type TriageMessage struct {
	ID        string
	Author    string
	At        string
	Channel   string
	Text      string
	InReplyTo string // the parent's id, for thread replies
}

// TriageUnit is open work a message may belong to.
type TriageUnit struct {
	Label   string
	State   string
	Title   string
	Summary string
}

// Triage is the data for triage.tmpl.
type Triage struct {
	Project        string
	About          string
	ProductContext string
	Units          []TriageUnit
	Messages       []TriageMessage
	Parents        []TriageMessage
}

// Release is the data for release.tmpl.
type Release struct {
	Label     string
	Title     string
	Summary   string
	Reporters []string
	PRs       []string
	Commits   string
}

// LearnReview is one review round of a finished unit.
type LearnReview struct {
	Round    int
	Decision string
	Unmet    []string // criteria not met, with the reviewer's evidence
	Findings []string
}

// LearnNote is something a person said about a unit's work.
type LearnNote struct {
	Author string
	Where  string
	Text   string
}

// Learn is the data for learn.tmpl.
type Learn struct {
	Label   string
	Title   string
	Kind    string
	Summary string
	Repos   []Repo
	// PRs describe each pull request: number, final state, CI.
	PRs          []string
	ReviewRounds int
	Reviews      []LearnReview
	// Feedback is what people told tfy: requirement and spec revisions,
	// and changes they asked for in review.
	Feedback   []LearnNote
	EditedDocs []string
	// Comments are what people wrote on the pull requests.
	Comments  []LearnNote
	Denials   []string
	FollowUps []string
}

// IssueCheck is the data for issue.tmpl: a linked issue, and what tfy has
// gathered that the issue might not say yet.
type IssueCheck struct {
	Label  string
	Title  string
	Issue  Issue
	Public bool
	// Feedback are the user reports from Slack behind the unit.
	Feedback            []Feedback
	Requirement         string
	RequirementApproved bool
	Spec                string
	SpecApproved        bool
	PRs                 []string
	ReleaseNotes        string
}
