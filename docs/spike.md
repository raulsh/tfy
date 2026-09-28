# M0 spike — Claude Code CLI behaviour (2.1.283)

Run on 2026-09-27 against a local sandbox: a unit-style workspace with two git repos, plus a local bare `origin` so any push that slipped through stayed harmless. The model was `sonnet` (claude-sonnet-5) at `--effort low`. Auth was a subscription (`apiKeySource: "none"`). Sanitized stream fixtures are in `internal/claude/testdata/`.

## Answers to the plan's open questions

| # | Question | Answer | Consequence |
|---|---|---|---|
| 1 | Does `--setting-sources ""` skip CLAUDE.md? | **Yes.** The canary in `CLAUDE.md` came back UNKNOWN with `""` and was found with `project` (`q1-*`). | At first: keep `""` and inject each repo's `CLAUDE.md`/`AGENTS.md` into `--append-system-prompt`. **Superseded** by native loading; see "Conventions" below. |
| 2 | Do `--settings` deny rules and hooks apply with empty setting sources? Do path-scoped allow rules work? | **Yes to all.** Hooks fire and deny rules apply. `Write(./docs/**)` in `dontAsk` allowed `docs/note.md` and denied `repo-b/note.md` (`q2-edit`). | Define and plan profiles can write only to `docs/`. |
| 2b | Are deny rules enough to stop pushes? | **No.** `Bash(git push *)` blocked `git push origin main`, but `git -C . push origin main` **ran** (`q2-denyonly`). | The guard hook is the primary control; deny rules are only a second layer. |
| 3 | Does the guard see `git -C x push`, `sh -c "git push"`, and `Monitor`? | **Yes.** With matcher `Bash\|Monitor`, the hook received all of them; `tool_input.command` holds the raw string (`q2-deny`). | The guard must parse `-C`, `sh -c`, env prefixes and compound commands itself. |
| 3b | What happens if the guard can't start? | **It fails open.** A missing binary gives `hook_response.exit_code: 127, outcome: "error"`, and the tool call **still ran** (`q9-missinghook`). | The runner aborts a run on any PreToolUse `hook_response` whose `exit_code` isn't 0 or 2. The guard binary lives at a stable path (`~/.tfy/bin`). |
| 4 | `autoMode.environment` with `$defaults`, and the init field for the permission mode | The settings containing `["$defaults", "Trusted repo: …"]` were accepted, since hooks still fired. The init field is **`permissionMode`** (`"auto"`). | The runner asserts that `init.permissionMode` equals the requested mode. |
| 4b | Does auto mode work across sibling repos from a parent cwd that isn't a repo? | **Yes, in both variants.** Claude edited and committed in both repos with or without the environment entry (`q4-auto-*`). The classifier didn't block anything. | Keep the unit folder as the cwd and keep the trust entry (cheap insurance). |
| 5 | Does `--json-schema` work with `--tools ""` and with other tools? | **Yes.** It adds a `StructuredOutput` tool. The result carries **`structured_output`** (an object), and `result` holds the same JSON as text (`q5-*`). | Read `structured_output`; fall back to parsing `result`. |
| 6 | Is `total_cost_usd` cumulative across `--resume`? | **Yes, even with `--fork-session`.** It went $0.0035 → $0.0049 → $0.0064 along the chain, and `modelUsage` tokens accumulate too (`q6-*`). | `runs.cost_usd` = reported total − the parent run's reported total. |
| 7 | Does resume require the same cwd? | **No.** `--resume <id>` from another cwd found the session. Transcripts live at `~/.claude/projects/<cwd-slug>/<sid>.jsonl`, and `--session-id` is honored. | A fixed workspace is kept anyway, for auto mode's trust scope and the doc paths. |
| 8 | Prompt on stdin, and behaviour on SIGINT and SIGTERM | Stdin works. **SIGINT** gives exit 0 plus a `result` event with `subtype: "error_during_execution"`. **SIGTERM** gives exit 143 (`q8-*`). | Cancel sends SIGINT first. A cancelled run is identified by our own cancel flag, not by the exit code. |

## Other findings that shape the implementation

- **Tool list:** 2.1.283 has no `Glob`, `Grep` or `TodoWrite` tools. By default it exposes `CronCreate`, `RemoteTrigger`, `PushNotification`, `ScheduleWakeup`, `SendMessage`, `Workflow`, `EnterWorktree`, `DesignSync`, `ShareOnboardingGuide` and others. Every run passes an explicit `--tools` list.
- **Read-only commands in dontAsk:** `ls`, `rg`, `cat` and `find` (without `-exec`) are auto-allowed with only `Read` in the allow list, while `touch` and `git -C … log` are denied (`q5-ro`). Plan and review runs explore with those commands, and the orchestrator **precomputes the review diffs into files** under `docs/`.
- **`rate_limit_event` arrives on every run** with `status: "allowed"` and `unifiedWindows.{five_hour,seven_day}.utilization`/`resetsAt`. Pause only on a status other than `allowed`, and surface the utilization in the UI as quota.
- **Useful system events:** `hook_started`/`hook_response` (need `--include-hook-events`), `permission_denied` (`decision_reason_type`: mode / rule / subcommandResults), `vcs_state_changed` (`kind`: commit / push; a push during a develop run is an alarm), `task_started`/`task_notification`/`background_tasks_changed`, and `thinking_tokens`.
- **Background tasks:** after `result`, the CLI lingers about 6s, kills its background tasks and exits (`q9-linger`). The runner adds a post-result grace timer (30s), then cancels.
- **Standalone long `sleep`s are blocked** by Claude Code itself, which suggests `run_in_background`.
- **`--disable-slash-commands`** removes the built-in skills (deep-research, design, …) from the prompt; use it for every run.
- **Commit trailers:** in auto mode Claude may add a `Co-Authored-By: Claude …` trailer to its commits on its own.
- **Result fields:** `subtype`, `is_error`, `num_turns`, `total_cost_usd`, `usage`, `modelUsage`, `permission_denials[]`, `terminal_reason`, `stop_reason`, `api_error_status`, `session_id`, `duration_ms`, and `structured_output` when a schema is set.

## End-to-end check (M0 exit)

`tfy dev claude --profile develop --trust repo-a,repo-b` ran against a unit-style workspace (a parent folder that is not a repo, two checkouts with **no remotes**), with the Go guard (`tfy hook-guard`) wired in through generated settings:

- The session started in `permissionMode: "auto"`.
- Claude committed in both repos without being prompted.
- `git -C repo-a push <bare-repo-path> HEAD:main` was blocked by the guard (exit 2, our message fed back to Claude), and the bare repo was unchanged.
- The run succeeded: 6 turns, $0.08 on sonnet at low effort.

Found along the way: **the Bash tool's working directory persists between calls**. After a `cd repo-a`, a later `cd repo-b` failed. Runs now set `CLAUDE_BASH_MAINTAIN_PROJECT_WORKING_DIR=1`; see "The shell's directory and path rules" below.

## First real run (2026-09-27)

One bugfix unit went through every stage against `raulsh/thefactory-sandbox` (private), with the default models: opus at high effort for define, plan, develop and review, and sonnet at low effort for the release notes.

| Stage | Duration | Cost | Result |
|---|---|---|---|
| define | 24s | $0.09 | Requirement with six testable requirements, stated assumptions and open questions. |
| plan | 74s | $0.28 | Spec with exact changes, a test plan and seven acceptance criteria. One `dontAsk` denial (a compound shell loop); Claude used Read instead. |
| develop | 51s | $0.26 | `permissionMode: auto`. Five shell commands, all allowed by the guard. Tests run, the binary built and curled, one local commit. |
| publish | | | Pushed over SSH, PR #1 opened with the acceptance-criteria checklist. CI green. |
| review, round 1 | | | Every criterion met or not verifiable, no defects, yet "request_changes". **Bug:** the reviewer cannot run tests and blocked on AC-7 ("gofmt/go test pass"). Fixed: only unmet or partial criteria, or blocker or major findings, send work back. The reviewer now gets the PR's CI state and the implementer's test report. |
| develop, round 2 and review, round 2 | | $0.28 | No changes needed; approved. |
| merge | | | From the API: preflight, then `gh pr merge --squash --match-head-commit --delete-branch`. |
| release | 5s | $0.01 | Notes from sonnet. CI on the merge commit was followed until green; unit done. |

Total: about $1.16 and 7 minutes of agent time, including the unnecessary round.

## Conventions: native loading (2026-09-27)

Checked with sonnet at low effort, against a unit-style workspace (a parent folder that is not a repo) holding one checkout. Canary words were planted in each kind of file.

| Question | Answer | Consequence |
|---|---|---|
| With the workspace as cwd and `--setting-sources project`, what loads at start? | The workspace's `CLAUDE.md` and its `@imports` (`@app/AGENTS.md` worked). | tfy writes the workspace `CLAUDE.md` before every run: the project's conventions, plus imports of each checkout's `CLAUDE.md`, `.claude/CLAUDE.md` and always-on rules. |
| And a checkout's own `CLAUDE.md` and `.claude/rules`? | **Only once Claude reads a file in the checkout**: then its `CLAUDE.md`, its always-on rules and its path-scoped rules (globs relative to the checkout) all loaded. A path-scoped rule in the workspace's own `.claude/rules` loaded the same way. | Nested loading is triggered by the Read tool, and an agent working through Bash alone might never trigger it. That is why the start-up files are imported eagerly; path-scoped rules are left to Claude Code. |
| Do project-settings hooks and `attribution` apply, next to a `--settings` file? | **Yes.** Hooks from both fired, and `attribution: {"commit": "", "pr": ""}` in the project settings left no trailer on the commit. | The repositories' hooks and attribution go into the workspace's `.claude/settings.json`; tfy's guard, deny rules and default attribution stay in the flag settings. |
| Are a checkout's own `.claude/settings.json` hooks loaded from the parent cwd? | Only the project root's settings load. | tfy merges each repository's hooks, from its default branch, into the workspace settings, wrapped to run from the checkout with `CLAUDE_PROJECT_DIR` set to it. |
| Can a run edit `.claude/rules` and `.claude/settings.json`? | **Not in `dontAsk`**, even with `Edit(./.claude/**)` allowed: both were denied. **In `auto` mode, yes**: Write and Edit both worked. | A unit that changes the conventions goes through the normal develop run, in auto mode. |
| Absolute-path rules | `Write(//abs/path/**)` together with `--add-dir` allowed exactly that folder and denied its sibling. | Available if a run ever needs to write outside its cwd. |

`hook_response` events carry no hook command, so the monitor can't tell the guard from a repository's hooks by name. The guard prints a marker (`tfy-guard`) on stdout, which Claude Code doesn't show the model for PreToolUse hooks. Only responses carrying the marker count as the guard's, and a repository hook's non-blocking error doesn't abort the run.

## The shell's directory and path rules (2026-09-27)

Seen for real: plan runs on a ten-repository project ended without `docs/spec.md`. Both Write calls were denied in `dontAsk` (`decision_reason_type: "mode"`), even though the run had `Write(./docs/**)`. In every such run, the last shell command before the Write had `cd`'d into a checkout. In a run whose `cd` into a checkout was itself denied, the Write went through.

Reproduced with sonnet at low effort. The workspace is not a repo, `app/` is a checkout, and each run ran `cd app && ls`, then Wrote `docs/x.md`, then ran `pwd`:

| Variant | Write | `pwd` afterwards |
|---|---|---|
| `Write(./docs/**)` | **denied** | `…/ws/app` |
| `Write(./docs/**)` with `CLAUDE_BASH_MAINTAIN_PROJECT_WORKING_DIR=1` | allowed | `…/ws` |
| `Write(//…/ws/docs/**)` (absolute) | allowed | `…/ws/app` |

So the CLI resolves `./` rules against the shell's *current* directory, which follows `cd`. Two consequences:

- Every run sets `CLAUDE_BASH_MAINTAIN_PROJECT_WORKING_DIR=1`, so each shell command starts in the run's working directory.
- `Spec.Args` anchors `./` rules to the run's working directory, as `//abs/...`. This holds even if the variable's behaviour changes.

## Dependencies between a project's repositories (2026-09-27)

Seen for real: on a unit where three Go repositories had to pin a new commit of a fourth, private one, the agent had no way to fetch it, because runs have no credentials.
- It first tried a `url.<checkout>.insteadOf` rule of its own. The guard refused it.
- It then wrote a Go program with `golang.org/x/mod/zip` to build the module's zip from the checkout, served that from a file-based `GOPROXY` in `/tmp`, and got a `go.sum` that CI accepted.

That was resourceful, and it should not be needed.

Checked with Go 1.26:
- `go get github.com/acme/mod@<sha>` resolves a private module through a gitconfig with `[url "file:///path/to/checkout"] insteadOf = https://github.com/acme/mod`, given `GOPRIVATE` and no credentials. The pseudo-version and `go.sum` hashes are the same ones GitHub would serve for that commit.
- A `pushInsteadOf` pointing at a path that doesn't exist makes pushes through those URLs fail.

So each run in a workspace gets its own gitconfig: the commit identity, `insteadOf` rules from each checkout's GitHub URLs to the checkout, and `pushInsteadOf` to nowhere. Checkouts set `uploadpack.allowReachableSHA1InWant`, so a fetch can name a merge commit behind a branch tip. tfy refreshes the default branch and tags of every checkout before a development round and before each merge run decision. The merge run can then pin what just merged.

## Merging in order (2026-09-28)

Seen for real on a unit across five repositories: the first version stored a merge plan, as structured steps, in the spec's meta. The unit's spec was written before that existed. Its prose gave a deploy order, the API first and the UI last, but the meta had no plan, so tfy merged all five pull requests within fifteen seconds. Three of them pinned the API module at its unit-branch commit, which the squash merge left out of master. CI failed with `unknown revision 2490d1f8e635`.

So merging is now a run of its own, `merge`: Bash, Read, Write and Edit in auto mode, trusted in the open pull requests' checkouts only, guarded, with the Stop hook. It resumes its session between decisions within one merge and returns one structured decision at a time: merge, update, wait, or blocked. tfy carries out each one and reports back. The order comes from the spec's prose and the repositories themselves, not from a field the plan had to fill in.

Checked with the real CLI (`TestRealClaudeMergeRun`, sonnet at low effort, two Go modules and a squash merge). The merge run took three decisions: merge api; update app, where it ran `go get` on the merge commit through the checkout mirror, built, tested and committed; then merge app, once tfy had pushed the update and a review had approved it. The three decisions cost $0.20 of the unit's $0.70. Resuming the session between decisions works with `--json-schema`, as it does for revisions.
