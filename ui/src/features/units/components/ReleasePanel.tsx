import { Rocket } from "lucide-react";
import type { UnitDetail } from "@/shared/api/types";
import { EmptyState, Markdown, Mono } from "@/shared/components/misc";
import { Card } from "@/shared/components/OverlayDrawer";
import { StatusTag } from "@/shared/components/StatusTag";
import type { Tone } from "@/shared/lib/status";
import { useDocument } from "../hooks";

const releaseTone: Record<string, Tone> = { pending: "warn", success: "ok", failure: "error", none: "neutral" };

function runTone(status: string, conclusion: string): Tone {
	if (status !== "completed") return "warn";
	if (["failure", "cancelled", "timed_out", "startup_failure", "action_required"].includes(conclusion)) return "error";
	return "ok";
}

// After the merge: the release notes, and CI on each merge commit.
export function ReleasePanel({ unit }: { unit: UnitDetail }) {
	const notes = useDocument(unit.documents.release_notes ? unit.id : undefined, "release_notes");
	const merged = unit.repos.filter((r) => r.pr_state === "merged");
	if (merged.length === 0) {
		return (
			<Card>
				<EmptyState icon={<Rocket size={28} />} title="Nothing released yet">
					Once the pull requests are merged, tfy writes release notes and follows CI on the merge commits.
				</EmptyState>
			</Card>
		);
	}
	return (
		<div style={{ display: "grid", gridTemplateColumns: "minmax(0, 1fr) 420px", gap: 16, alignItems: "start" }}>
			<Card title="Release notes">
				{notes.data ? (
					<Markdown>{notes.data.document.content}</Markdown>
				) : (
					<span className="muted">Being written…</span>
				)}
			</Card>
			<Card title="CI on the merge commits" padded={false}>
				{merged.map((r, i) => (
					<div
						key={r.repo_id}
						style={{ padding: "10px 16px", borderTop: i ? "1px solid var(--tf-border)" : undefined }}
					>
						<div style={{ display: "flex", justifyContent: "space-between", alignItems: "center", gap: 8 }}>
							<span style={{ fontWeight: 500 }}>{r.full_name}</span>
							<StatusTag tone={releaseTone[r.release_state] ?? "neutral"} label={r.release_state || "waiting"} />
						</div>
						<Mono faint>{r.merge_sha.slice(0, 7)}</Mono>
						{(r.release_runs ?? []).map((run) => (
							<div
								key={run.databaseId}
								style={{ display: "flex", justifyContent: "space-between", gap: 8, marginTop: 6 }}
							>
								<a href={run.url} target="_blank" rel="noreferrer">
									{run.name}
								</a>
								<StatusTag tone={runTone(run.status, run.conclusion)} label={run.conclusion || run.status} />
							</div>
						))}
						{r.release_state === "none" && (
							<div className="faint" style={{ fontSize: 12, marginTop: 4 }}>
								No CI ran on this commit.
							</div>
						)}
					</div>
				))}
			</Card>
		</div>
	);
}
