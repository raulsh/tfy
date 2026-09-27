package domain

import "encoding/json"

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
}

// DefaultProjectSettings are applied to new projects.
func DefaultProjectSettings() ProjectSettings {
	return ProjectSettings{
		MergeMethod:         "squash",
		DeleteBranch:        true,
		MaxReviewIterations: 2,
		TriageConfidenceMin: 0.6,
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
	return s
}

// JSON encodes the settings for storage.
func (s ProjectSettings) JSON() string {
	b, _ := json.Marshal(s)
	return string(b)
}
