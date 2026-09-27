package domain

import (
	"encoding/json"
	"regexp"
	"strings"
)

// ProjectSettings is the JSON in projects.settings. Zero values mean the
// defaults below.
type ProjectSettings struct {
	DraftPRs            bool    `json:"draft_prs"`
	MergeMethod         string  `json:"merge_method"` // squash | merge | rebase
	DeleteBranch        bool    `json:"delete_branch"`
	MaxReviewIterations int     `json:"max_review_iterations"`
	PostReviewToGitHub  bool    `json:"post_review_to_github"`
	AutoAcceptProposals bool    `json:"auto_accept_proposals"`
	TriageConfidenceMin float64 `json:"triage_confidence_min"`
	// BranchTemplate names unit branches; see BranchName.
	BranchTemplate string `json:"branch_template"`
	// LearnFromUnits runs a retrospective when a unit is done, which may
	// propose changes to the repositories' Claude Code conventions.
	LearnFromUnits bool `json:"learn_from_units"`
}

// DefaultProjectSettings are applied to new projects.
func DefaultProjectSettings() ProjectSettings {
	return ProjectSettings{
		MergeMethod:         "squash",
		DeleteBranch:        true,
		MaxReviewIterations: 2,
		TriageConfidenceMin: 0.6,
		BranchTemplate:      DefaultBranchTemplate,
		LearnFromUnits:      true,
	}
}

// ParseProjectSettings decodes settings JSON, filling defaults.
func ParseProjectSettings(raw string) ProjectSettings {
	s := DefaultProjectSettings()
	if raw != "" {
		_ = json.Unmarshal([]byte(raw), &s)
	}
	switch s.MergeMethod {
	case "squash", "merge", "rebase":
	default:
		s.MergeMethod = "squash"
	}
	// Zero is valid: review, but never send work back automatically.
	s.MaxReviewIterations = max(s.MaxReviewIterations, 0)
	if s.TriageConfidenceMin <= 0 || s.TriageConfidenceMin > 1 {
		s.TriageConfidenceMin = 0.6
	}
	if !ValidBranchTemplate(s.BranchTemplate) {
		s.BranchTemplate = DefaultBranchTemplate
	}
	return s
}

// JSON encodes the settings for storage.
func (s ProjectSettings) JSON() string {
	b, _ := json.Marshal(s)
	return string(b)
}

// DefaultBranchTemplate is the branch name of a unit unless the project
// sets its own.
const DefaultBranchTemplate = "tfy/u{seq}-{slug}"

var branchTemplateChars = regexp.MustCompile(`^[A-Za-z0-9._/{}-]+$`)

// ValidBranchTemplate reports whether t yields valid, unique branch names:
// only safe characters, the {seq} placeholder (so two units never share a
// branch), and no empty path segments.
func ValidBranchTemplate(t string) bool {
	if !branchTemplateChars.MatchString(t) || !strings.Contains(t, "{seq}") {
		return false
	}
	if strings.HasPrefix(t, "/") || strings.HasSuffix(t, "/") || strings.Contains(t, "//") || strings.Contains(t, "..") {
		return false
	}
	rest := strings.NewReplacer("{seq}", "", "{slug}", "", "{kind}", "").Replace(t)
	return !strings.ContainsAny(rest, "{}")
}
