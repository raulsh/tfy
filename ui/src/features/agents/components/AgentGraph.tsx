import { Popover } from "antd";
import { Bot, Split } from "lucide-react";
import type { ReactNode } from "react";
import { Link, useSearchParams } from "react-router-dom";
import type { Agent, AgentRun } from "@/shared/api/types";
import { Mono } from "@/shared/components/misc";
import { StatusDot } from "@/shared/components/StatusTag";
import { compact, duration } from "@/shared/lib/format";
import type { Tone } from "@/shared/lib/status";
import { agentWorking } from "../hooks";

const statusView: Record<string, { label: string; tone: Tone }> = {
	working: { label: "Working", tone: "accent" },
	waiting: { label: "Waiting on its sub-agents", tone: "warn" },
	done: { label: "Finishing", tone: "ok" },
	completed: { label: "Done", tone: "ok" },
	failed: { label: "Failed", tone: "error" },
	killed: { label: "Killed", tone: "error" },
	stopped: { label: "Stopped", tone: "neutral" },
};

const view = (a: Agent) => statusView[a.status] ?? { label: a.status, tone: "neutral" as Tone };

// One run as a graph: its main agent, then the sub-agents each agent
// started, left to right.
export function AgentGraph({ run, now }: { run: AgentRun; now: number }) {
	const [, setParams] = useSearchParams();
	const openRun = () =>
		setParams((p) => {
			const n = new URLSearchParams(p);
			n.set("run", run.run_id);
			return n;
		});
	return (
		<div className="tf-tree">
			<MainNode run={run} now={now} onOpen={openRun} />
			<Kids run={run} parent="main" now={now} />
		</div>
	);
}

function Kids({ run, parent, now }: { run: AgentRun; parent: string; now: number }) {
	const kids = run.agents.filter((a) => (a.parent_id || "main") === parent);
	if (kids.length === 0) return null;
	return (
		<div className="tf-tree-kids">
			{kids.map((a) => (
				<div key={a.id} className={`tf-tree-kid${agentWorking(a) ? " tf-live" : ""}`}>
					<div className="tf-tree">
						<SubagentNode agent={a} now={now} />
						<Kids run={run} parent={a.id} now={now} />
					</div>
				</div>
			))}
		</div>
	);
}

function MainNode({ run, now, onOpen }: { run: AgentRun; now: number; onOpen: () => void }) {
	const a = run.main;
	const working = run.agents.filter(agentWorking).length;
	const status = a.status === "waiting" ? `Waiting on ${working} sub-agent${working === 1 ? "" : "s"}` : view(a).label;
	return (
		<Node
			agent={a}
			width={320}
			onClick={onOpen}
			title="Open the run"
			icon={<Bot size={14} />}
			heading={
				<>
					<span style={{ fontWeight: 600 }}>{run.kind}</span>
					{run.unit_id ? (
						<Link
							to={`/units/${run.unit_id}`}
							onClick={(e) => e.stopPropagation()}
							style={{ minWidth: 0, overflow: "hidden", textOverflow: "ellipsis", whiteSpace: "nowrap" }}
						>
							{run.unit_label} {run.unit_title}
						</Link>
					) : (
						<span className="faint">no unit</span>
					)}
				</>
			}
			status={status}
			meta={[
				a.model,
				`${a.tool_uses} tool call${a.tool_uses === 1 ? "" : "s"}`,
				run.subagents ? `${run.agents.length} sub-agent${run.agents.length === 1 ? "" : "s"}` : "sub-agents off",
				duration(now - Date.parse(run.started_at)),
			]}
		/>
	);
}

function SubagentNode({ agent: a, now }: { agent: Agent; now: number }) {
	const end = a.ended_at ? Date.parse(a.ended_at) : now;
	const node = (
		<Node
			agent={a}
			width={280}
			icon={<Split size={14} />}
			heading={
				<span
					title={a.name}
					style={{ fontWeight: 600, minWidth: 0, overflow: "hidden", textOverflow: "ellipsis", whiteSpace: "nowrap" }}
				>
					{a.name || "Sub-agent"}
				</span>
			}
			status={a.type ? `${view(a).label} · ${a.type}` : view(a).label}
			meta={[
				a.model,
				`${a.tool_uses} tool call${a.tool_uses === 1 ? "" : "s"}`,
				a.tokens ? `${compact(a.tokens)} tokens` : undefined,
				duration(end - Date.parse(a.started_at)),
			]}
		/>
	);
	if (!a.prompt && !a.report) return node;
	return (
		<Popover
			trigger="click"
			placement="bottomLeft"
			content={
				<div style={{ maxWidth: 460, fontSize: 12.5, lineHeight: 1.55 }}>
					{a.prompt && (
						<>
							<div className="faint" style={{ marginBottom: 2 }}>
								Asked to
							</div>
							<div style={{ whiteSpace: "pre-wrap", marginBottom: a.report ? 10 : 0 }}>{a.prompt}</div>
						</>
					)}
					{a.report && (
						<>
							<div className="faint" style={{ marginBottom: 2 }}>
								Reported
							</div>
							<div style={{ whiteSpace: "pre-wrap" }}>{a.report}</div>
						</>
					)}
				</div>
			}
		>
			<div>{node}</div>
		</Popover>
	);
}

function Node({
	agent: a,
	width,
	icon,
	heading,
	status,
	meta,
	onClick,
	title,
}: {
	agent: Agent;
	width: number;
	icon: ReactNode;
	heading: ReactNode;
	status: string;
	meta: (string | undefined)[];
	onClick?: () => void;
	title?: string;
}) {
	const { tone } = view(a);
	const live = agentWorking(a);
	return (
		<div
			role="button"
			tabIndex={0}
			title={title}
			onClick={onClick}
			onKeyDown={(e) => {
				if (onClick && (e.key === "Enter" || e.key === " ")) onClick();
			}}
			style={{
				width,
				flex: "none",
				padding: "10px 12px",
				border: "1px solid var(--tf-border)",
				borderRadius: 8,
				background: "var(--tf-bg)",
				boxShadow: `inset 3px 0 0 var(--tf-${tone})`,
				opacity: live || a.id === "main" ? 1 : 0.7,
				cursor: "pointer",
				fontSize: 12.5,
			}}
		>
			<div style={{ display: "flex", alignItems: "center", gap: 8, minWidth: 0, height: 22 }}>
				<StatusDot tone={tone} pulse={a.status === "working"} />
				<span style={{ color: "var(--tf-text3)", display: "inline-flex", flex: "none" }}>{icon}</span>
				{heading}
			</div>
			<div style={{ color: "var(--tf-text2)", marginTop: 2 }}>{status}</div>
			{a.activity && (
				<div
					className="mono"
					title={a.activity}
					style={{
						marginTop: 4,
						fontSize: 11.5,
						overflow: "hidden",
						textOverflow: "ellipsis",
						whiteSpace: "nowrap",
					}}
				>
					{a.activity}
				</div>
			)}
			<div style={{ marginTop: 6 }}>
				<Mono faint>{meta.filter(Boolean).join(" · ")}</Mono>
			</div>
		</div>
	);
}
