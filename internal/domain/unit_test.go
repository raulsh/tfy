package domain

import "testing"

func TestHappyPathIsLegal(t *testing.T) {
	path := []State{
		StateProposed, StateDefining, StateDefinitionReview, StatePlanning, StateSpecReview,
		StateDeveloping, StatePublishing, StateReviewing, StateDeveloping, StatePublishing,
		StateReviewing, StateAwaitingMerge, StateMerging, StateReleasing, StateDone,
	}
	for i := 1; i < len(path); i++ {
		if !CanTransition(path[i-1], path[i]) {
			t.Errorf("%s → %s should be legal", path[i-1], path[i])
		}
	}
}

func TestIllegalMoves(t *testing.T) {
	illegal := [][2]State{
		{StateProposed, StateDeveloping}, // no skipping definition
		{StateDefinitionReview, StateDeveloping},
		{StateSpecReview, StatePublishing},
		{StateDone, StateRejected}, // terminal
		{StateDone, StateDefining},
		{StateReleasing, StatePublishing},
	}
	for _, m := range illegal {
		if CanTransition(m[0], m[1]) {
			t.Errorf("%s → %s should be refused", m[0], m[1])
		}
	}
	// A merge plan releases a step, then develops the next step's update or
	// awaits its merge.
	for _, to := range []State{StateDeveloping, StateAwaitingMerge, StateDone} {
		if !CanTransition(StateReleasing, to) {
			t.Errorf("releasing → %s must be allowed", to)
		}
	}
}

func TestRejectFromAnywhereLive(t *testing.T) {
	for s := range transitions {
		if s.Terminal() {
			continue
		}
		if !CanTransition(s, StateRejected) {
			t.Errorf("cannot reject from %s", s)
		}
	}
}

func TestEveryStateHasAStage(t *testing.T) {
	states := []State{StateProposed, StateDefining, StateDefinitionReview, StatePlanning, StateSpecReview,
		StateDeveloping, StatePublishing, StateReviewing, StateAwaitingMerge, StateMerging, StateReleasing,
		StateDone, StateRejected}
	for _, s := range states {
		if s.Stage() == "" {
			t.Errorf("%s has no stage", s)
		}
	}
}

func TestBranchName(t *testing.T) {
	got := BranchName(DefaultBranchTemplate, 42, "bugfix", "Fix: /health returns 500 when the DB is down!")
	if got != "tfy/u42-fix-health-returns-500-when-the-db-is" {
		t.Errorf("got %q", got)
	}
	if BranchName("", 1, "chore", "¡¿!") != "tfy/u1-unit" {
		t.Error("empty slugs fall back to 'unit', and an empty template to the default")
	}
	if got := BranchName("{kind}/{seq}-{slug}", 7, "feature", "Add dark mode"); got != "feature/7-add-dark-mode" {
		t.Errorf("custom template: got %q", got)
	}
	for _, bad := range []string{"fix/{slug}", "a b/{seq}", "/x/{seq}", "x//{seq}", "x/{seq}/", "x/{seq}-{title}", "x/../{seq}"} {
		if ValidBranchTemplate(bad) {
			t.Errorf("%q should be rejected", bad)
		}
		if got := BranchName(bad, 3, "chore", "Tidy"); got != "tfy/u3-tidy" {
			t.Errorf("an invalid template %q must fall back to the default, got %q", bad, got)
		}
	}
}

func TestParseKind(t *testing.T) {
	if k, _ := ParseKind(""); k != KindFeature {
		t.Error("default kind is feature")
	}
	if k, _ := ParseKind(" BugFix "); k != KindBugfix {
		t.Error("kinds are case-insensitive")
	}
	if _, err := ParseKind("epic"); err == nil {
		t.Error("unknown kinds are rejected")
	}
}

func TestProjectSettingsDefaults(t *testing.T) {
	s := ParseProjectSettings(`{"merge_method":"yolo","draft_prs":true}`)
	if s.MergeMethod != "squash" || !s.DraftPRs || s.MaxReviewIterations != 2 {
		t.Errorf("settings = %+v", s)
	}
}
