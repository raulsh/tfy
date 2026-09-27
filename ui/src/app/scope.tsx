import dayjs from "dayjs";
import { createContext, type ReactNode, useContext, useMemo, useState } from "react";

export type Range = "24h" | "7d" | "30d" | "all";

export const ranges: { value: Range; label: string }[] = [
	{ value: "24h", label: "Last 24 hours" },
	{ value: "7d", label: "Last 7 days" },
	{ value: "30d", label: "Last 30 days" },
	{ value: "all", label: "All time" },
];

interface ScopeValue {
	projectId: string | undefined;
	setProjectId: (id: string | undefined) => void;
	range: Range;
	setRange: (r: Range) => void;
	inRange: (iso: string) => boolean;
}

const ScopeContext = createContext<ScopeValue | null>(null);

function stored<T extends string>(key: string, fallback: T | undefined): T | undefined {
	try {
		return (localStorage.getItem(key) as T | null) ?? fallback;
	} catch {
		return fallback;
	}
}

function store(key: string, value: string | undefined) {
	try {
		if (value) localStorage.setItem(key, value);
		else localStorage.removeItem(key);
	} catch {
		// per-viewer convenience only
	}
}

// Scope is the project and time window the list pages show.
export function ScopeProvider({ children }: { children: ReactNode }) {
	const [projectId, setProject] = useState<string | undefined>(() => stored("thefactory-project", undefined));
	const [range, setRangeState] = useState<Range>(() => stored<Range>("thefactory-range", "all") ?? "all");
	const value = useMemo<ScopeValue>(() => {
		const since = range === "all" ? null : dayjs().subtract(range === "24h" ? 1 : range === "7d" ? 7 : 30, "day");
		return {
			projectId,
			setProjectId: (id) => {
				setProject(id);
				store("thefactory-project", id);
			},
			range,
			setRange: (r) => {
				setRangeState(r);
				store("thefactory-range", r);
			},
			inRange: (iso) => !since || dayjs(iso).isAfter(since),
		};
	}, [projectId, range]);
	return <ScopeContext.Provider value={value}>{children}</ScopeContext.Provider>;
}

export function useScope(): ScopeValue {
	const v = useContext(ScopeContext);
	if (!v) throw new Error("useScope outside ScopeProvider");
	return v;
}
