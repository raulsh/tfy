package pipeline

// Agents have no credentials, so a project's own repositories — which a
// change in one often depends on in another — would be out of their reach
// as dependencies. Each run in a unit's workspace gets a git config that
// serves them from the unit's checkouts instead: git URLs of
// github.com/<owner>/<repo> resolve to the checkouts, which hold the unit's
// branches, the default branch as last fetched, and tags. Go modules, npm
// and pip git dependencies all fetch through git, so `go get
// github.com/acme/api@<commit>` works for a commit that was merged a minute
// ago, or that only exists on the unit's branch. Pushes through those URLs
// go nowhere, and the guard blocks pushes anyway.

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"slices"
	"strings"

	"github.com/raulsh/tfy/internal/store/db"
)

// mirrorURLForms are the ways a GitHub repository is written in a git URL.
var mirrorURLForms = []string{
	"https://github.com/%s", "https://github.com/%s.git",
	"git@github.com:%s", "git@github.com:%s.git",
	"ssh://git@github.com/%s", "ssh://git@github.com/%s.git",
}

// writeRunGitConfig writes the git config of a run in a unit's workspace:
// the commit identity, and the checkouts as the source of the project's
// repositories. It returns environment entries that make Go fetch them with
// git rather than through a module proxy.
func (p *Pipeline) writeRunGitConfig(ctx context.Context, path string, urs []db.ListUnitReposRow) ([]string, error) {
	identity, err := os.ReadFile(p.Paths.GitConfig())
	if err != nil {
		return nil, err
	}
	var b strings.Builder
	b.Write(identity)
	b.WriteString("\n# The unit's checkouts serve the project's repositories (written by tfy).\n")
	var modules []string
	for _, ur := range urs {
		if _, err := os.Stat(filepath.Join(ur.CheckoutPath, ".git")); err != nil {
			continue
		}
		// A fetch may then name any commit the checkout holds, such as a
		// merge commit behind the tip of its default branch.
		if err := p.Git.SetConfig(ctx, ur.CheckoutPath, "uploadpack.allowReachableSHA1InWant", "true"); err != nil {
			return nil, err
		}
		fmt.Fprintf(&b, "[url %s]\n", gitConfigQuote("file://"+ur.CheckoutPath))
		for _, form := range mirrorURLForms {
			fmt.Fprintf(&b, "\tinsteadOf = %s\n", fmt.Sprintf(form, ur.FullName))
		}
		modules = append(modules, "github.com/"+ur.FullName)
	}
	b.WriteString("[url \"file:///dev/null/tfy-pushes-are-disabled/\"]\n")
	for _, prefix := range []string{"https://github.com/", "git@github.com:", "ssh://git@github.com/"} {
		fmt.Fprintf(&b, "\tpushInsteadOf = %s\n", prefix)
	}
	if err := os.WriteFile(path, []byte(b.String()), 0o644); err != nil {
		return nil, err
	}
	return goPrivateEnv(modules), nil
}

// goPrivateEnv adds modules to GOPRIVATE, and to GONOPROXY and GONOSUMDB
// when those are set (they override it): the proxy and the checksum
// database cannot know a commit that is only in a checkout.
func goPrivateEnv(modules []string) []string {
	if len(modules) == 0 {
		return nil
	}
	add := func(key string) string {
		var list []string
		for _, v := range strings.Split(os.Getenv(key), ",") {
			if v = strings.TrimSpace(v); v != "" && !slices.Contains(list, v) {
				list = append(list, v)
			}
		}
		for _, m := range modules {
			if !slices.Contains(list, m) {
				list = append(list, m)
			}
		}
		return key + "=" + strings.Join(list, ",")
	}
	env := []string{add("GOPRIVATE")}
	for _, key := range []string{"GONOPROXY", "GONOSUMDB"} {
		if os.Getenv(key) != "" {
			env = append(env, add(key))
		}
	}
	return env
}

// gitConfigQuote quotes a config subsection name.
func gitConfigQuote(s string) string {
	return `"` + strings.NewReplacer(`\`, `\\`, `"`, `\"`).Replace(s) + `"`
}
