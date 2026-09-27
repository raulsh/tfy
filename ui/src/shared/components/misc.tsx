import { Tooltip } from "antd";
import type { ReactNode } from "react";
import ReactMarkdown from "react-markdown";
import remarkGfm from "remark-gfm";
import { absTime, timeAgo } from "@/shared/lib/format";

// Key | Value rows.
export function KVTable({ rows }: { rows: [ReactNode, ReactNode][] }) {
	return (
		<table style={{ width: "100%", borderCollapse: "collapse" }}>
			<tbody>
				{rows.map(([k, v], i) => (
					<tr key={i} style={{ borderTop: i ? "1px solid var(--tf-border)" : undefined }}>
						<td
							style={{
								padding: "9px 16px",
								width: 200,
								color: "var(--tf-text2)",
								verticalAlign: "top",
								fontWeight: 500,
							}}
						>
							{k}
						</td>
						<td style={{ padding: "9px 16px", verticalAlign: "top", wordBreak: "break-word" }}>{v}</td>
					</tr>
				))}
			</tbody>
		</table>
	);
}

export function TimeAgo({ at }: { at: string | null | undefined }) {
	if (!at) return <span className="faint">—</span>;
	return (
		<Tooltip title={absTime(at)}>
			<span style={{ whiteSpace: "nowrap" }}>{timeAgo(at)}</span>
		</Tooltip>
	);
}

// HTML comments are hidden, as GitHub hides them: tfy's own markers, and
// the notes in pull request templates.
const htmlComment = /<!--[\s\S]*?-->/g;

export function Markdown({ children }: { children: string }) {
	return (
		<div className="tf-markdown">
			<ReactMarkdown remarkPlugins={[remarkGfm]}>{children.replace(htmlComment, "")}</ReactMarkdown>
		</div>
	);
}

export function PageHeader({ title, extra, children }: { title: ReactNode; extra?: ReactNode; children?: ReactNode }) {
	return (
		<div style={{ padding: "14px 16px 12px", borderBottom: "1px solid var(--tf-border)" }}>
			<div style={{ display: "flex", alignItems: "center", gap: 12 }}>
				<h1 style={{ margin: 0, fontSize: 18, fontWeight: 500, flex: 1 }}>{title}</h1>
				{extra}
			</div>
			{children}
		</div>
	);
}

// A KPI tile for the summary strip.
export function Metric({
	label,
	value,
	hint,
	tone,
}: {
	label: string;
	value: ReactNode;
	hint?: ReactNode;
	tone?: string;
}) {
	return (
		<div
			style={{
				minWidth: 130,
				padding: "10px 14px",
				border: "1px solid var(--tf-border)",
				borderRadius: 8,
				background: "var(--tf-bg)",
				boxShadow: tone ? `inset 3px 0 0 ${tone}` : undefined,
			}}
		>
			<div style={{ fontSize: 12, color: "var(--tf-text2)" }}>{label}</div>
			<div style={{ fontSize: 20, fontWeight: 500, fontVariantNumeric: "tabular-nums", lineHeight: 1.3 }}>{value}</div>
			{hint && <div style={{ fontSize: 11.5, color: "var(--tf-text3)" }}>{hint}</div>}
		</div>
	);
}

export function EmptyState({ icon, title, children }: { icon?: ReactNode; title: string; children?: ReactNode }) {
	return (
		<div style={{ padding: "64px 24px", textAlign: "center", color: "var(--tf-text2)" }}>
			{icon && <div style={{ marginBottom: 12, color: "var(--tf-text3)" }}>{icon}</div>}
			<div style={{ fontSize: 15, fontWeight: 500, color: "var(--tf-text)", marginBottom: 6 }}>{title}</div>
			<div style={{ maxWidth: 460, margin: "0 auto" }}>{children}</div>
		</div>
	);
}

export function JsonView({ value, maxHeight = 360 }: { value: unknown; maxHeight?: number }) {
	const text = typeof value === "string" ? value : JSON.stringify(value, null, 2);
	return (
		<pre
			style={{
				margin: 0,
				padding: "10px 12px",
				background: "var(--tf-fill)",
				borderRadius: 4,
				fontSize: 12,
				lineHeight: 1.5,
				maxHeight,
				overflow: "auto",
				whiteSpace: "pre-wrap",
				wordBreak: "break-word",
			}}
		>
			{text}
		</pre>
	);
}

export function Mono({ children, faint }: { children: ReactNode; faint?: boolean }) {
	return (
		<span style={{ fontFamily: "var(--tf-mono)", fontSize: 12, color: faint ? "var(--tf-text2)" : undefined }}>
			{children}
		</span>
	);
}
