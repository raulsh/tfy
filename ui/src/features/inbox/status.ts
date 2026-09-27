import type { FeedbackStatus } from "@/shared/api/types";
import type { Tone } from "@/shared/lib/status";

export const feedbackLabel: Record<FeedbackStatus, string> = {
	new: "Awaiting triage",
	triaging: "Triaging",
	proposal: "Proposal",
	attached: "In a unit",
	noise: "Noise",
	uncertain: "Needs a look",
	dismissed: "Dismissed",
	inbox: "Inbox",
};

export const feedbackTone: Record<FeedbackStatus, Tone> = {
	new: "accent",
	triaging: "accent",
	proposal: "info",
	attached: "ok",
	noise: "neutral",
	uncertain: "warn",
	dismissed: "neutral",
	inbox: "neutral",
};

export const feedbackOrder: FeedbackStatus[] = [
	"uncertain",
	"inbox",
	"new",
	"triaging",
	"proposal",
	"attached",
	"noise",
	"dismissed",
];
