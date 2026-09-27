// Package domain holds tfy's core vocabulary: units of work, the
// states they move through, and the rules for moving.
package domain

import (
	"fmt"
	"regexp"
	"slices"
	"strings"
)

// State is where a unit is in the pipeline. Stages are derived from it.
type State string

const (
	StateProposed         State = "proposed"
	StateDefining         State = "defining"
	StateDefinitionReview State = "definition_review"
	StatePlanning         State = "planning"
	StateSpecReview       State = "spec_review"
	StateDeveloping       State = "developing"
	StatePublishing       State = "publishing"
	StateReviewing        State = "reviewing"
	StateAwaitingMerge    State = "awaiting_merge"
	StateMerging          State = "merging"
	StateReleasing        State = "releasing"
	StateDone             State = "done"
	StateRejected         State = "rejected"
)

// Stage groups states for display: the five stages of the factory plus the
// two ends.
type Stage string

const (
	StageIntake     Stage = "intake"
	StageDefinition Stage = "definition"
	StagePlanning   Stage = "planning"
	StageExecuting  Stage = "executing"
	StageRelease    Stage = "release"
	StageDone       Stage = "done"
	StageRejected   Stage = "rejected"
)

// Stages in pipeline order.
var Stages = []Stage{StageIntake, StageDefinition, StagePlanning, StageExecuting, StageRelease, StageDone, StageRejected}

// Stage returns the stage a state belongs to.
func (s State) Stage() Stage {
	switch s {
	case StateProposed:
		return StageIntake
	case StateDefining, StateDefinitionReview:
		return StageDefinition
	case StatePlanning, StateSpecReview:
		return StagePlanning
	case StateDeveloping, StatePublishing, StateReviewing, StateAwaitingMerge, StateMerging:
		return StageExecuting
	case StateReleasing:
		return StageRelease
	case StateDone:
		return StageDone
	case StateRejected:
		return StageRejected
	}
	return ""
}

// WaitsOnHuman reports whether the state is a gate a person must open.
func (s State) WaitsOnHuman() bool {
	switch s {
	case StateProposed, StateDefinitionReview, StateSpecReview, StateAwaitingMerge:
		return true
	}
	return false
}

var stateLabels = map[State]string{
	StateProposed: "Proposed", StateDefining: "Defining", StateDefinitionReview: "Requirement review",
	StatePlanning: "Planning", StateSpecReview: "Spec review", StateDeveloping: "Developing",
	StatePublishing: "Publishing", StateReviewing: "Reviewing", StateAwaitingMerge: "Awaiting merge",
	StateMerging: "Merging", StateReleasing: "Releasing", StateDone: "Done", StateRejected: "Rejected",
}

// Label is the state as people read it.
func (s State) Label() string {
	if l, ok := stateLabels[s]; ok {
		return l
	}
	return string(s)
}

// Terminal reports whether no further work happens in this state.
func (s State) Terminal() bool { return s == StateDone || s == StateRejected }

// transitions lists the legal moves. Rejection is legal from every
// non-terminal state and is added in CanTransition.
var transitions = map[State][]State{
	StateProposed:         {StateDefining},
	StateDefining:         {StateDefinitionReview},
	StateDefinitionReview: {StateDefining, StatePlanning},
	StatePlanning:         {StateSpecReview, StateDefinitionReview},
	StateSpecReview:       {StatePlanning, StateDeveloping, StateDefinitionReview},
	StateDeveloping:       {StatePublishing, StateSpecReview},
	StatePublishing:       {StateReviewing, StateAwaitingMerge, StateDeveloping, StateSpecReview},
	StateReviewing:        {StateDeveloping, StateAwaitingMerge, StateSpecReview, StateReleasing, StateDone},
	StateAwaitingMerge:    {StateMerging, StateReviewing, StateDeveloping, StateSpecReview, StateReleasing, StateDone},
	StateMerging:          {StateReleasing, StateAwaitingMerge, StateDone},
	// A unit with a merge plan releases one step at a time: after a step,
	// the next one gets its update (developing) or is ready to merge.
	StateReleasing: {StateDone, StateDeveloping, StateAwaitingMerge},
	StateRejected:  {StateProposed, StateDefinitionReview, StateSpecReview},
}

// CanTransition reports whether a unit may move from one state to another.
func CanTransition(from, to State) bool {
	if to == StateRejected {
		return !from.Terminal()
	}
	return slices.Contains(transitions[from], to)
}

// TransitionError explains a refused move.
type TransitionError struct{ From, To State }

func (e *TransitionError) Error() string {
	return fmt.Sprintf("a unit cannot move from %s to %s", e.From, e.To)
}

// Attention flags a unit that needs a human, whatever its state.
type Attention string

const (
	AttentionNone            Attention = ""
	AttentionInterrupted     Attention = "interrupted"
	AttentionFailed          Attention = "failed"
	AttentionBudgetExceeded  Attention = "budget_exceeded"
	AttentionWaiting         Attention = "waiting" // rate limited
	AttentionNoChanges       Attention = "no_changes"
	AttentionConflict        Attention = "conflict"
	AttentionReviewBlocked   Attention = "review_blocked"
	AttentionPRClosed        Attention = "pr_closed"
	AttentionHeadChanged     Attention = "head_changed"
	AttentionPartiallyMerged Attention = "partially_merged"
	AttentionCIFailed        Attention = "ci_failed"
	AttentionNewFeedback     Attention = "new_feedback"
)

// Kind is what sort of change a unit is.
type Kind string

const (
	KindFeature     Kind = "feature"
	KindBugfix      Kind = "bugfix"
	KindImprovement Kind = "improvement"
	KindChore       Kind = "chore"
)

// Kinds lists the valid kinds.
var Kinds = []Kind{KindFeature, KindBugfix, KindImprovement, KindChore}

// ParseKind validates a kind, defaulting empty to feature.
func ParseKind(s string) (Kind, error) {
	if s == "" {
		return KindFeature, nil
	}
	k := Kind(strings.ToLower(strings.TrimSpace(s)))
	if slices.Contains(Kinds, k) {
		return k, nil
	}
	return "", fmt.Errorf("unknown kind %q", s)
}

// Origin records how a unit came to be.
type Origin string

const (
	OriginDeveloper   Origin = "developer"
	OriginSlackAuto   Origin = "slack_auto"
	OriginSlackManual Origin = "slack_manual"
	OriginFollowUp    Origin = "follow_up"
	// OriginRetrospective units change the repositories' Claude Code
	// conventions, as proposed by the retrospective of an earlier unit.
	OriginRetrospective Origin = "retrospective"
	// OriginGitHubIssue units were created from a GitHub issue.
	OriginGitHubIssue Origin = "github_issue"
)

// Label is how a unit is referred to by people: U-42.
func Label(seq int64) string { return fmt.Sprintf("U-%d", seq) }

var nonSlug = regexp.MustCompile(`[^a-z0-9]+`)

// Slug turns a title into a short, branch-safe fragment.
func Slug(title string, max int) string {
	s := nonSlug.ReplaceAllString(strings.ToLower(title), "-")
	s = strings.Trim(s, "-")
	if len(s) > max {
		// Cut at a word boundary rather than mid-word.
		cut := s[:max]
		if s[max] != '-' {
			if i := strings.LastIndexByte(cut, '-'); i > 0 {
				cut = cut[:i]
			}
		}
		s = strings.TrimRight(cut, "-")
	}
	if s == "" {
		s = "unit"
	}
	return s
}

// BranchName is the branch a unit's changes live on in every repo: the
// project's template with {seq}, {slug} (from the title) and {kind} filled
// in. An invalid template falls back to DefaultBranchTemplate.
func BranchName(template string, seq int64, kind, title string) string {
	if !ValidBranchTemplate(template) {
		template = DefaultBranchTemplate
	}
	return strings.NewReplacer("{seq}", fmt.Sprint(seq), "{slug}", Slug(title, 40), "{kind}", Slug(kind, 20)).Replace(template)
}
