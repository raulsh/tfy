// Mirrors internal/api/views.go.

export type Stage = "intake" | "definition" | "planning" | "executing" | "release" | "done" | "rejected";

export type UnitState =
	| "proposed"
	| "defining"
	| "definition_review"
	| "planning"
	| "spec_review"
	| "developing"
	| "publishing"
	| "reviewing"
	| "awaiting_merge"
	| "merging"
	| "releasing"
	| "done"
	| "rejected";

export type UnitAction =
	| "accept"
	| "reject"
	| "mark-ready"
	| "approve-spec"
	| "iterate"
	| "back"
	| "retry"
	| "cancel"
	| "refresh"
	| "merge"
	| "rereview"
	| "back-to-merge"
	| "override-approve"
	| "revise-spec"
	| "reopen"
	| "acknowledge"
	| "mark-released"
	| "follow-up"
	| "suggest-conventions";

export interface Unit {
	id: string;
	seq: number;
	label: string;
	project_id: string;
	project_name?: string;
	parent_unit_id?: string;
	kind: string;
	title: string;
	summary: string;
	description: string;
	origin: string;
	state: UnitState;
	stage: Stage;
	attention: string;
	attention_detail: string;
	review_iteration: number;
	merge_round: number;
	workspace_path: string;
	created_by: string;
	run_overrides: RunOverrides;
	created_at: string;
	updated_at: string;
	busy: boolean;
	actions: UnitAction[];
}

// Another model or effort level for one kind of a unit's runs, over the
// configuration's. What is empty runs as configured.
export interface RunOverride {
	model?: string;
	effort?: string;
}

export type RunOverrides = Record<string, RunOverride>;

export interface UnitRepo {
	repo_id: string;
	full_name: string;
	default_branch: string;
	checkout_path: string;
	base_sha: string;
	is_target: boolean;
	branch: string;
	head_sha: string;
	publish_state: string;
	pr_number: number;
	pr_url: string;
	pr_state: string;
	checks_state: string;
	merge_sha: string;
	merged_at: string | null;
	release_state: "" | "pending" | "success" | "failure" | "none";
	release_runs: WorkflowRun[];
}

export interface WorkflowRun {
	databaseId: number;
	name: string;
	status: string;
	conclusion: string;
	url: string;
}

export interface DailyStat {
	day: string;
	cost_usd: number;
	runs: number;
	done: number;
}

export interface RecentActivity extends Activity {
	unit_label: string;
	unit_title: string;
}

export interface DocumentMeta {
	kind: string;
	version: number;
	author: string;
	run_id: string;
	created_at: string;
}

export interface UnitDocument extends DocumentMeta {
	id: string;
	content: string;
	meta: Record<string, unknown>;
}

export interface Activity {
	id: number;
	unit_id: string;
	at: string;
	actor: string;
	kind: string;
	message: string;
	data: Record<string, unknown>;
}

export interface UnitDetail extends Unit {
	repos: UnitRepo[];
	documents: Record<string, DocumentMeta>;
	runs: Run[];
	activity: Activity[];
	feedback: Feedback[];
	issues: LinkedIssue[];
	artifacts: Artifact[];
}

// One version of a file the define or plan run made under docs/artifacts,
// such as a mockup.
export interface Artifact {
	path: string;
	version: number;
	content_type: string;
	size: number;
	removed?: boolean;
	author: string;
	run_id: string;
	created_at: string;
}

export type FeedbackStatus =
	| "new"
	| "triaging"
	| "proposal"
	| "attached"
	| "noise"
	| "uncertain"
	| "dismissed"
	| "inbox";

export interface Triage {
	verdict?: string;
	kind?: string;
	title?: string;
	summary?: string;
	unit?: string;
	confidence?: number;
	reason?: string;
}

export interface Feedback {
	id: string;
	project_id: string;
	channel_id: string;
	channel_name: string;
	ts: string;
	thread_ts: string;
	author: string;
	text: string;
	permalink: string;
	reply_count: number;
	edited: boolean;
	posted_at: string;
	status: FeedbackStatus;
	triage: Triage;
	unit_id: string;
	unit_label?: string;
	unit_title?: string;
	unit_state?: string;
}

export interface SlackSource {
	id: string;
	project_id: string;
	channel_id: string;
	channel_name: string;
	auto_triage: boolean;
	exclude_bots: boolean;
	poll_interval_s: number;
	last_polled_at: string | null;
	last_error: string;
	created_at: string;
}

export interface SlackChannel {
	id: string;
	name: string;
	type: string;
	topic: string;
	purpose: string;
}

export type RunStatus =
	| "queued"
	| "running"
	| "succeeded"
	| "failed"
	| "cancelled"
	| "timed_out"
	| "budget_exceeded"
	| "rate_limited"
	| "aborted"
	| "interrupted";

export interface Run {
	id: string;
	unit_id: string;
	unit_label?: string;
	unit_title?: string;
	project_id: string;
	parent_run_id: string;
	kind: string;
	status: RunStatus;
	reason: string;
	session_id: string;
	model: string;
	effort: string;
	permission_mode: string;
	prompt_version: string;
	cwd: string;
	cost_usd: number;
	cost_total_usd: number;
	input_tokens: number;
	output_tokens: number;
	turns: number;
	denials: number;
	result: unknown;
	created_at: string;
	started_at: string | null;
	ended_at: string | null;
	duration_ms: number;
}

export interface RunEvent {
	seq: number;
	at: string;
	type: string;
	subtype?: string;
	tool?: string;
	summary?: string;
	payload: Record<string, unknown>;
}

export interface ProjectSettings {
	draft_prs: boolean;
	merge_method: "squash" | "merge" | "rebase";
	delete_branch: boolean;
	max_review_iterations: number;
	post_review_to_github: boolean;
	auto_accept_proposals: boolean;
	triage_confidence_min: number;
	branch_template: string;
	learn_from_units: boolean;
	suggest_issue_updates: boolean;
}

export interface Repo {
	id: string;
	full_name: string;
	default_branch: string;
	cloned: boolean;
	clone_url: string;
	created_at: string;
}

export interface Project {
	id: string;
	name: string;
	slug: string;
	description: string;
	product_context: string;
	conventions: string;
	settings: ProjectSettings;
	repos: Repo[];
	created_at: string;
	updated_at: string;
}

export interface GitHubRepo {
	full_name: string;
	description: string;
	private: boolean;
	default_branch: string;
	updated_at: string;
}

export interface QuotaWindow {
	utilization: number;
	resetsAt: number;
}

export interface Stats {
	units_by_stage: Partial<Record<Stage, number>>;
	units_total: number;
	needs_attention: number;
	waiting_on_you: number;
	live_runs: number;
	cost_today_usd: number;
	cost_7d_usd: number;
	paused_until: string | null;
	feedback_by_status: Partial<Record<FeedbackStatus, number>>;
	slack_enabled: boolean;
	quota: { status: string; windows: Record<string, QuotaWindow>; updated_at: string } | null;
}

export interface Check {
	name: string;
	status: "ok" | "warn" | "fail";
	detail: string;
	fix?: string;
}

export interface StageConfig {
	model: string;
	effort: string;
	budget_usd: number;
	timeout: string;
}

export interface ConfigView {
	host: string;
	port: number;
	max_concurrent_runs: number;
	stages: Record<string, StageConfig>;
	pr_poll_interval: string;
	slack_poll_interval: string;
	data_dir: string;
	config_file: string;
	version: string;
}

// What Claude Code follows in a repository, as its default branch has it.
export interface RepoConventions {
	repo_id: string;
	repo: string;
	default_branch: string;
	files: ConventionFile[];
	hooks: ConventionHook[];
	attribution?: { commit?: string; pr?: string };
	error?: string;
}

export interface ConventionFile {
	path: string;
	kind: "instructions" | "rule" | "hook_script" | "settings" | "pr_template";
	paths?: string[];
	content: string;
}

export interface ConventionHook {
	event: string;
	matcher?: string;
	type: string;
	command?: string;
	timeout?: number;
}

// A GitHub issue linked to a unit, as tfy last read it.
export interface LinkedIssue {
	id: string;
	repo: string;
	number: number;
	ref: string;
	url: string;
	title: string;
	state: string;
	author: string;
	labels: string[];
	body: string;
	comments: { author: string; at: string; body: string }[];
	public: boolean;
	closes: boolean;
	fetched_at: string | null;
	issue_updated_at: string | null;
	suggestion_state: "" | "running" | "ready" | "none" | "applied" | "dismissed" | "failed";
	suggestion?: IssueSuggestion;
}

// What checking an issue against tfy's findings proposed, and what was done.
export interface IssueSuggestion {
	worth_updating: boolean;
	reason: string;
	comment: string;
	title: string;
	body: string;
	base_title: string;
	base_body: string;
	run_id?: string;
	error?: string;
	commented?: boolean;
	comment_url?: string;
	edited?: boolean;
	at: string;
}

// An open issue a unit could be linked to.
export interface IssueOption {
	repo: string;
	number: number;
	ref: string;
	title: string;
	url: string;
	labels: string[];
	updated_at: string;
	units: string[];
}

// A merge run's decision: the result of a run of kind "merge".
export interface MergeDecision {
	action: "merge" | "update" | "wait" | "blocked";
	repos: string[];
	wait_for: "" | "released" | "tagged";
	reason: string;
}
