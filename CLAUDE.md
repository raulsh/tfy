# tfy (thefactory)

tfy takes units of work from feedback to merged pull requests by driving the Claude Code CLI headless. It is one Go binary (cobra, Fiber v3, SQLite via sqlc and goose) with the React UI (antd 5, TanStack Query) embedded.

## Commits

- Every commit message is a single line: no body, no trailers.
- Use Conventional Commits: `<type>(<optional scope>): <summary>`, for example `feat: suggest convention updates after each unit` or `fix(guard): catch pushes through git -C`. Types: feat, fix, refactor, perf, test, docs, build, ci, chore, revert.
- Never add a `Co-Authored-By` trailer or any other attribution. `.claude/settings.json` turns Claude Code's attribution off, and `.claude/hooks/check-commit.sh` checks every new commit.

## Commands

- `make build`: the UI, then `bin/tfy` with the UI embedded.
- `make test`: `go test -race ./...`, `tsc --noEmit` and Biome. The pipeline tests take about 100s under `-race`.
- `make lint`: gofmt, go vet and Biome. Fix UI formatting with `pnpm -C ui exec biome check --write .`.
- `make generate`: sqlc, after editing `internal/store/queries` (sqlc lives in `~/go/bin`).
- `make demo`: the whole pipeline against fake `claude`, `gh` and `slk` on port 7430.

## Working on tfy

- `docs/spike.md` records the Claude Code CLI behaviour the runner relies on. Check it again when the CLI changes, and add what you learn.
- Agents must never be able to publish. Keep every layer: the guard hook (`internal/guard`), the deny rules, the scrubbed environment, checkouts without remotes, and the monitor that aborts a run. A change that weakens one needs a test that shows the others still hold.
- Every run passes an explicit `--tools` list; the CLI's default set includes tools no run should have.
- Conventions belong to Claude Code, not to tfy: repositories keep them in `CLAUDE.md`, `.claude/rules` and `.claude/settings.json`, and tfy makes its runs load them natively. Don't build a parallel mechanism for something Claude Code already does.
- Pipeline tests drive real git against fake executables (`internal/pipeline/pipeline_test.go`); add one for every new stage or action.
