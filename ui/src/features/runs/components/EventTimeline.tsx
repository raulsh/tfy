import {
	Bot,
	ChevronDown,
	ChevronRight,
	CircleAlert,
	Flag,
	GitCommitHorizontal,
	Play,
	ShieldAlert,
	Split,
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
	// A sub-agent's rows hang under the Agent call that started it.
	parent?: string; // that call's tool use id
	depth?: number; // 0, the main agent's, when unset
	text?: boolean; // a message, shown as prose when opened
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
	// Sub-agents: the Agent call's row by task, and each call's depth.
	const byTask = new Map<string, Row>();
	const depthOf = new Map<string, number>();
	let lastResult: Row | undefined;
	let started = false;
	for (const ev of events) {
		const at = new Date(ev.at).getTime();
		const p = ev.payload as Record<string, unknown>;
		const parent = typeof p.parent_tool_use_id === "string" ? p.parent_tool_use_id : undefined;
		const depth = parent ? (depthOf.get(parent) ?? 0) + 1 : 0;
		const agent = parent ? byToolUse.get(parent) : undefined;
		switch (ev.type) {
			case "system":
				if (ev.subtype === "init" && started) {
					// The session announces itself again at every turn; after a
					// result, a sub-agent reported back and woke it.
					if (lastResult) {
						lastResult.label = "Turn ended";
						lastResult.color = "var(--tf-text3)";
						lastResult = undefined;
						rows.push({
							key: `${ev.seq}`,
							start: at,
							icon: <Play size={13} />,
							label: "Resumed",
							detail: "a sub-agent reported back",
							color: statusColors.info,
							payload: p,
						});
					}
				} else if (ev.subtype === "task_started" && p.task_type === "local_agent") {
					const row = byToolUse.get(String(p.tool_use_id ?? ""));
					if (row) {
						byTask.set(String(p.task_id), row);
						row.end = undefined; // until the sub-agent ends, not its launch
						row.payload = { ...(row.payload as object), task: p };
					}
				} else if (
					(ev.subtype === "task_updated" || ev.subtype === "task_notification") &&
					byTask.has(String(p.task_id))
				) {
					const row = byTask.get(String(p.task_id)) as Row;
					const s = String((p.patch as { status?: string } | undefined)?.status ?? p.status ?? "");
					if (s && s !== "running" && s !== "pending") {
						row.end ??= at;
						if (s === "failed" || s === "killed") row.error = true;
					}
				} else if (ev.subtype === "init") {
					started = true;
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
							label: agent ? agentName(agent) : "Claude",
							detail: b.text.trim(),
							color: "var(--tf-text3)",
							payload: b,
							text: true,
							parent,
							depth,
						});
					} else if (b.type === "tool_use" && b.name === "SubagentHandback") {
						rows.push({
							key: `${ev.seq}.${i}`,
							start: at,
							icon: <Bot size={13} />,
							label: "Reported back",
							detail: String(b.input?.message ?? ""),
							color: toolColors.Agent,
							payload: { tool_use: b },
							text: true,
							parent,
							depth,
						});
					} else if (b.type === "tool_use") {
						const isAgent = b.name === "Agent";
						const row: Row = {
							key: `${ev.seq}.${i}`,
							start: at,
							end: undefined,
							icon: isAgent ? <Split size={13} /> : <Terminal size={13} />,
							label: isAgent ? "Sub-agent" : (b.name ?? "tool"),
							detail: isAgent
								? `${b.input?.description ?? ""}${b.input?.subagent_type ? ` · ${b.input.subagent_type}` : ""}`
								: inputSummary(b.input),
							color: toolColors[b.name ?? ""] ?? "#B9C3D0",
							payload: { tool_use: b },
							parent,
							depth,
						};
						rows.push(row);
						if (b.id) {
							byToolUse.set(b.id, row);
							depthOf.set(b.id, depth);
						}
					}
				}
				break;
			case "user":
				for (const b of blocks(ev)) {
					if (b.type !== "tool_result" || !b.tool_use_id) continue;
					const row = byToolUse.get(b.tool_use_id);
					if (!row) continue;
					// A sub-agent launched in the background answers at once;
					// its row ends when the sub-agent does.
					if ((row.payload as { task?: unknown }).task === undefined) row.end = at;
					row.error = b.is_error;
					row.payload = { ...(row.payload as object), result: resultText(b).slice(0, 20000) };
				}
				break;
			case "result":
				lastResult = {
					key: `${ev.seq}`,
					start: at,
					icon: p.is_error ? <CircleAlert size={13} /> : <Flag size={13} />,
					label: p.is_error ? `Finished with ${p.subtype}` : "Finished",
					detail: `${p.num_turns ?? 0} turns · $${Number(p.total_cost_usd ?? 0).toFixed(4)}${typeof p.result === "string" && p.result ? ` · ${p.result}` : ""}`,
					color: p.is_error ? statusColors.error : statusColors.ok,
					error: Boolean(p.is_error),
					payload: p,
				};
				rows.push(lastResult);
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
	return nest(rows);
}

// agentName is how a sub-agent's own rows are labelled.
function agentName(call: Row): string {
	const name = call.detail.split(" · ")[0] || "Sub-agent";
	return name.length > 28 ? `${name.slice(0, 27)}…` : name;
}

// nest orders the rows as a tree: each sub-agent's rows, in time order,
// right under the Agent call that started it.
function nest(rows: Row[]): Row[] {
	const callOf = (r: Row) => (r.payload as { tool_use?: ContentBlock }).tool_use?.id;
	const calls = new Set(rows.map(callOf).filter(Boolean));
	const kids = new Map<string, Row[]>();
	const top: Row[] = [];
	for (const r of rows) {
		if (r.parent && calls.has(r.parent)) kids.set(r.parent, [...(kids.get(r.parent) ?? []), r]);
		else top.push(r);
	}
	const out: Row[] = [];
	const walk = (list: Row[]) => {
		for (const r of list) {
			out.push(r);
			const id = callOf(r);
			if (id && kids.has(id)) walk(kids.get(id) as Row[]);
		}
	};
	walk(top);
	return out;
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
							<span
								style={{
									display: "flex",
									gap: 8,
									alignItems: "center",
									minWidth: 0,
									paddingLeft: Math.max((r.depth ?? 0) - 1, 0) * 16,
								}}
							>
								{(r.depth ?? 0) > 0 && (
									<span style={{ width: 2, height: 16, flex: "none", borderRadius: 1, background: toolColors.Agent }} />
								)}
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
								{r.text ? (
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
