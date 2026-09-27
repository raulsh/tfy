import { AutoComplete } from "antd";
import { type CSSProperties, useEffect, useState } from "react";
import type { IssueOption } from "@/shared/api/types";
import { Mono } from "@/shared/components/misc";
import { useProjectIssues } from "../hooks";

// Typed text that is a reference or a link, not words to search for.
const refLike = /^(https?:\/\/|#?\d+$|[\w.-]+\/[\w.-]+#\d+$)/;

// The kind a unit gets from an issue's labels, as the server guesses it.
export function kindFromLabels(labels: string[]): string {
	const names = labels.map((l) => l.trim().toLowerCase());
	if (names.some((n) => n.includes("bug") || n === "defect" || n === "regression")) return "bugfix";
	if (
		names.some((n) =>
			["chore", "maintenance", "dependencies", "refactor", "tech debt", "technical debt", "ci"].includes(n),
		)
	)
		return "chore";
	if (names.some((n) => ["improvement", "performance", "ux", "polish"].includes(n))) return "improvement";
	return "feature";
}

// A search over the project's open issues that also takes a link,
// owner/repo#12 or #12 as typed.
export function IssuePicker({
	projectId,
	value,
	onChange,
	onPick,
	placeholder,
	style,
}: {
	projectId?: string;
	value?: string;
	onChange?: (v: string) => void;
	onPick?: (o: IssueOption) => void;
	placeholder?: string;
	style?: CSSProperties;
}) {
	const [focused, setFocused] = useState(false);
	const [q, setQ] = useState("");
	useEffect(() => {
		const t = setTimeout(() => setQ((value ?? "").trim()), 300);
		return () => clearTimeout(t);
	}, [value]);
	const typedRef = refLike.test(q);
	const issues = useProjectIssues(projectId, typedRef ? "" : q, focused && !typedRef);
	return (
		<AutoComplete
			style={style}
			value={value}
			filterOption={false}
			popupMatchSelectWidth={560}
			options={(issues.data ?? []).map((o) => ({
				value: o.ref,
				label: (
					<div style={{ display: "flex", gap: 8, alignItems: "baseline", minWidth: 0 }}>
						<Mono faint>{o.ref}</Mono>
						<span style={{ flex: 1, minWidth: 0, overflow: "hidden", textOverflow: "ellipsis" }}>{o.title}</span>
						{o.units.length > 0 && (
							<span className="faint" style={{ fontSize: 12 }}>
								{o.units.join(", ")}
							</span>
						)}
					</div>
				),
			}))}
			onChange={(v) => onChange?.(v)}
			onSelect={(v) => {
				onChange?.(v);
				const o = issues.data?.find((x) => x.ref === v);
				if (o) onPick?.(o);
			}}
			onFocus={() => setFocused(true)}
			placeholder={placeholder ?? "Search open issues, or paste a link or owner/repo#12"}
			notFoundContent={
				issues.isFetching
					? "Searching…"
					: issues.error
						? (issues.error as Error).message
						: focused && q
							? "No open issue matches"
							: undefined
			}
		/>
	);
}
