import { Search } from "lucide-react";
import { type ReactNode, useEffect, useRef, useState } from "react";
import type { FacetGroup } from "@/shared/lib/facets";
import { Chip } from "./Chip";

// The filter bar: removable key | value chips for the active facets, and free
// text. "/" focuses it from anywhere.
export function QueryBar({
	groups,
	search,
	onSearch,
	onRemove,
	extra,
}: {
	groups: FacetGroup[];
	search: string;
	onSearch: (q: string) => void;
	onRemove: (key: string, value: string) => void;
	extra?: ReactNode;
}) {
	const input = useRef<HTMLInputElement>(null);
	const [draft, setDraft] = useState(search);
	useEffect(() => setDraft(search), [search]);
	useEffect(() => {
		const t = window.setTimeout(() => {
			if (draft !== search) onSearch(draft);
		}, 200);
		return () => window.clearTimeout(t);
	}, [draft, search, onSearch]);
	useEffect(() => {
		const onKey = (e: KeyboardEvent) => {
			const el = e.target as HTMLElement;
			if (e.key === "/" && !["INPUT", "TEXTAREA"].includes(el.tagName) && !el.isContentEditable) {
				e.preventDefault();
				input.current?.focus();
			}
		};
		window.addEventListener("keydown", onKey);
		return () => window.removeEventListener("keydown", onKey);
	}, []);

	const chips = groups.flatMap((g) => g.options.filter((o) => o.selected).map((o) => ({ g, o })));
	return (
		<div
			style={{
				display: "flex",
				gap: 10,
				alignItems: "center",
				padding: "10px 16px",
				borderBottom: "1px solid var(--tf-border)",
			}}
		>
			<div
				onClick={() => input.current?.focus()}
				style={{
					flex: 1,
					display: "flex",
					flexWrap: "wrap",
					alignItems: "center",
					gap: 6,
					minHeight: 32,
					padding: "4px 10px",
					border: "1px solid var(--tf-border)",
					borderRadius: 4,
					background: "var(--tf-bg)",
					cursor: "text",
				}}
			>
				<Search size={14} color="var(--tf-text3)" />
				{chips.map(({ g, o }) => (
					<Chip
						key={`${g.key}:${o.value}`}
						k={g.label.toLowerCase()}
						v={o.label}
						mono
						onRemove={() => onRemove(g.key, o.value)}
					/>
				))}
				<input
					ref={input}
					value={draft}
					onChange={(e) => setDraft(e.target.value)}
					onKeyDown={(e) => {
						if (e.key === "Escape") {
							setDraft("");
							onSearch("");
						}
					}}
					placeholder={chips.length ? "" : "Type / to filter…"}
					style={{
						flex: 1,
						minWidth: 160,
						border: 0,
						outline: "none",
						background: "transparent",
						color: "var(--tf-text)",
						fontFamily: "var(--tf-mono)",
						fontSize: 12.5,
					}}
				/>
			</div>
			{extra}
		</div>
	);
}
