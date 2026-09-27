import { useMutation, useQuery, useQueryClient } from "@tanstack/react-query";
import { api, query } from "@/shared/api/client";
import type { DocumentMeta, Unit, UnitAction, UnitDetail, UnitDocument } from "@/shared/api/types";

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
		mutationFn: ({ action, feedback, closePRs }: { action: UnitAction; feedback?: string; closePRs?: boolean }) =>
			api.post<Unit>(`/units/${unitId}/actions/${action}`, { feedback, close_prs: closePRs }),
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
