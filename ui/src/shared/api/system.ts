import { useQuery } from "@tanstack/react-query";
import { api } from "./client";
import type { Check, ConfigView, DailyStat, RecentActivity, Stats } from "./types";

export function useStats(projectId?: string) {
	return useQuery({
		queryKey: ["stats", projectId ?? "all"],
		queryFn: () => api.get<Stats>(`/stats${projectId ? `?project_id=${projectId}` : ""}`),
		refetchInterval: 30_000,
	});
}

export function useConfig() {
	return useQuery({ queryKey: ["config"], queryFn: () => api.get<ConfigView>("/config") });
}

export function useDoctor() {
	return useQuery({ queryKey: ["doctor"], queryFn: () => api.get<Check[]>("/doctor"), staleTime: 60_000 });
}

export function useDailyStats(days = 14) {
	return useQuery({
		queryKey: ["stats", "daily", days],
		queryFn: () => api.get<DailyStat[]>(`/stats/daily?days=${days}`),
	});
}

export function useRecentActivity() {
	return useQuery({ queryKey: ["stats", "activity"], queryFn: () => api.get<RecentActivity[]>("/activity?limit=40") });
}
