import { useMutation, useQuery, useQueryClient } from "@tanstack/react-query";
import { api, query } from "@/shared/api/client";
import type {
	DocumentMeta,
	LinkedIssue,
	RunOverrides,
	Unit,
	UnitAction,
	UnitDetail,
	UnitDocument,
} from "@/shared/api/types";

export function useUnits(projectId?: string) {
	return useQuery({
		queryKey: ["units", projectId ?? "all"],
		queryFn: () => api.get<Unit[]>(`/units${query({ project_id: projectId })}`),
	});
}

export function useUnit(id: string | undefined) {
	return useQuery({
		queryKey: ["unit", id],
		queryFn: () => api.get<UnitDetail>(`/units/${id}`),
		enabled: !!id,
	});
}

export function useDocument(unitId: string | undefined, kind: string, version?: number) {
	return useQuery({
		queryKey: ["doc", unitId, kind, version ?? "latest"],
		queryFn: () =>
			api.get<{ document: UnitDocument; versions: DocumentMeta[] }>(
				`/units/${unitId}/documents/${kind}${query({ version })}`,
			),
		enabled: !!unitId,
		retry: false,
	});
}

export interface NewUnit {
	project_id: string;
	kind: string;
	title: string;
	description: string;
	// A GitHub issue the unit is for: a link, owner/repo#12, or #12.
	issue?: string;
	run_overrides?: RunOverrides;
}

export function useCreateUnit() {
	const qc = useQueryClient();
	return useMutation({
		mutationFn: (u: NewUnit) => api.post<Unit>("/units", u),
		onSuccess: () => qc.invalidateQueries({ queryKey: ["units"] }),
	});
}

export function useUnitAction(unitId: string) {
	const qc = useQueryClient();
	return useMutation({
		mutationFn: ({
			action,
			feedback,
			closePRs,
			admin,
		}: {
			action: UnitAction;
			feedback?: string;
			closePRs?: boolean;
			admin?: boolean;
		}) => api.post<Unit>(`/units/${unitId}/actions/${action}`, { feedback, close_prs: closePRs, admin }),
		onSettled: () => {
			qc.invalidateQueries({ queryKey: ["unit", unitId] });
			qc.invalidateQueries({ queryKey: ["units"] });
			qc.invalidateQueries({ queryKey: ["stats"] });
		},
	});
}

export function useSaveDocument(unitId: string, kind: string) {
	const qc = useQueryClient();
	return useMutation({
		mutationFn: (content: string) => api.put<UnitDocument>(`/units/${unitId}/documents/${kind}`, { content }),
		onSuccess: () => {
			qc.invalidateQueries({ queryKey: ["doc", unitId] });
			qc.invalidateQueries({ queryKey: ["unit", unitId] });
		},
	});
}

// Mutations on a unit's linked GitHub issues; each refreshes the unit.
export function useIssueActions(unitId: string) {
	const qc = useQueryClient();
	const done = () => qc.invalidateQueries({ queryKey: ["unit", unitId] });
	const base = `/units/${unitId}/issues`;
	return {
		link: useMutation({
			mutationFn: (v: { ref: string; closes?: boolean }) => api.post<LinkedIssue>(base, v),
			onSettled: done,
		}),
		setCloses: useMutation({
			mutationFn: (v: { id: string; closes: boolean }) =>
				api.patch<LinkedIssue>(`${base}/${v.id}`, { closes: v.closes }),
			onSettled: done,
		}),
		unlink: useMutation({ mutationFn: (id: string) => api.delete(`${base}/${id}`), onSettled: done }),
		refresh: useMutation({
			mutationFn: (id: string) => api.post<LinkedIssue>(`${base}/${id}/refresh`, {}),
			onSettled: done,
		}),
		check: useMutation({
			mutationFn: (id: string) => api.post<LinkedIssue>(`${base}/${id}/check`, {}),
			onSettled: done,
		}),
		apply: useMutation({
			mutationFn: (v: { id: string; mode: "comment" | "edit"; comment?: string; title?: string; body?: string }) =>
				api.post<LinkedIssue>(`${base}/${v.id}/apply`, v),
			onSettled: done,
		}),
		dismiss: useMutation({
			mutationFn: (id: string) => api.post<LinkedIssue>(`${base}/${id}/dismiss`, {}),
			onSettled: done,
		}),
	};
}
