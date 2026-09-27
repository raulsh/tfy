import {
	Bot,
	ChevronDown,
	ChevronRight,
	CircleAlert,
	Flag,
	GitCommitHorizontal,
	Play,
	ShieldAlert,
	Terminal,
} from "lucide-react";
import { type ReactNode, useEffect, useMemo, useRef, useState } from "react";
import { status as statusColors, toolColors } from "@/app/theme/tokens";
import type { RunEvent } from "@/shared/api/types";
import { JsonView } from "@/shared/components/misc";
import { duration } from "@/shared/lib/format";

// A row of the waterfall: a span (a tool call, from its request to its
// result) or a point (a message, a denial, the result).
interface Row {
	key: string;
	start: number;
	end?: number; // spans only
	icon: ReactNode;
	label: string;
	detail: string;
	color: string;
	error?: boolean;
	payload: unknown;
}

interface ContentBlock {
	type: string;
	text?: string;
	id?: string;
	name?: string;
	input?: Record<string, unknown>;
	tool_use_id?: string;
	is_error?: boolean;
	content?: unknown;
}

function blocks(ev: RunEvent): ContentBlock[] {
	const msg = ev.payload?.message as { content?: unknown } | undefined;
	return Array.isArray(msg?.content) ? (msg.content as ContentBlock[]) : [];
}

function inputSummary(input: Record<string, unknown> | undefined): string {
	if (!input) return "";
	for (const k of ["command", "file_path", "notebook_path", "url", "query", "pattern", "description"]) {
		const v = input[k];
		if (typeof v === "string" && v) return v;
	}
	return JSON.stringify(input);
}

function resultText(b: ContentBlock): string {
	if (typeof b.content === "string") return b.content;
	if (Array.isArray(b.content)) return b.content.map((c: { text?: string }) => c.text ?? "").join("");
	return "";
}

function buildRows(events: RunEvent[]): Row[] {
	const rows: Row[] = [];
	const byToolUse = new Map<string, Row>();
	for (const ev of events) {
		const at = new Date(ev.at).getTime();
		const p = ev.payload as Record<string, unknown>;
		switch (ev.type) {
			case "system":
				if (ev.subtype === "init") {
					rows.push({
						key: `${ev.seq}`,
						start: at,
						icon: <Play size={13} />,
						label: "Session started",
						detail: `${p.model ?? ""} · ${p.permissionMode ?? ""} mode · ${(p.tools as string[] | undefined)?.length ?? 0} tools`,
						color: statusColors.info,
						payload: p,
					});
				} else if (ev.subtype === "hook_response" && (p.exit_code as number) !== 0) {
					rows.push({
						key: `${ev.seq}`,
						start: at,
						icon: <ShieldAlert size={13} />,
						label: (p.exit_code as number) === 2 ? "Guard blocked a command" : "Guard failed",
						detail: String(p.stderr ?? "").trim(),
						color: statusColors.amber,
						error: (p.exit_code as number) !== 2,
						payload: p,
					});
				} else if (ev.subtype === "permission_denied") {
					rows.push({
						key: `${ev.seq}`,
						start: at,
						icon: <ShieldAlert size={13} />,
						label: `Denied ${p.tool_name ?? ""}`,
						detail: String(p.message ?? ""),
						color: statusColors.amber,
						payload: p,
					});
				} else if (ev.subtype === "vcs_state_changed") {
					rows.push({
						key: `${ev.seq}`,
						start: at,
						icon: <GitCommitHorizontal size={13} />,
						label: `git ${p.kind ?? ""}`,
						detail: String(p.cwd ?? "")
							.split("/")
							.slice(-2)
							.join("/"),
						color: p.kind === "push" ? statusColors.error : statusColors.ok,
						payload: p,
					});
				}
				break;
			case "assistant":
				for (const [i, b] of blocks(ev).entries()) {
					if (b.type === "text" && b.text?.trim()) {
						rows.push({
							key: `${ev.seq}.${i}`,
							start: at,
							icon: <Bot size={13} />,
							label: "Claude",
							detail: b.text.trim(),
							color: "var(--tf-text3)",
							payload: b,
						});
					} else if (b.type === "tool_use") {
						const row: Row = {
							key: `${ev.seq}.${i}`,
							start: at,
							end: undefined,
							icon: <Terminal size={13} />,
							label: b.name ?? "tool",
							detail: inputSummary(b.input),
							color: toolColors[b.name ?? ""] ?? "#B9C3D0",
							payload: { tool_use: b },
						};
						rows.push(row);
						if (b.id) byToolUse.set(b.id, row);
					}
				}
				break;
			case "user":
				for (const b of blocks(ev)) {
					if (b.type !== "tool_result" || !b.tool_use_id) continue;
					const row = byToolUse.get(b.tool_use_id);
					if (!row) continue;
					row.end = at;
					row.error = b.is_error;
					row.payload = { ...(row.payload as object), result: resultText(b).slice(0, 20000) };
				}
				break;
			case "result":
				rows.push({
					key: `${ev.seq}`,
					start: at,
					icon: p.is_error ? <CircleAlert size={13} /> : <Flag size={13} />,
					label: p.is_error ? `Finished with ${p.subtype}` : "Finished",
					detail: `${p.num_turns ?? 0} turns · $${Number(p.total_cost_usd ?? 0).toFixed(4)}${typeof p.result === "string" && p.result ? ` · ${p.result}` : ""}`,
					color: p.is_error ? statusColors.error : statusColors.ok,
					error: Boolean(p.is_error),
					payload: p,
				});
				break;
			case "log":
				rows.push({
					key: `${ev.seq}`,
					start: at,
					icon: <Terminal size={13} />,
					label: "output",
					detail: String(p.text ?? ""),
					color: "var(--tf-text3)",
					payload: p,
				});
				break;
		}
	}
	return rows;
}

// The run as a trace: time offset, what happened, and a waterfall bar.
export function EventTimeline({ events, live }: { events: RunEvent[]; live: boolean }) {
	const rows = useMemo(() => buildRows(events), [events]);
	const [open, setOpen] = useState<Record<string, boolean>>({});
	const [now, setNow] = useState(Date.now());
	const bottom = useRef<HTMLDivElement>(null);
	const follow = useRef(true);

	useEffect(() => {
		if (!live) return;
		const t = window.setInterval(() => setNow(Date.now()), 1000);
		return () => window.clearInterval(t);
	}, [live]);

	// Keep the newest event in view while live, unless the reader scrolled up.
	const count = rows.length;
	useEffect(() => {
		if (live && follow.current && count > 0) bottom.current?.scrollIntoView({ block: "nearest" });
	}, [count, live]);

	if (rows.length === 0)
		return (
			<div className="faint" style={{ padding: 16 }}>
				{live ? "Waiting for the first events…" : "No events recorded."}
			</div>
		);

	const t0 = rows[0].start;
	const tEnd = Math.max(live ? now : 0, ...rows.map((r) => r.end ?? r.start), t0 + 1000);
	const total = tEnd - t0;

	return (
		<div
			onScroll={(e) => {
				const el = e.currentTarget;
				follow.current = el.scrollHeight - el.scrollTop - el.clientHeight < 40;
			}}
			style={{ fontSize: 12.5 }}
		>
			{rows.map((r) => {
				const isOpen = open[r.key];
				const left = ((r.start - t0) / total) * 100;
				// A span still open is drawn up to now while the run is live.
				const spanEnd = isSpan(r) ? (r.end ?? (live ? now : r.start)) : undefined;
				const width = spanEnd !== undefined ? Math.max(((spanEnd - r.start) / total) * 100, 0.6) : 0;
				return (
					<div key={r.key} style={{ borderTop: "1px solid var(--tf-border)" }}>
						<div
							onClick={() => setOpen((o) => ({ ...o, [r.key]: !o[r.key] }))}
							style={{
								display: "grid",
								gridTemplateColumns: "16px 64px minmax(0, 1fr) 34%",
								gap: 10,
								alignItems: "center",
								padding: "6px 12px",
								cursor: "pointer",
								boxShadow: `inset 3px 0 0 ${r.error ? statusColors.error : "transparent"}`,
							}}
						>
							<span style={{ color: "var(--tf-text3)" }}>
								{isOpen ? <ChevronDown size={13} /> : <ChevronRight size={13} />}
							</span>
							<span className="mono faint" style={{ fontSize: 11.5, textAlign: "right" }}>
								+{duration(r.start - t0)}
							</span>
							<span style={{ display: "flex", gap: 8, alignItems: "center", minWidth: 0 }}>
								<span style={{ color: r.color, display: "inline-flex", flex: "none" }}>{r.icon}</span>
								<span style={{ fontWeight: 500, flex: "none" }}>{r.label}</span>
								<span
									className={isSpan(r) ? "mono" : undefined}
									style={{
										color: "var(--tf-text2)",
										overflow: "hidden",
										textOverflow: "ellipsis",
										whiteSpace: "nowrap",
										fontSize: isSpan(r) ? 12 : undefined,
									}}
								>
									{r.detail}
								</span>
							</span>
							<span style={{ position: "relative", height: 14 }}>
								<span style={{ position: "absolute", inset: "6px 0", background: "var(--tf-fill)", borderRadius: 1 }} />
								{width > 0 ? (
									<span
										title={duration((spanEnd ?? r.start) - r.start)}
										style={{
											position: "absolute",
											left: `${left}%`,
											width: `${Math.min(width, 100 - left)}%`,
											top: 2,
											bottom: 2,
											background: r.error ? statusColors.error : r.color,
											borderRadius: 2,
											opacity: r.end === undefined ? 0.6 : 1,
										}}
									/>
								) : (
									<span
										style={{
											position: "absolute",
											left: `calc(${left}% - 1px)`,
											width: 2,
											top: 1,
											bottom: 1,
											background: r.color,
										}}
									/>
								)}
							</span>
						</div>
						{isOpen && (
							<div style={{ padding: "0 12px 10px 100px" }}>
								{r.label === "Claude" ? (
									<div style={{ whiteSpace: "pre-wrap", lineHeight: 1.55 }}>{r.detail}</div>
								) : (
									<JsonView value={r.payload} />
								)}
							</div>
						)}
					</div>
				);
			})}
			<div ref={bottom} />
		</div>
	);
}

function isSpan(r: Row): boolean {
	return "end" in r;
}
