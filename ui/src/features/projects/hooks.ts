import { useMutation, useQuery, useQueryClient } from "@tanstack/react-query";
import { api, query } from "@/shared/api/client";
import type { GitHubRepo, IssueOption, Project, ProjectSettings, Repo, RepoConventions } from "@/shared/api/types";

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
	// Left out, the project's conventions stay as they are.
	conventions?: string;
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

// The conventions of every repository of a project, read from their default
// branches. Refreshing fetches the repositories first.
export function useConventions(projectId: string) {
	const qc = useQueryClient();
	const q = useQuery({
		queryKey: ["conventions", projectId],
		queryFn: () => api.get<RepoConventions[]>(`/projects/${projectId}/conventions`),
		staleTime: 60_000,
	});
	const refresh = useMutation({
		mutationFn: () => api.get<RepoConventions[]>(`/projects/${projectId}/conventions?refresh=1`),
		onSuccess: (data) => qc.setQueryData(["conventions", projectId], data),
	});
	return { ...q, refresh };
}

// Open issues of a project's repositories, for linking; q searches them.
export function useProjectIssues(projectId: string | undefined, q: string, enabled: boolean) {
	return useQuery({
		queryKey: ["project-issues", projectId, q],
		queryFn: () => api.get<IssueOption[]>(`/projects/${projectId}/issues${query({ q: q || undefined })}`),
		enabled: enabled && !!projectId,
		staleTime: 30_000,
		retry: false,
	});
}
