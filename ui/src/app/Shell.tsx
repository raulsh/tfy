import { Select, Tooltip } from "antd";
import {
	Activity,
	Boxes,
	ChevronsLeft,
	ChevronsRight,
	FolderGit2,
	Gauge,
	Inbox,
	Network,
	Settings,
} from "lucide-react";
import { type ReactNode, useState } from "react";
import { NavLink, Outlet, useSearchParams } from "react-router-dom";
import { useAgents, workingAgents } from "@/features/agents/hooks";
import { useProjects } from "@/features/projects/hooks";
import { RunDrawer } from "@/features/runs/components/RunDrawer";
import { useStats } from "@/shared/api/system";
import { StatusDot } from "@/shared/components/StatusTag";
import { type LiveState, useLiveUpdates } from "@/shared/hooks/useLiveUpdates";
import { useScope } from "./scope";

const nav: { to: string; label: string; icon: ReactNode; badge?: "inbox" | "agents" }[] = [
	{ to: "/overview", label: "Overview", icon: <Gauge size={17} strokeWidth={1.6} /> },
	{ to: "/units", label: "Units", icon: <Boxes size={17} strokeWidth={1.6} /> },
	{ to: "/inbox", label: "Inbox", icon: <Inbox size={17} strokeWidth={1.6} />, badge: "inbox" },
	{ to: "/agents", label: "Agents", icon: <Network size={17} strokeWidth={1.6} />, badge: "agents" },
	{ to: "/runs", label: "Runs", icon: <Activity size={17} strokeWidth={1.6} /> },
	{ to: "/projects", label: "Projects", icon: <FolderGit2 size={17} strokeWidth={1.6} /> },
	{ to: "/settings", label: "Settings", icon: <Settings size={17} strokeWidth={1.6} /> },
];

function readCollapsed(): boolean {
	try {
		return localStorage.getItem("tfy-nav") === "collapsed";
	} catch {
		return false;
	}
}

export function Shell() {
	const live = useLiveUpdates();
	const [collapsed, setCollapsed] = useState(readCollapsed);
	const [params, setParams] = useSearchParams();
	const runId = params.get("run") ?? undefined;

	const toggle = () => {
		setCollapsed((c) => {
			try {
				localStorage.setItem("tfy-nav", c ? "open" : "collapsed");
			} catch {
				// not remembered
			}
			return !c;
		});
	};

	return (
		<div style={{ display: "flex", height: "100vh", overflow: "hidden" }}>
			<aside
				style={{
					width: collapsed ? 60 : 208,
					flex: "none",
					display: "flex",
					flexDirection: "column",
					borderRight: "1px solid var(--tf-border)",
					background: "var(--tf-bg)",
					transition: "width 0.15s",
				}}
			>
				<div
					style={{ padding: collapsed ? "16px 0 10px" : "16px 16px 10px", textAlign: collapsed ? "center" : "left" }}
				>
					<Wordmark collapsed={collapsed} />
				</div>
				{!collapsed && <ProjectScope />}
				<nav style={{ display: "flex", flexDirection: "column", gap: 2, padding: "8px 10px" }}>
					{nav.map((n) => (
						<NavItem key={n.to} {...n} collapsed={collapsed} />
					))}
				</nav>
				<div style={{ flex: 1 }} />
				<div
					style={{
						padding: "10px",
						borderTop: "1px solid var(--tf-border)",
						display: "flex",
						flexDirection: "column",
						gap: 4,
					}}
				>
					<LiveIndicator state={live} collapsed={collapsed} />
					<button type="button" onClick={toggle} style={navButton(collapsed)}>
						{collapsed ? <ChevronsRight size={16} /> : <ChevronsLeft size={16} />}
						{!collapsed && <span>Collapse</span>}
					</button>
				</div>
			</aside>
			<main style={{ flex: 1, minWidth: 0, display: "flex", flexDirection: "column", overflow: "hidden" }}>
				<Outlet />
			</main>
			<RunDrawer
				runId={runId}
				onClose={() =>
					setParams(
						(p) => {
							const n = new URLSearchParams(p);
							n.delete("run");
							return n;
						},
						{ replace: true },
					)
				}
			/>
		</div>
	);
}

function Wordmark({ collapsed }: { collapsed: boolean }) {
	return (
		<span
			style={{
				display: "inline-block",
				lineHeight: "22px",
				fontFamily: "var(--tf-brand)",
				fontWeight: 700,
				fontSize: 20,
				letterSpacing: -0.3,
				color: "var(--tf-wordmark)",
			}}
		>
			{collapsed ? "tf" : "thefactory"}
		</span>
	);
}

function ProjectScope() {
	const { projectId, setProjectId } = useScope();
	const { data: projects } = useProjects();
	const valid = projects?.some((p) => p.id === projectId) ? projectId : undefined;
	return (
		<div style={{ padding: "0 10px 6px" }}>
			<Select
				value={valid ?? "all"}
				onChange={(v) => setProjectId(v === "all" ? undefined : v)}
				style={{ width: "100%" }}
				options={[
					{ value: "all", label: "All projects" },
					...(projects ?? []).map((p) => ({ value: p.id, label: p.name })),
				]}
			/>
		</div>
	);
}

function NavItem({
	to,
	label,
	icon,
	collapsed,
	badge,
}: {
	to: string;
	label: string;
	icon: ReactNode;
	collapsed: boolean;
	badge?: "inbox" | "agents";
}) {
	const { projectId } = useScope();
	const { data: stats } = useStats(projectId);
	const { data: board } = useAgents();
	// Messages a person should look at (triage was unsure, or they were
	// imported without triage), or the agents at work.
	const count =
		badge === "inbox"
			? (stats?.feedback_by_status?.uncertain ?? 0) + (stats?.feedback_by_status?.inbox ?? 0)
			: badge === "agents"
				? workingAgents(board)
				: 0;
	const link = (
		<NavLink
			to={to}
			style={({ isActive }) => ({
				...navButton(collapsed),
				background: isActive ? "var(--tf-fill)" : undefined,
				color: isActive ? "var(--tf-text)" : "var(--tf-text2)",
				fontWeight: isActive ? 500 : 400,
				textDecoration: "none",
			})}
		>
			{icon}
			{!collapsed && <span style={{ flex: 1 }}>{label}</span>}
			{!collapsed && count > 0 && (
				<span
					style={{
						fontSize: 11,
						padding: "0 6px",
						borderRadius: 8,
						background: "var(--tf-fill)",
						color: "var(--tf-text2)",
						fontVariantNumeric: "tabular-nums",
					}}
				>
					{badge === "agents" && <StatusDot tone="accent" pulse size={6} />} {count}
				</span>
			)}
		</NavLink>
	);
	return collapsed ? (
		<Tooltip title={label} placement="right">
			{link}
		</Tooltip>
	) : (
		link
	);
}

function LiveIndicator({ state, collapsed }: { state: LiveState; collapsed: boolean }) {
	const tone = state === "live" ? "ok" : state === "connecting" ? "warn" : "error";
	const label = state === "live" ? "Live" : state === "connecting" ? "Connecting…" : "Offline";
	return (
		<Tooltip title={collapsed ? label : "Updates stream from tfy as they happen"} placement="right">
			<div style={{ ...navButton(collapsed), cursor: "default", color: "var(--tf-text2)", fontSize: 12 }}>
				<StatusDot tone={tone} />
				{!collapsed && label}
			</div>
		</Tooltip>
	);
}

function navButton(collapsed: boolean) {
	return {
		display: "flex",
		alignItems: "center",
		justifyContent: collapsed ? "center" : "flex-start",
		gap: 10,
		height: 34,
		padding: collapsed ? 0 : "0 10px",
		borderRadius: 6,
		border: 0,
		background: "none",
		color: "var(--tf-text2)",
		cursor: "pointer",
		fontSize: 13.5,
		fontFamily: "inherit",
		width: "100%",
	} as const;
}
