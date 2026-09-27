package guard

import "testing"

func TestCheckCommand(t *testing.T) {
	cases := []struct {
		cmd   string
		allow bool
	}{
		// ordinary work
		{"git status --short", true},
		{"git add -A && git commit -m 'add push notifications'", true},
		{`git commit -m "fix: don't push on save"`, true},
		{"git log --grep push --oneline", true},
		{"git -C repo-a diff base/main...HEAD", true},
		{"git merge base/main", true},
		{"git fetch", true},
		{"git remote -v", true},
		{"git config user.email", true},
		{"git config --get remote.origin.url", true},
		{"git config get remote.origin.url", true},
		{"git config set remote.origin.url https://x", false},
		{"go test ./... | tail -20", true},
		{"npm install && npm test", true},
		{"rg -n push src/", true},
		{"echo push", true},
		{"cat <<'EOF' > notes.md\ngit push is done by the orchestrator\nEOF", true},

		// pushes in every disguise the spike and the critique found
		{"git push", false},
		{"git push origin main", false},
		{"git -C . push origin main", false},
		{"git -C repo-a push", false},
		{"git --git-dir=.git --work-tree=. push", false},
		{"git -c core.editor=true push", false},
		{`sh -c "git push origin main"`, false},
		{`bash -lc 'cd repo-a && git push'`, false},
		{"eval git push", false},
		{`eval "git push -f"`, false},
		{"env GIT_TRACE=1 git push", false},
		{"timeout 30 git push", false},
		{"nohup git push &", false},
		{"cd repo-a && git push", false},
		{"make build; git push", false},
		{"echo ok || git push", false},
		{"(cd repo-a; git push)", false},
		{"echo $(git push)", false},
		{"echo `git push`", false},
		{"G=git; $G push", false},
		{"/usr/bin/git push", false},
		{"xargs -n1 git push < repos.txt", false},
		{"git send-pack origin", false},

		// remotes, credentials, and aliases
		{"git remote add origin https://github.com/x/y", false},
		{"git remote set-url origin git@github.com:x/y", false},
		{"git config --global user.name x", false},
		{"git config remote.origin.url https://x", false},
		{"git config alias.p push", false},
		{"git config credential.helper store", false},
		{"git -c credential.helper=store fetch", false},
		{"git -c url.https://x.insteadOf=https://y fetch", false},

		// GitHub CLI
		{"gh pr create --fill", false},
		{"gh auth token", false},
		{"cd x && gh pr merge 1", false},
	}
	for _, c := range cases {
		got := CheckCommand(c.cmd)
		if got.Allow != c.allow {
			t.Errorf("CheckCommand(%q) allow = %v, want %v (reason %q)", c.cmd, got.Allow, c.allow, got.Reason)
		}
		if !got.Allow && got.Reason == "" {
			t.Errorf("CheckCommand(%q) denied without a reason", c.cmd)
		}
	}
}

func TestCheckOnlyInspectsShellTools(t *testing.T) {
	var in Input
	in.ToolName = "Write"
	in.ToolInput.Command = "git push"
	if !Check(in).Allow {
		t.Fatal("non-shell tools must pass through")
	}
	in.ToolName = "Monitor"
	if Check(in).Allow {
		t.Fatal("Monitor runs shell commands and must be checked")
	}
}
