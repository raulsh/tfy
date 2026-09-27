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

// Instructions is one repository's CLAUDE.md or AGENTS.md.
type Instructions struct {
	Repo    string
	File    string
	Content string
}

// Feedback is a user message behind a unit.
type Feedback struct {
	Author  string
	At      string
	Channel string
	Text    string
}

// Criterion is an acceptance criterion from the spec.
type Criterion struct {
	ID   string `json:"id"`
	Text string `json:"text"`
}

// System is the stable part of a unit's prompt, passed with
// --append-system-prompt. It must not change between runs of one session.
type System struct {
	Repos        []Repo
	Instructions []Instructions
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
	Revision       string // reviewer feedback when iterating
	EditedByUser   bool
}

// Plan is the data for plan.tmpl.
type Plan struct {
	Label        string
	Title        string
	Repos        []Repo
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
	Findings        string // review findings when iterating
}

// ReviewDiff is one repository's change under review.
type ReviewDiff struct {
	Dir      string
	FullName string
	Branch   string
	File     string // under docs/review/
	Commits  int
	Checks   string // CI on the pull request: success, failure, pending, none
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
