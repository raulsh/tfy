import { CircleStop, GitMerge, Hourglass, PencilLine } from "lucide-react";
import type { MergeDecision, Run, UnitDetail } from "@/shared/api/types";
import { Mono, TimeAgo } from "@/shared/components/misc";
import { Card } from "@/shared/components/OverlayDrawer";
import { StatusTag } from "@/shared/components/StatusTag";
import { runStatusLabel, runTone, toneColor } from "@/shared/lib/status";

const icons = { merge: GitMerge, update: PencilLine, wait: Hourglass, blocked: CircleStop };

function decisionOf(r: Run): MergeDecision | undefined {
	const d = r.result as MergeDecision | undefined;
	return d && typeof d === "object" && "action" in d ? d : undefined;
}

function headline(d: MergeDecision) {
	const repos = <Mono>{d.repos.join(", ")}</Mono>;
	switch (d.action) {
		case "merge":
			return <>Merge {repos}</>;
		case "update":
			return <>Update {repos}</>;
		case "wait":
			return (
				<>
					Wait until {repos} {d.repos.length > 1 ? "are" : "is"} {d.wait_for}
				</>
			);
		default:
			return <>Stop</>;
	}
}

// How the unit's pull requests merged: the merge run's decisions, oldest
// first.
export function MergeCard({ unit }: { unit: UnitDetail }) {
	const runs = unit.runs.filter((r) => r.kind === "merge").reverse();
	if (runs.length === 0) return null;
	return (
		<Card title="Merge" padded={false}>
			{runs.map((r, i) => {
				const d = decisionOf(r);
				const Icon = d ? icons[d.action] : Hourglass;
				return (
					<div
						key={r.id}
						style={{
							display: "grid",
							gridTemplateColumns: "20px minmax(0, 1fr) auto",
							gap: 8,
							padding: "10px 16px",
							borderTop: i ? "1px solid var(--tf-border)" : undefined,
						}}
					>
						<span style={{ paddingTop: 2 }}>
							<Icon size={15} color={d?.action === "blocked" ? toneColor.error : toneColor.accent} />
						</span>
						<div style={{ minWidth: 0 }}>
							<div style={{ fontWeight: 500 }}>{d ? headline(d) : "Deciding…"}</div>
							{d?.reason && (
								<div className="muted" style={{ fontSize: 12, marginTop: 2 }}>
									{d.reason}
								</div>
							)}
						</div>
						<div style={{ fontSize: 12, textAlign: "right" }}>
							{r.status !== "succeeded" && <StatusTag tone={runTone(r.status)} label={runStatusLabel(r.status)} />}
							<div className="faint">
								<TimeAgo at={r.created_at} />
							</div>
						</div>
					</div>
				);
			})}
		</Card>
	);
}
