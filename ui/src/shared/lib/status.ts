import { status } from "@/app/theme/tokens";
import type { RunStatus, Stage, Unit, UnitState } from "@/shared/api/types";

export const stages: { key: Stage; label: string }[] = [
	{ key: "intake", label: "Intake" },
	{ key: "definition", label: "Definition" },
	{ key: "planning", label: "Planning" },
	{ key: "executing", label: "Executing" },
	{ key: "release", label: "Release" },
	{ key: "done", label: "Done" },
	{ key: "rejected", label: "Rejected" },
];

export const stageLabel = (s: string) => stages.find((x) => x.key === s)?.label ?? s;

const stateLabels: Record<UnitState, string> = {
	proposed: "Proposed",
	defining: "Defining",
	definition_review: "Requirement review",
	planning: "Planning",
	spec_review: "Spec review",
	developing: "Developing",
	publishing: "Publishing",
	reviewing: "Reviewing",
	awaiting_merge: "Awaiting merge",
	merging: "Merging",
	releasing: "Releasing",
	done: "Done",
	rejected: "Rejected",
};

export const stateLabel = (s: string) => stateLabels[s as UnitState] ?? s;

// States where the factory is doing work vs. waiting on a person.
const working = new Set<UnitState>([
	"defining",
	"planning",
	"developing",
	"publishing",
	"reviewing",
	"merging",
	"releasing",
]);
const waiting = new Set<UnitState>(["proposed", "definition_review", "spec_review", "awaiting_merge"]);

export type Tone = "ok" | "warn" | "error" | "info" | "accent" | "neutral";

export const toneColor: Record<Tone, string> = {
	ok: status.ok,
	warn: status.warn,
	error: status.error,
	info: status.info,
	accent: status.accent,
	neutral: status.neutral,
};

export function unitTone(u: Pick<Unit, "state" | "attention">): Tone {
	if (u.attention && u.attention !== "new_feedback" && u.attention !== "waiting") return "error";
	if (u.attention === "waiting") return "warn";
	if (u.state === "done") return "ok";
	if (u.state === "rejected") return "neutral";
	if (waiting.has(u.state)) return "warn";
	if (working.has(u.state)) return "accent";
	return "info";
}

export const isWaitingOnHuman = (s: UnitState) => waiting.has(s);

const runLabels: Record<RunStatus, string> = {
	queued: "Queued",
	running: "Running",
	succeeded: "Succeeded",
	failed: "Failed",
	cancelled: "Cancelled",
	timed_out: "Timed out",
	budget_exceeded: "Over budget",
	rate_limited: "Rate limited",
	aborted: "Aborted",
	interrupted: "Interrupted",
};

export const runStatusLabel = (s: string) => runLabels[s as RunStatus] ?? s;

export function runTone(s: string): Tone {
	switch (s) {
		case "succeeded":
			return "ok";
		case "running":
		case "queued":
			return "accent";
		case "failed":
		case "aborted":
			return "error";
		case "timed_out":
		case "budget_exceeded":
		case "rate_limited":
			return "warn";
		default:
			return "neutral";
	}
}

export const isRunLive = (s: string) => s === "running" || s === "queued";

export const attentionLabel: Record<string, string> = {
	interrupted: "Interrupted",
	failed: "Failed",
	budget_exceeded: "Over budget",
	waiting: "Rate limited",
	no_changes: "No changes",
	conflict: "Conflict",
	review_blocked: "Review blocked",
	pr_closed: "PR closed",
	head_changed: "Head changed",
	partially_merged: "Partially merged",
	ci_failed: "CI failed",
	new_feedback: "New feedback",
};

export const kindLabel: Record<string, string> = {
	feature: "Feature",
	bugfix: "Bug fix",
	improvement: "Improvement",
	chore: "Chore",
};

export const originLabel: Record<string, string> = {
	developer: "Developer",
	slack_auto: "Slack (triaged)",
	slack_manual: "Slack (manual)",
	follow_up: "Follow-up",
	retrospective: "Retrospective",
	github_issue: "GitHub issue",
};

export function checksTone(s: string): Tone {
	switch (s) {
		case "success":
			return "ok";
		case "failure":
			return "error";
		case "pending":
			return "warn";
		default:
			return "neutral";
	}
}

export function prTone(s: string): Tone {
	switch (s) {
		case "merged":
			return "ok";
		case "open":
			return "accent";
		case "closed":
			return "error";
		default:
			return "neutral";
	}
}
