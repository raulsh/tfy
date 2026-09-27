#!/usr/bin/env bash
# Runs thefactory against fake claude and gh executables (the pipeline test
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
go build -o "$DEMO/thefactory" ./cmd/thefactory

export THEFACTORY_HOME="$DEMO/home"
export THEFACTORY_CLAUDE_BIN="$DEMO/fake-claude"
export THEFACTORY_GH_BIN="$DEMO/fake-gh"
# Slack channel histories are read from $DEMO/control/slack/<channel id>.json.
export THEFACTORY_SLK_BIN="$DEMO/fake-slk"
export FAKE_REMOTES="$DEMO/remotes"
export FAKE_GH_STATE="$DEMO/gh-state"
echo "demo data in $DEMO (link acme/app to a project to try the pipeline)"
exec "$DEMO/thefactory" serve --port "$PORT" "$@"
