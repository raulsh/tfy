#!/usr/bin/env bash
# Runs tfy against fake claude and gh executables (the pipeline test
# helpers) and a local "GitHub" remote: the whole pipeline works, instantly
# and for free. For UI work and demos; nothing leaves the machine.
set -euo pipefail
ROOT=$(cd "$(dirname "$0")/.." && pwd)
DEMO=${DEMO_DIR:-$ROOT/tmp/demo}
PORT=${PORT:-7430}
cd "$ROOT"

rm -rf "${DEMO:?}"
mkdir -p "$DEMO/remotes/acme" "$DEMO/gh-state"
go test -c -o "$DEMO/fakes" ./internal/pipeline
# Agent runs get a scrubbed environment, so the fake's settings are baked
# into its wrapper rather than inherited.
# Queue reviewer verdicts with: echo request_changes > $DEMO/control/verdicts
FAKE_DELAY=${FAKE_DELAY:-4s}
mkdir -p "$DEMO/control"
for tool in claude gh slk; do
	printf '#!/bin/sh\nFAKE_TOOL=%s FAKE_DELAY=%s FAKE_CONTROL=%s exec "%s" "$@"\n' "$tool" "$FAKE_DELAY" "$DEMO/control" "$DEMO/fakes" > "$DEMO/fake-$tool"
	chmod +x "$DEMO/fake-$tool"
done
git init -q -b main "$DEMO/seed"
git -C "$DEMO/seed" -c user.name=demo -c user.email=demo@example.com commit -q --allow-empty -m "init"
git clone -q --bare "$DEMO/seed" "$DEMO/remotes/acme/app.git"
# A second repository, for changes that merge in order. To try one, write the
# plan run's output to $DEMO/control/plan.json, e.g.
#   {"summary": "…", "target_repos": ["api", "app"], "acceptance_criteria": [], "new_dependencies": []}
# and the merge run's decisions to $DEMO/control/merges, one per line, e.g.
#   {"action": "merge", "repos": ["api"], "wait_for": "", "reason": "api goes first."}
#   {"action": "update", "repos": ["app"], "wait_for": "", "reason": "Pin the merged api."}
# (with no more decisions, it merges whatever is open).
git clone -q --bare "$DEMO/seed" "$DEMO/remotes/acme/api.git"
# Open issues on the fake GitHub, to create units from or link.
cat > "$DEMO/gh-state/issues_acme_app.json" <<'JSON'
[{"number": 3, "title": "Checkout crashes on Safari", "state": "OPEN", "url": "https://github.com/acme/app/issues/3",
  "author": {"login": "ana"}, "labels": [{"name": "bug"}], "updatedAt": "2026-09-27T10:00:00Z",
  "body": "Pressing Pay on Safari leaves a blank page.",
  "comments": [{"author": {"login": "bo"}, "body": "Same on my iPad.", "createdAt": "2026-09-27T11:00:00Z"}]},
 {"number": 5, "title": "Dark mode for the dashboard", "state": "OPEN", "url": "https://github.com/acme/app/issues/5",
  "author": {"login": "cy"}, "labels": [{"name": "enhancement"}], "updatedAt": "2026-09-26T09:00:00Z",
  "body": "The dashboard is too bright at night.", "comments": []}]
JSON
# What "Suggest improvements" answers in the demo.
cat > "$DEMO/control/issue.json" <<'JSON'
{"worth_updating": true, "reason": "The agreed requirement says which browsers are affected and what must happen instead.",
 "comment": "Scope agreed for the fix: payment must work on Safari 17 on macOS and iPadOS. Pressing Pay opens the payment form; no blank page, no console errors.",
 "title": "Checkout shows a blank page after pressing Pay on Safari 17",
 "body": "Pressing Pay on Safari leaves a blank page.\n\n### Affected\nSafari 17 on macOS and iPadOS.\n\n### Expected\nThe payment form opens."}
JSON
go build -o "$DEMO/tfy" ./cmd/tfy

export TFY_HOME="$DEMO/home"
export TFY_CLAUDE_BIN="$DEMO/fake-claude"
export TFY_GH_BIN="$DEMO/fake-gh"
# Slack channel histories are read from $DEMO/control/slack/<channel id>.json.
export TFY_SLK_BIN="$DEMO/fake-slk"
export FAKE_REMOTES="$DEMO/remotes"
export FAKE_GH_STATE="$DEMO/gh-state"
echo "demo data in $DEMO (link acme/app to a project to try the pipeline)"
exec "$DEMO/tfy" serve --port "$PORT" "$@"
