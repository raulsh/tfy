import { useMutation, useQuery, useQueryClient } from "@tanstack/react-query";
import { api, query } from "@/shared/api/client";
import type { GitHubRepo, Project, ProjectSettings, Repo } from "@/shared/api/types";

export function useProjects() {
	return useQuery({ queryKey: ["projects"], queryFn: () => api.get<Project[]>("/projects") });
}

export function useProject(id: string | undefined) {
	return useQuery({
		queryKey: ["project", id],
		queryFn: () => api.get<Project>(`/projects/${id}`),
		enabled: !!id,
	});
}

export interface ProjectInput {
	name: string;
	description: string;
	product_context: string;
	settings?: ProjectSettings;
}

function useInvalidateProjects() {
	const qc = useQueryClient();
	return () => {
		qc.invalidateQueries({ queryKey: ["projects"] });
		qc.invalidateQueries({ queryKey: ["project"] });
	};
}

export function useCreateProject() {
	const invalidate = useInvalidateProjects();
	return useMutation({ mutationFn: (p: ProjectInput) => api.post<Project>("/projects", p), onSuccess: invalidate });
}

export function useUpdateProject(id: string) {
	const invalidate = useInvalidateProjects();
	return useMutation({
		mutationFn: (p: ProjectInput) => api.patch<Project>(`/projects/${id}`, p),
		onSuccess: invalidate,
	});
}

export function useDeleteProject() {
	const invalidate = useInvalidateProjects();
	return useMutation({ mutationFn: (id: string) => api.delete(`/projects/${id}`), onSuccess: invalidate });
}

export function useLinkRepo(projectId: string) {
	const invalidate = useInvalidateProjects();
	return useMutation({
		mutationFn: (fullName: string) => api.post<Repo>(`/projects/${projectId}/repos`, { full_name: fullName }),
		onSuccess: invalidate,
	});
}

export function useUnlinkRepo(projectId: string) {
	const invalidate = useInvalidateProjects();
	return useMutation({
		mutationFn: (repoId: string) => api.delete(`/projects/${projectId}/repos/${repoId}`),
		onSuccess: invalidate,
	});
}

export function useGitHubOwners(enabled: boolean) {
	return useQuery({
		queryKey: ["gh-owners"],
		queryFn: () => api.get<string[]>("/pickers/github-owners"),
		enabled,
		staleTime: 5 * 60_000,
	});
}

export function useGitHubRepos(owner: string | undefined) {
	return useQuery({
		queryKey: ["gh-repos", owner],
		queryFn: () => api.get<GitHubRepo[]>(`/pickers/github-repos${query({ owner })}`),
		enabled: !!owner,
		staleTime: 5 * 60_000,
	});
}
