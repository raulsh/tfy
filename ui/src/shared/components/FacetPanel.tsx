import { Checkbox } from "antd";
import { ChevronDown, ChevronRight } from "lucide-react";
import { useState } from "react";
import type { FacetGroup } from "@/shared/lib/facets";

// A column of collapsible facet groups: checkbox, colored tick, label, and a
// right-aligned count, with "only" on hover.
export function FacetPanel({
	groups,
	onToggle,
	onOnly,
	onClear,
	total,
}: {
	groups: FacetGroup[];
	onToggle: (key: string, value: string) => void;
	onOnly: (key: string, value: string) => void;
	onClear: (key?: string) => void;
	total: number;
}) {
	const [collapsed, setCollapsed] = useState<Record<string, boolean>>({});
	const anySelected = groups.some((g) => g.options.some((o) => o.selected));
	return (
		<div
			style={{
				width: 240,
				flex: "none",
				borderRight: "1px solid var(--tf-border)",
				overflowY: "auto",
				paddingBottom: 24,
			}}
		>
			<div
				style={{
					display: "flex",
					justifyContent: "space-between",
					alignItems: "center",
					padding: "10px 14px",
					borderBottom: "1px solid var(--tf-border)",
				}}
			>
				<span
					style={{
						fontSize: 12,
						padding: "1px 8px",
						borderRadius: 10,
						border: "1px solid var(--tf-border)",
						color: "var(--tf-text2)",
					}}
				>
					{total} results
				</span>
				{anySelected && (
					<button type="button" onClick={() => onClear()} style={linkButton}>
						Clear all
					</button>
				)}
			</div>
			{groups.map((g) => {
				const isCollapsed = collapsed[g.key];
				if (g.options.length === 0) return null;
				return (
					<div key={g.key} style={{ borderBottom: "1px solid var(--tf-border)", padding: "6px 0" }}>
						<button
							type="button"
							onClick={() => setCollapsed((c) => ({ ...c, [g.key]: !c[g.key] }))}
							style={{
								...linkButton,
								display: "flex",
								alignItems: "center",
								gap: 6,
								width: "100%",
								padding: "6px 14px",
								color: "var(--tf-text)",
								fontWeight: 500,
							}}
						>
							{isCollapsed ? <ChevronRight size={14} /> : <ChevronDown size={14} />}
							{g.label}
						</button>
						{!isCollapsed &&
							g.options.map((o) => (
								<FacetRow
									key={o.value}
									label={o.label}
									count={o.count}
									color={o.color}
									selected={o.selected}
									onToggle={() => onToggle(g.key, o.value)}
									onOnly={() => onOnly(g.key, o.value)}
								/>
							))}
					</div>
				);
			})}
		</div>
	);
}

function FacetRow({
	label,
	count,
	color,
	selected,
	onToggle,
	onOnly,
}: {
	label: string;
	count: number;
	color?: string;
	selected: boolean;
	onToggle: () => void;
	onOnly: () => void;
}) {
	const [hover, setHover] = useState(false);
	return (
		<div
			onMouseEnter={() => setHover(true)}
			onMouseLeave={() => setHover(false)}
			style={{
				display: "flex",
				alignItems: "center",
				gap: 8,
				padding: "3px 14px 3px 16px",
				background: hover ? "var(--tf-hover)" : undefined,
				cursor: "pointer",
			}}
			onClick={onToggle}
		>
			<Checkbox checked={selected} onClick={(e) => e.stopPropagation()} onChange={onToggle} />
			<span style={{ width: 3, height: 14, borderRadius: 1, background: color ?? "var(--tf-border)", flex: "none" }} />
			<span
				style={{
					flex: 1,
					overflow: "hidden",
					textOverflow: "ellipsis",
					whiteSpace: "nowrap",
					color: count === 0 ? "var(--tf-text3)" : "var(--tf-text)",
				}}
			>
				{label}
			</span>
			{hover ? (
				<button
					type="button"
					onClick={(e) => {
						e.stopPropagation();
						onOnly();
					}}
					style={{ ...linkButton, fontSize: 11, fontWeight: 600, letterSpacing: 0.4 }}
				>
					ONLY
				</button>
			) : (
				<span style={{ color: "var(--tf-text2)", fontSize: 12, fontVariantNumeric: "tabular-nums" }}>{count}</span>
			)}
		</div>
	);
}

const linkButton = {
	border: 0,
	background: "none",
	padding: 0,
	cursor: "pointer",
	color: "var(--tf-accent)",
	fontSize: 12,
	fontFamily: "inherit",
	textAlign: "left" as const,
};
