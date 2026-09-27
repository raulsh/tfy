# tfy (thefactory)

tfy, short for thefactory, takes a **unit** of work (a feature, bug fix, improvement or chore) from an idea to a merged pull request. Every step is carried out by [Claude Code](https://claude.com/claude-code), running headless on your machine.

```
 feedback ─▶ definition ─▶ planning ─▶ executing ─────────────────▶ release
 (Slack)     requirement    spec with    develop → PR(s) → review     CI, notes
             you approve    acceptance   → you merge
                            criteria
```

- **Definition.** Claude writes the requirement: what is needed, and why. You edit it, or ask for a revision, then mark it ready.
- **Planning.** Claude reads the linked repositories and writes a spec with numbered acceptance criteria. You approve it.
- **Executing.** Claude implements the spec in isolated checkouts and commits locally. tfy pushes the branches and opens the pull requests, across as many repositories as the spec touches.
- **Review.** A reviewer run checks each acceptance criterion against the diff. If any criterion is unmet or a finding blocks, the work goes back to development automatically, for a limited number of rounds.
- **Merge.** You merge from the UI. tfy first checks that no pull request changed since the review and none conflicts. You can also merge on GitHub; tfy notices.
- **Release.** Claude writes release notes for users, and tfy follows CI on each merge commit. If CI fails, it offers a follow-up bugfix unit.
- **Feedback.** Units can also start from Slack. tfy polls your channels through `slk` and triages every new message:
  - actionable ones become **proposals** for you to accept;
  - messages about tracked work are attached to it;
  - the rest is set aside as noise.

  You can also pick messages in the Inbox and turn them into a unit yourself.

It runs locally. `tfy serve` is the backend and serves the UI on `127.0.0.1`.

## Requirements

- [Claude Code](https://claude.com/claude-code) 2.1+, signed in (`claude auth login`)
- [GitHub CLI](https://cli.github.com), signed in (`gh auth login`)
- git
- [slk](https://github.com/raulsh/slk), signed in (`slk configure`); optional, needed only for Slack feedback intake
- To build: Go 1.26 and pnpm

## Quick start

```sh
make install            # builds the UI and the binary into ~/.local/bin
tfy init         # creates ~/.tfy (moves ~/.thefactory there, if you used the old name)
tfy doctor       # checks claude, gh, git, slk
tfy serve --open # prints a link with a token, and opens it
```

1. Create a project, link its GitHub repositories, and optionally its Slack channels (Projects → Slack).
2. Create a unit, or accept a proposal from the Inbox, and follow its runs live.
3. At each gate, decide:
   - mark the requirement ready;
   - approve the spec;
   - merge once the review approves.

To try the whole pipeline without spending tokens or touching GitHub, run `make demo`. It serves on port 7430 against fake `claude` and `gh` and a local "GitHub". Link `acme/app` to a project to start.

## Conventions

Projects want consistency: how commits are written, how pull requests are opened and described, how code is tested. Claude Code already knows how to follow such rules, so tfy does not reinvent them. It keeps them where Claude Code reads them, and makes its runs load them the way a session started in the repository would.

- **In each repository**, versioned and reviewed with the code, so people using Claude Code by hand follow the same rules:
  - `CLAUDE.md` for what every session must know, and `.claude/rules/*.md` for topics or areas (`paths:` frontmatter scopes a rule to matching files);
  - hooks in `.claude/settings.json`, with scripts in `.claude/hooks/`, for checks that must never be skipped, like rejecting a commit message that breaks the rules;
  - `attribution` in `.claude/settings.json`; where a repository doesn't set it, tfy's runs add no `Co-Authored-By` trailer;
  - the pull request template, `.github/pull_request_template.md`.
- **Across a project**, for rules its repositories share: the project's Conventions tab in tfy.

Before every run, tfy writes the unit workspace's `CLAUDE.md`, which holds the project's conventions and imports each checkout's `CLAUDE.md` and always-on rules, and its `.claude/settings.json`, with the repositories' hooks and attribution. Claude Code does the rest itself: path-scoped rules, nested `CLAUDE.md` files and imports. Claude writes the commit messages and the pull request title and description following those conventions; tfy only pushes and opens the pull request with them. Branch names follow the project's template (Pipeline tab, `tfy/u{seq}-{slug}` by default).

Hooks run outside the permission system, so tfy only takes them from the default branch, the reviewed version. It leaves a repository's hooks out of a run when that checkout's `.claude/hooks` differs from the default branch, and says so in the unit's activity.

**They improve unit after unit.** When a unit is done, a retrospective run looks at what went back and forth: review rounds, what people asked to change, comments on the pull requests, refused commands, failed CI. If that shows a gap in the conventions, it opens a unit (origin *retrospective*) proposing the change to `CLAUDE.md`, a rule, a hook or the pull request template. You accept or reject it like any proposal, and an accepted one lands as a reviewed pull request in the repository. The Conventions tab shows what each repository has today, and **Propose a change** starts such a unit by hand. Turn retrospectives off per project in the Pipeline tab.

## Changes that merge in order

Some changes across repositories cannot merge at once. A shared module has to be merged before the repositories that use it can pin its merged commit. A service has to be deployed before the clients that need it. The plan run then adds a **merge plan** to the spec: ordered steps, each a set of repositories merged together. For each step after the first it gives:

- **what the step waits for** from the steps before it: `merged`; `released`, meaning CI (including any deploy) is green on their merge commits; or `tagged`, meaning a tag contains their merge commits, for dependencies consumed by version;
- **an update** to make first, if any, such as "pin github.com/acme/api to the commit step 1 merged".

You approve the plan with the spec. The whole change is developed and reviewed at once, as usual. Then:

1. **Merge** merges the current step only.
2. tfy waits for what the next step needs, reusing the release stage's CI tracking.
3. For an update, a short development round changes just that step's repositories, and it is reviewed on its own.
4. **Merge** is offered for the next step. **Continue to step N** skips a wait you don't need.

Release notes and the retrospective come once, after the last step. Without a merge plan, everything merges together as before.

**The unit's repositories are available as dependencies.** Agents have no credentials, so private repositories would be out of reach as dependencies. Each run's git config serves `github.com/<owner>/<repo>` URLs from the unit's checkouts, which hold its branches, the default branch as last fetched, and tags. tfy also adds them to `GOPRIVATE`. `go get github.com/acme/api@<commit>`, or an npm or pip git dependency, then works for a commit merged a minute ago or one that only exists on the unit's branch. Pushes through those URLs go nowhere, and the guard blocks pushes anyway.

## GitHub issues

A unit can be linked to GitHub issues: create it from one (New unit → GitHub issue, or paste a link), or link it on the unit page.

- **Claude reads them.** The define run gets each issue and its comments, fenced as information rather than instructions. Every later run finds the full text in the workspace's `docs/issues/`. Agents have no GitHub access, so tfy reads the issues with your `gh`.
- **GitHub links and closes them itself.** Each pull request references the unit's issues: `Closes #12`, so merging closes the issue, or `Refs #12` when you switch off *closes on merge* for that issue. tfy doesn't repeat a reference Claude already wrote. For an issue that must stay open, it turns Claude's `Fixes #12` into `addresses #12`.
- **tfy can suggest how an issue could say more.** **Suggest improvements** weighs the issue against what tfy has gathered: the Slack reports behind the unit, the requirement and spec, the pull requests and the release notes. Claude answers either "nothing to add", or a comment and, when the issue is unclear, a better title and description. You edit the suggestion and post it with your account, or dismiss it.
  - For public repositories the check leaves out who said what in Slack, and anything internal.
  - An edit is refused if the issue changed on GitHub after the check read it.
  - The Pipeline tab can run the check automatically once a requirement is marked ready. It still posts nothing on its own.

## How agents are kept in their lane

Each Claude run gets only what its stage needs, and several layers stop it from publishing on its own:

- **Isolated checkouts.** Every unit works in its own `git clone --local` checkouts, **with no remote**. Only tfy pushes and opens pull requests, using your credentials.
- **No publishing credentials.** Runs start from a scrubbed environment: no `GH_TOKEN`, no SSH agent, an empty `gh` configuration, and a gitconfig with no credential helper.
- **A guard hook.** Every shell command passes through `tfy hook-guard`, which parses it and blocks pushes, remote and credential changes, and `gh`. It catches `git -C`, `sh -c`, `eval`, wrappers, and command substitutions. If the guard fails to run, the run is aborted, because the CLI itself would fail open.
- **Isolated settings.** A unit's runs load only the workspace as a Claude Code project (`--setting-sources project`), whose settings tfy rewrites before every run; user settings, MCP servers (strict, none) and anything an agent left in the workspace's `.claude` stay out. Each run has an explicit tool list and a permission mode per stage:

  | Stage | Mode | Access |
  |---|---|---|
  | define | `dontAsk` | writes `docs/` only |
  | plan | `dontAsk` | writes `docs/` only; read-only commands |
  | develop | `auto` | the checkouts |
  | review, retrospective | `dontAsk` | read-only commands |

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
| `cmd/tfy` | Entry point. |
| `internal/claude` | Runs `claude -p`: args, isolated env, stream parser, and a safety monitor that aborts on guard failures, the wrong permission mode, or pushes. |
| `internal/guard` | The PreToolUse guard: a shell parser plus rules. |
| `internal/pipeline` | Every stage (triage, define, plan, develop, publish, review, merge, release), managed clones and checkouts, the pollers, and restart recovery. |
| `internal/slack` | The `slk` adapter. It reads the `--json` envelope, so truncation is never silent. |
| `internal/jobs` | SQLite-backed job queue: one active job per unit, a boot sweep, and rate-limit pauses. |
| `internal/store` | SQLite with goose migrations; sqlc queries live in `queries/`. |
| `internal/api` | Fiber v3 JSON API, SSE streams, and the embedded UI. |
| `ui` | React 19, antd 5, TanStack Query. The design follows groundcover. |

Data lives in `~/.tfy`, or `$TFY_HOME`:

- `tfy.db`, the database
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
