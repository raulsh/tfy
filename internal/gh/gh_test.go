package gh

import (
	"errors"
	"fmt"
	"testing"
)

// gh refuses a merge the base branch's rules forbid before asking GitHub,
// and suggests --admin; other failures do not.
func TestRulesRefused(t *testing.T) {
	refused := &Error{Args: []string{"pr", "merge", "30"}, Stderr: "X Pull request acme/app#30 is not mergeable: the base branch policy prohibits the merge.\n" +
		"To have the pull request merged after all the requirements have been met, add the `--auto` flag.\n" +
		"To use administrator privileges to immediately merge the pull request, add the `--admin` flag.\n"}
	if !RulesRefused(refused) || !RulesRefused(fmt.Errorf("merge acme/app #30: %w", refused)) {
		t.Error("a refusal by the branch's rules must be recognized, wrapped or not")
	}
	for _, err := range []error{
		&Error{Args: []string{"pr", "merge", "30"}, Stderr: "GraphQL: Head branch was modified. Review and try the merge again."},
		errors.New("exit status 1"),
		nil,
	} {
		if RulesRefused(err) {
			t.Errorf("%v is not a refusal by the branch's rules", err)
		}
	}
}
