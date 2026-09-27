import { CircleCheck, CircleDashed, CircleDot } from "lucide-react";
import type { MergeStep, MergeStepView, UnitDetail } from "@/shared/api/types";
import { Mono } from "@/shared/components/misc";
import { Card } from "@/shared/components/OverlayDrawer";
import { toneColor } from "@/shared/lib/status";

const waitLabel: Record<string, string> = {
	merged: "once the steps before it are merged",
	released: "once CI is green on their merge commits",
	tagged: "once a tag contains their merge commits",
};

// The order the unit's pull requests merge in, with where each step stands.
export function MergePlanCard({ unit }: { unit: UnitDetail }) {
	const plan = unit.merge_plan;
	if (!plan || plan.steps.length < 2) return null;
	return (
		<Card title={`Merge plan · step ${plan.current + 1} of ${plan.steps.length}`} padded={false}>
			{plan.steps.map((st) => (
				<StepRow key={st.index} st={st} unit={unit} />
			))}
		</Card>
	);
}

function StepRow({ st, unit }: { st: MergeStepView; unit: UnitDetail }) {
	const icon =
		st.state === "merged" ? (
			<CircleCheck size={15} color={toneColor.ok} />
		) : st.state === "current" ? (
			<CircleDot size={15} color={toneColor.accent} />
		) : (
			<CircleDashed size={15} color={toneColor.neutral} />
		);
	const prs = unit.repos.filter((r) => st.repos.includes(r.full_name) && r.pr_number);
	return (
		<div
			style={{
				display: "grid",
				gridTemplateColumns: "20px minmax(0, 1fr)",
				gap: 8,
				padding: "10px 16px",
				borderTop: st.index ? "1px solid var(--tf-border)" : undefined,
				background: st.state === "current" ? "var(--tf-selected)" : undefined,
			}}
		>
			<span style={{ paddingTop: 2 }}>{icon}</span>
			<div>
				<div style={{ fontWeight: 500 }}>
					Step {st.index + 1}
					{st.index > 0 && (
						<span className="muted" style={{ fontWeight: 400 }}>
							{" "}
							· {waitLabel[st.wait_for] ?? st.wait_for}
						</span>
					)}
				</div>
				<div style={{ display: "flex", gap: 10, flexWrap: "wrap", marginTop: 2 }}>
					{st.repos.map((r) => {
						const pr = prs.find((x) => x.full_name === r);
						return (
							<span key={r} style={{ fontSize: 12 }}>
								<Mono>{r}</Mono>
								{pr && (
									<>
										{" "}
										<a href={pr.pr_url} target="_blank" rel="noreferrer">
											#{pr.pr_number}
										</a>{" "}
										<span className="faint">{pr.pr_state || "open"}</span>
									</>
								)}
							</span>
						);
					})}
				</div>
				{st.update && (
					<div className="muted" style={{ fontSize: 12, marginTop: 4 }}>
						Before it merges: {st.update}
					</div>
				)}
			</div>
		</div>
	);
}

// The plan as the spec proposes it, before anything is merged.
export function SpecMergePlan({ plan }: { plan?: MergeStep[] }) {
	if (!plan || plan.length < 2) return null;
	return (
		<Card title="Merge order">
			<ol style={{ margin: 0, paddingLeft: 18, display: "grid", gap: 8 }}>
				{plan.map((st, i) => (
					<li key={`${i}-${st.repos.join(",")}`}>
						<Mono>{st.repos.join(", ")}</Mono>
						{i > 0 && (
							<div className="faint" style={{ fontSize: 12 }}>
								{waitLabel[st.wait_for ?? "merged"] ?? st.wait_for}
							</div>
						)}
						{st.update && (
							<div className="muted" style={{ fontSize: 12 }}>
								Then: {st.update}
							</div>
						)}
					</li>
				))}
			</ol>
		</Card>
	);
}
