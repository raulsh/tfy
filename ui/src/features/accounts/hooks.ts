import { useMutation, useQuery, useQueryClient } from "@tanstack/react-query";
import { api } from "@/shared/api/client";
import type { Account, AccountInput, AccountTest, Engine, RunSettings } from "@/shared/api/types";

export function useAccounts() {
	return useQuery({ queryKey: ["accounts"], queryFn: () => api.get<Account[]>("/accounts") });
}

export function useEngines() {
	return useQuery({ queryKey: ["engines"], queryFn: () => api.get<Engine[]>("/engines"), staleTime: 5 * 60_000 });
}

export function useRunSettings() {
	return useQuery({ queryKey: ["run-settings"], queryFn: () => api.get<RunSettings>("/settings/runs") });
}

function useInvalidateAccounts() {
	const qc = useQueryClient();
	return () => {
		qc.invalidateQueries({ queryKey: ["accounts"] });
		qc.invalidateQueries({ queryKey: ["run-settings"] });
		qc.invalidateQueries({ queryKey: ["stats"] });
	};
}

export function useCreateAccount() {
	const invalidate = useInvalidateAccounts();
	return useMutation({ mutationFn: (a: AccountInput) => api.post<Account>("/accounts", a), onSuccess: invalidate });
}

export function useUpdateAccount(id: string) {
	const invalidate = useInvalidateAccounts();
	return useMutation({
		mutationFn: (a: AccountInput) => api.patch<Account>(`/accounts/${id}`, a),
		onSuccess: invalidate,
	});
}

export function useDeleteAccount() {
	const invalidate = useInvalidateAccounts();
	return useMutation({ mutationFn: (id: string) => api.delete(`/accounts/${id}`), onSuccess: invalidate });
}

export function useResumeAccount() {
	const invalidate = useInvalidateAccounts();
	return useMutation({ mutationFn: (id: string) => api.post(`/accounts/${id}/resume`), onSuccess: invalidate });
}

export function useTestAccount() {
	const invalidate = useInvalidateAccounts();
	return useMutation({
		mutationFn: ({ id, probe }: { id: string; probe: boolean }) =>
			api.post<AccountTest>(`/accounts/${id}/test${probe ? "?probe=1" : ""}`),
		onSuccess: invalidate,
	});
}

export function useSetRunSettings() {
	const invalidate = useInvalidateAccounts();
	return useMutation({
		mutationFn: (s: RunSettings) => api.put<RunSettings>("/settings/runs", s),
		onSuccess: invalidate,
	});
}

// The account names, by id.
export function accountNames(accounts: Account[] | undefined): Record<string, string> {
	return Object.fromEntries((accounts ?? []).map((a) => [a.id, a.name]));
}

export const kindLabels: Record<string, string> = {
	machine: "This machine's login",
	token: "Subscription token",
	api_key: "API key",
	bedrock: "Amazon Bedrock",
};
