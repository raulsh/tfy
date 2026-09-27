import { useMutation, useQuery, useQueryClient } from "@tanstack/react-query";
import { api, query } from "@/shared/api/client";
import type { Feedback, SlackChannel, SlackSource, Unit } from "@/shared/api/types";

export function useFeedback(projectId?: string) {
	return useQuery({
		queryKey: ["feedback", projectId ?? "all"],
		queryFn: () => api.get<Feedback[]>(`/feedback${query({ project_id: projectId, limit: 2000 })}`),
	});
}

export function useThread(feedbackId: string | undefined) {
	return useQuery({
		queryKey: ["feedback", "thread", feedbackId],
		queryFn: () => api.get<Feedback[]>(`/feedback/${feedbackId}/thread`),
		enabled: !!feedbackId,
	});
}

function useInvalidate() {
	const qc = useQueryClient();
	return () => {
		qc.invalidateQueries({ queryKey: ["feedback"] });
		qc.invalidateQueries({ queryKey: ["units"] });
		qc.invalidateQueries({ queryKey: ["stats"] });
	};
}

export function useSetFeedbackStatus() {
	const invalidate = useInvalidate();
	return useMutation({
		mutationFn: ({ id, status }: { id: string; status: "dismissed" | "inbox" }) =>
			api.patch<Feedback>(`/feedback/${id}`, { status }),
		onSuccess: invalidate,
	});
}

export function useImportPermalinks() {
	const invalidate = useInvalidate();
	return useMutation({
		mutationFn: (v: { project_id: string; permalinks: string[] }) => api.post<Feedback[]>("/feedback/import", v),
		onSuccess: invalidate,
	});
}

export interface FromFeedback {
	project_id: string;
	feedback_ids: string[];
	title: string;
	kind: string;
	description: string;
}

export function useUnitFromFeedback() {
	const invalidate = useInvalidate();
	return useMutation({
		mutationFn: (v: FromFeedback) => api.post<Unit>("/feedback/units", v),
		onSuccess: invalidate,
	});
}

export function useSources(projectId: string | undefined) {
	return useQuery({
		queryKey: ["sources", projectId],
		queryFn: () => api.get<SlackSource[]>(`/projects/${projectId}/sources`),
		enabled: !!projectId,
	});
}

export function useSlackChannels(q: string, enabled: boolean) {
	return useQuery({
		queryKey: ["slack-channels", q],
		queryFn: () => api.get<SlackChannel[]>(`/pickers/slack-channels${query({ q })}`),
		enabled,
		staleTime: 5 * 60_000,
	});
}

export interface SourceInput {
	channel_id?: string;
	channel_name?: string;
	auto_triage?: boolean;
	exclude_bots?: boolean;
	poll_interval_s?: number;
	backfill_days?: number;
}

function useInvalidateSources() {
	const qc = useQueryClient();
	return () => {
		qc.invalidateQueries({ queryKey: ["sources"] });
		qc.invalidateQueries({ queryKey: ["feedback"] });
	};
}

export function useAddSource(projectId: string) {
	const invalidate = useInvalidateSources();
	return useMutation({
		mutationFn: (v: SourceInput) => api.post<SlackSource>(`/projects/${projectId}/sources`, v),
		onSuccess: invalidate,
	});
}

export function useUpdateSource() {
	const invalidate = useInvalidateSources();
	return useMutation({
		mutationFn: ({ id, ...v }: SourceInput & { id: string }) => api.patch<SlackSource>(`/sources/${id}`, v),
		onSuccess: invalidate,
	});
}

export function useRemoveSource() {
	const invalidate = useInvalidateSources();
	return useMutation({ mutationFn: (id: string) => api.delete(`/sources/${id}`), onSuccess: invalidate });
}

export function usePollSource() {
	const invalidate = useInvalidateSources();
	return useMutation({
		mutationFn: (id: string) => api.post<{ new: number }>(`/sources/${id}/poll`),
		onSuccess: invalidate,
	});
}
