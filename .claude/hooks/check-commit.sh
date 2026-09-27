#!/bin/sh
# PostToolUse hook for Bash: when a command has just made a commit, check its
# message against the rules in CLAUDE.md (single line, Conventional Commits,
# no attribution). Exit 2 feeds the problems back to Claude, which amends.
input=$(cat)
case "$input" in *commit*) ;; *) exit 0 ;; esac
cd "${CLAUDE_PROJECT_DIR:-.}" 2>/dev/null || exit 0
at=$(git log -1 --format=%ct 2>/dev/null) || exit 0
# Only a commit this command made: older ones are not ours to judge.
[ $(($(date +%s) - at)) -le 60 ] || exit 0

msg=$(git log -1 --format=%B)
subject=$(printf '%s\n' "$msg" | head -n 1)
problems=""
if [ "$(printf '%s\n' "$msg" | grep -c .)" -gt 1 ]; then
	problems="$problems
- it has more than one line; keep only the subject"
fi
if printf '%s\n' "$msg" | grep -qi 'co-authored-by'; then
	problems="$problems
- it has a Co-Authored-By trailer"
fi
if ! printf '%s\n' "$subject" | grep -Eq '^(feat|fix|refactor|perf|test|docs|build|ci|chore|revert)(\([a-z0-9._/-]+\))?!?: [^ ]'; then
	problems="$problems
- the subject is not a Conventional Commit: <type>(<optional scope>): <summary>"
fi
[ -z "$problems" ] && exit 0
printf 'The commit just made (%s) breaks the commit rules in CLAUDE.md:%s\nAmend it: git commit --amend -m "<type>: <summary>"\n' "$(git log -1 --format=%h)" "$problems" >&2
exit 2
