import { useMutation, useQuery, useQueryClient } from "@tanstack/react-query";
import { api, query } from "@/shared/api/client";
import type { Run } from "@/shared/api/types";

export function useRuns(filter: { unitId?: string; projectId?: string } = {}) {
	return useQuery({
		queryKey: ["runs", filter.unitId ?? "", filter.projectId ?? ""],
		queryFn: () =>
			api.get<Run[]>(`/runs${query({ unit_id: filter.unitId, project_id: filter.projectId, limit: 500 })}`),
	});
}

export function useRun(id: string | undefined) {
	return useQuery({
		queryKey: ["run", id],
		queryFn: () => api.get<Run>(`/runs/${id}`),
		enabled: !!id,
		// Keep duration ticking while the drawer is open.
		refetchInterval: (q) => (q.state.data?.status === "running" ? 5000 : false),
	});
}

export function useCancelRun() {
	const qc = useQueryClient();
	return useMutation({
		mutationFn: (id: string) => api.post(`/runs/${id}/cancel`),
		onSettled: (_d, _e, id) => {
			qc.invalidateQueries({ queryKey: ["run", id] });
			qc.invalidateQueries({ queryKey: ["runs"] });
		},
	});
}
