import { useQuery } from "@tanstack/react-query";
import { api } from "@/shared/api/client";
import type { Agent, AgentBoard } from "@/shared/api/types";

// The agent board. After the first fetch it is kept current by the live
// stream (useLiveUpdates), which sends it whole whenever it changes.
export function useAgents() {
	return useQuery({
		queryKey: ["agents"],
		queryFn: () => api.get<AgentBoard>("/agents"),
		staleTime: Number.POSITIVE_INFINITY,
	});
}

export const agentWorking = (a: Agent) => a.status === "working" || a.status === "waiting";

// How many agents are at work: each run's main agent and its sub-agents
// still going.
export function workingAgents(board: AgentBoard | undefined): number {
	let n = 0;
	for (const r of board?.runs ?? []) {
		n += (agentWorking(r.main) ? 1 : 0) + r.agents.filter(agentWorking).length;
	}
	return n;
}
