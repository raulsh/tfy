// A small fetch wrapper for tfy's API: same-origin cookie auth and the
// {data} / {error} envelopes.

export class ApiError extends Error {
	constructor(
		public status: number,
		message: string,
	) {
		super(message);
	}
}

const BASE = "/api/v1";

async function request<T>(method: string, path: string, body?: unknown): Promise<T> {
	const res = await fetch(BASE + path, {
		method,
		credentials: "same-origin",
		headers: body === undefined ? undefined : { "Content-Type": "application/json" },
		body: body === undefined ? undefined : JSON.stringify(body),
	});
	let payload: { data?: T; error?: string } | undefined;
	try {
		payload = await res.json();
	} catch {
		payload = undefined;
	}
	if (!res.ok) {
		throw new ApiError(res.status, payload?.error ?? `${res.status} ${res.statusText}`);
	}
	return payload?.data as T;
}

export const api = {
	get: <T>(path: string) => request<T>("GET", path),
	post: <T>(path: string, body?: unknown) => request<T>("POST", path, body ?? {}),
	put: <T>(path: string, body?: unknown) => request<T>("PUT", path, body ?? {}),
	patch: <T>(path: string, body?: unknown) => request<T>("PATCH", path, body ?? {}),
	delete: <T>(path: string) => request<T>("DELETE", path),
};

export function streamURL(path: string): string {
	return BASE + path;
}

// The file itself, for a frame, an image or a new tab.
export function artifactURL(unitId: string, path: string, version: number): string {
	return `${BASE}/units/${unitId}/artifacts/${version}/${path.split("/").map(encodeURIComponent).join("/")}`;
}

export function query(params: Record<string, string | number | undefined | null>): string {
	const q = new URLSearchParams();
	for (const [k, v] of Object.entries(params)) {
		if (v !== undefined && v !== null && v !== "") q.set(k, String(v));
	}
	const s = q.toString();
	return s ? `?${s}` : "";
}
