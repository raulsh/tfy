# thefactory

thefactory takes a **unit** of work (a feature, bug fix, improvement or chore) from an idea to a merged pull request. Every step is carried out by [Claude Code](https://claude.com/claude-code), running headless on your machine.

```
 feedback ─▶ definition ─▶ planning ─▶ executing ─────────────────▶ release
 (Slack)     requirement    spec with    develop → PR(s) → review     CI, notes
             you approve    acceptance   → you merge
                            criteria
```

- **Definition.** Claude writes the requirement: what is needed, and why. You edit it, or ask for a revision, then mark it ready.
- **Planning.** Claude reads the linked repositories and writes a spec with numbered acceptance criteria. You approve it.
- **Executing.** Claude implements the spec in isolated checkouts and commits locally. thefactory pushes the branches and opens the pull requests, across as many repositories as the spec touches.
- **Review.** A reviewer run checks each acceptance criterion against the diff. If any criterion is unmet or a finding blocks, the work goes back to development automatically, for a limited number of rounds.
- **Merge.** You merge from the UI. thefactory first checks that no pull request changed since the review and none conflicts. You can also merge on GitHub; thefactory notices.
- **Release.** Claude writes release notes for users, and thefactory follows CI on each merge commit. If CI fails, it offers a follow-up bugfix unit.
- **Feedback.** Units can also start from Slack. thefactory polls your channels through `slk` and triages every new message:
  - actionable ones become **proposals** for you to accept;
  - messages about tracked work are attached to it;
  - the rest is set aside as noise.

  You can also pick messages in the Inbox and turn them into a unit yourself.

It runs locally. `thefactory serve` is the backend and serves the UI on `127.0.0.1`.

## Requirements

- [Claude Code](https://claude.com/claude-code) 2.1+, signed in (`claude auth login`)
- [GitHub CLI](https://cli.github.com), signed in (`gh auth login`)
- git
- [slk](https://github.com/raulsh/slk), signed in (`slk configure`); optional, needed only for Slack feedback intake
- To build: Go 1.26 and pnpm

## Quick start

```sh
make install            # builds the UI and the binary into ~/.local/bin
thefactory init         # creates ~/.thefactory
thefactory doctor       # checks claude, gh, git, slk
thefactory serve --open # prints a link with a token, and opens it
```

1. Create a project, link its GitHub repositories, and optionally its Slack channels (Projects → Slack).
2. Create a unit, or accept a proposal from the Inbox, and follow its runs live.
3. At each gate, decide:
   - mark the requirement ready;
   - approve the spec;
   - merge once the review approves.

To try the whole pipeline without spending tokens or touching GitHub, run `make demo`. It serves on port 7430 against fake `claude` and `gh` and a local "GitHub". Link `acme/app` to a project to start.

## How agents are kept in their lane

Each Claude run gets only what its stage needs, and several layers stop it from publishing on its own:

- **Isolated checkouts.** Every unit works in its own `git clone --local` checkouts, **with no remote**. Only thefactory pushes and opens pull requests, using your credentials.
- **No publishing credentials.** Runs start from a scrubbed environment: no `GH_TOKEN`, no SSH agent, an empty `gh` configuration, and a gitconfig with no credential helper.
- **A guard hook.** Every shell command passes through `thefactory hook-guard`, which parses it and blocks pushes, remote and credential changes, and `gh`. It catches `git -C`, `sh -c`, `eval`, wrappers, and command substitutions. If the guard fails to run, the run is aborted, because the CLI itself would fail open.
- **Isolated settings.** Runs use `--setting-sources ""`, strict MCP with no servers, an explicit tool list, and a permission mode per stage:

  | Stage | Mode | Access |
  |---|---|---|
  | define | `dontAsk` | writes `docs/` only |
  | plan | `dontAsk` | writes `docs/` only; read-only commands |
  | develop | `auto` | the checkouts |

- **Two human gates.** No code is written until you approve a spec, and nothing merges until you do.
- **Local-only server.** It binds to 127.0.0.1, checks `Host` (against DNS rebinding) and `Origin`, and requires a per-install token.

`docs/spike.md` records how the Claude Code CLI behaved when all of this was checked.

## Development

```sh
make dev    # Go server with --dev on :7420, plus Vite on :5174 with hot reload
make test   # go test -race, tsc, biome
make demo   # full pipeline on fakes
```

| Path | What it holds |
|---|---|
| `cmd/thefactory` | Entry point. |
| `internal/claude` | Runs `claude -p`: args, isolated env, stream parser, and a safety monitor that aborts on guard failures, the wrong permission mode, or pushes. |
| `internal/guard` | The PreToolUse guard: a shell parser plus rules. |
| `internal/pipeline` | Every stage (triage, define, plan, develop, publish, review, merge, release), managed clones and checkouts, the pollers, and restart recovery. |
| `internal/slack` | The `slk` adapter. It reads the `--json` envelope, so truncation is never silent. |
| `internal/jobs` | SQLite-backed job queue: one active job per unit, a boot sweep, and rate-limit pauses. |
| `internal/store` | SQLite with goose migrations; sqlc queries live in `queries/`. |
| `internal/api` | Fiber v3 JSON API, SSE streams, and the embedded UI. |
| `ui` | React 19, antd 5, TanStack Query. The design follows groundcover. |

Data lives in `~/.thefactory`, or `$THEFACTORY_HOME`:

- `factory.db`, the database
- `repos/`, fetch-only bare clones
- `workspaces/<unit>/`, the unit's documents and checkouts
- `config.yaml`, models, budgets and timeouts per stage

## Status

| Milestone | Scope | Status |
|---|---|---|
| M0 | Claude CLI spike and runner core | done |
| M1 | Developer unit → define → plan → develop → PR → merged | done |
| M2 | Reviewer agent with an acceptance-criteria matrix and automatic rounds; merge from the UI with preflight; re-review after outside pushes; reject cleanup and reopen | done |
| M3 | Slack intake through `slk` (polling, thread watch, auto-triage into proposals), Inbox, manual grouping, import by link | done |
| M4 | Release tracking (CI on merge commits), release notes, follow-up units, overview dashboard | done |

Every milestone is covered by integration tests that run the real pipeline against fake `claude`, `gh` and `slk` executables and real git. M0 exercised the real Claude CLI; a full run against real GitHub is the next check.
