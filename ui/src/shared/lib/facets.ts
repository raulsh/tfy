import { useCallback, useMemo } from "react";
import { useSearchParams } from "react-router-dom";

// Faceted filtering, groundcover style: values of one facet combine with OR,
// different facets with AND. Counts for a facet ignore that facet's own
// selection, so every option shows what picking it would give.

export interface FacetDef<T> {
	key: string;
	label: string;
	get: (item: T) => string | undefined;
	format?: (value: string) => string;
	color?: (value: string) => string;
	order?: string[];
}

export interface FacetOption {
	value: string;
	label: string;
	count: number;
	color?: string;
	selected: boolean;
}

export interface FacetGroup {
	key: string;
	label: string;
	options: FacetOption[];
}

export type Selection = Record<string, string[]>;

function matches<T>(item: T, defs: FacetDef<T>[], sel: Selection, skip?: string): boolean {
	for (const d of defs) {
		if (d.key === skip) continue;
		const chosen = sel[d.key];
		if (chosen?.length && !chosen.includes(d.get(item) ?? "")) return false;
	}
	return true;
}

export function useFacetedList<T>(
	items: T[] | undefined,
	defs: FacetDef<T>[],
	text: (item: T) => string,
): {
	filtered: T[];
	groups: FacetGroup[];
	selection: Selection;
	search: string;
	toggle: (key: string, value: string) => void;
	only: (key: string, value: string) => void;
	clear: (key?: string) => void;
	setSearch: (q: string) => void;
} {
	const [params, setParams] = useSearchParams();

	const selection = useMemo(() => {
		const s: Selection = {};
		for (const d of defs) {
			const vals = params.getAll(d.key);
			if (vals.length) s[d.key] = vals;
		}
		return s;
	}, [params, defs]);
	const search = params.get("q") ?? "";

	const filtered = useMemo(() => {
		const q = search.trim().toLowerCase();
		return (items ?? []).filter((it) => matches(it, defs, selection) && (!q || text(it).toLowerCase().includes(q)));
	}, [items, defs, selection, search, text]);

	const groups = useMemo(
		() =>
			defs.map((d) => {
				const counts = new Map<string, number>();
				for (const it of items ?? []) {
					if (!matches(it, defs, selection, d.key)) continue;
					const v = d.get(it) ?? "";
					if (v) counts.set(v, (counts.get(v) ?? 0) + 1);
				}
				for (const v of selection[d.key] ?? []) if (!counts.has(v)) counts.set(v, 0);
				let values = [...counts.keys()];
				if (d.order) {
					values = values.sort((a, b) => {
						const ia = d.order!.indexOf(a);
						const ib = d.order!.indexOf(b);
						return (ia < 0 ? 999 : ia) - (ib < 0 ? 999 : ib);
					});
				} else {
					values = values.sort((a, b) => (counts.get(b) ?? 0) - (counts.get(a) ?? 0));
				}
				return {
					key: d.key,
					label: d.label,
					options: values.map((v) => ({
						value: v,
						label: d.format ? d.format(v) : v,
						count: counts.get(v) ?? 0,
						color: d.color?.(v),
						selected: selection[d.key]?.includes(v) ?? false,
					})),
				};
			}),
		[items, defs, selection],
	);

	const update = useCallback(
		(fn: (p: URLSearchParams) => void) => {
			setParams(
				(prev) => {
					const next = new URLSearchParams(prev);
					fn(next);
					return next;
				},
				{ replace: true },
			);
		},
		[setParams],
	);

	return {
		filtered,
		groups,
		selection,
		search,
		toggle: (key, value) =>
			update((p) => {
				const vals = p.getAll(key);
				p.delete(key);
				const next = vals.includes(value) ? vals.filter((v) => v !== value) : [...vals, value];
				for (const v of next) p.append(key, v);
			}),
		only: (key, value) =>
			update((p) => {
				p.delete(key);
				p.append(key, value);
			}),
		clear: (key) =>
			update((p) => {
				if (key) p.delete(key);
				else for (const d of defs) p.delete(d.key);
			}),
		setSearch: (q) =>
			update((p) => {
				if (q) p.set("q", q);
				else p.delete("q");
			}),
	};
}
