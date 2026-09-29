import { Spin } from "antd";
import { Network } from "lucide-react";
import { useEffect, useMemo, useState } from "react";
import { useScope } from "@/app/scope";
import { EmptyState, Metric, PageHeader } from "@/shared/components/misc";
import { toneColor } from "@/shared/lib/status";
import { AgentGraph } from "../components/AgentGraph";
import { agentWorking, useAgents, workingAgents } from "../hooks";

// Every agent at work now: each run's main agent and the sub-agents it
// started, what each is doing, and on which unit.
export default function AgentsPage() {
	const { projectId } = useScope();
	const { data: board, isLoading } = useAgents();
	const runs = useMemo(
		() => (board?.runs ?? []).filter((r) => !projectId || r.project_id === projectId),
		[board, projectId],
	);
	const now = useNow(runs.length > 0);
	const scoped = board && { ...board, runs };
	const working = workingAgents(scoped);
	const subagents = runs.reduce((n, r) => n + r.agents.length, 0);
	const subagentsWorking = runs.reduce((n, r) => n + r.agents.filter(agentWorking).length, 0);

	return (
		<div style={{ display: "flex", flexDirection: "column", height: "100%" }}>
			<PageHeader title="Agents">
				<div style={{ display: "flex", gap: 10, marginTop: 12, flexWrap: "wrap" }}>
					<Metric
						label="Runs"
						value={board ? `${board.runs.length} / ${board.max_runs}` : "—"}
						hint="going now, of the concurrent runs allowed"
					/>
					<Metric label="Agents working" value={working} tone={working ? toneColor.accent : undefined} />
					<Metric
						label="Sub-agents"
						value={subagentsWorking}
						hint={subagents > subagentsWorking ? `${subagents - subagentsWorking} finished` : "at work"}
					/>
				</div>
			</PageHeader>
			<div style={{ flex: 1, overflow: "auto" }}>
				{isLoading ? (
					<Spin style={{ display: "block", margin: 48 }} />
				) : runs.length === 0 ? (
					<EmptyState icon={<Network size={28} />} title="No agents at work">
						Runs show up here while they go: the main agent, the sub-agents it hands work to, and what each is doing.
					</EmptyState>
				) : (
					runs.map((r) => (
						<div
							key={r.run_id}
							style={{ padding: "16px 16px 18px", borderBottom: "1px solid var(--tf-border)", overflowX: "auto" }}
						>
							<AgentGraph run={r} now={now} />
						</div>
					))
				)}
			</div>
		</div>
	);
}

// The time, ticking every second while there is something to time.
function useNow(ticking: boolean): number {
	const [now, setNow] = useState(Date.now());
	useEffect(() => {
		if (!ticking) return;
		const t = window.setInterval(() => setNow(Date.now()), 1000);
		return () => window.clearInterval(t);
	}, [ticking]);
	return now;
}
