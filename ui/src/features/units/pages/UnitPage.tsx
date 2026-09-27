import { Spin, Tabs } from "antd";
import { Activity as ActivityIcon, FileCode2, FileText, GitPullRequest, LayoutList, Rocket } from "lucide-react";
import type { ReactNode } from "react";
import { Link, useParams, useSearchParams } from "react-router-dom";
import { RunsTable } from "@/features/runs/components/RunsTable";
import { Chip } from "@/shared/components/Chip";
import { EmptyState, KVTable, Markdown, Mono, TimeAgo } from "@/shared/components/misc";
import { Card } from "@/shared/components/OverlayDrawer";
import { usd } from "@/shared/lib/format";
import { kindLabel, originLabel, stageLabel, toneColor, unitTone } from "@/shared/lib/status";
import { ActivityList } from "../components/ActivityList";
import { DocumentPanel } from "../components/DocumentPanel";
import { ExecutionPanel } from "../components/ExecutionPanel";
import { IssuesCard } from "../components/IssuesCard";
import { AttentionBanner, StageStepper, UnitActions, UnitStateTag } from "../components/parts";
import { ReleasePanel } from "../components/ReleasePanel";
import { RetrospectiveCard } from "../components/RetrospectiveCard";
import { useUnit } from "../hooks";

function tabLabel(icon: ReactNode, text: string, badge?: ReactNode) {
	return (
		<span style={{ display: "inline-flex", alignItems: "center", gap: 6 }}>
			{icon}
			{text}
			{badge}
		</span>
	);
}

function defaultTab(state: string): string {
	switch (state) {
		case "defining":
		case "definition_review":
			return "requirement";
		case "planning":
		case "spec_review":
			return "spec";
		case "developing":
		case "publishing":
		case "reviewing":
		case "awaiting_merge":
		case "merging":
			return "execution";
		case "releasing":
			return "release";
		default:
			return "overview";
	}
}

export default function UnitPage() {
	const { id } = useParams();
	const { data: unit, isLoading, error } = useUnit(id);
	const [params, setParams] = useSearchParams();

	if (isLoading) return <Spin style={{ display: "block", margin: 64 }} />;
	if (error || !unit)
		return (
			<EmptyState title="Unit not found">
				<Link to="/units">Back to units</Link>
			</EmptyState>
		);

	const tab = params.get("tab") ?? defaultTab(unit.state);
	const spend = unit.runs.reduce((s, r) => s + r.cost_usd, 0);
	const color = toneColor[unitTone(unit)];

	return (
		<div style={{ display: "flex", flexDirection: "column", height: "100%", overflow: "hidden" }}>
			<div style={{ height: 3, background: color, flex: "none" }} />
			<div style={{ padding: "12px 20px 0", borderBottom: "1px solid var(--tf-border)", flex: "none" }}>
				<div style={{ fontSize: 12, marginBottom: 6 }}>
					<Link to="/units" className="muted">
						Units
					</Link>
					<span className="faint"> / </span>
					<Mono faint>{unit.label}</Mono>
				</div>
				<div style={{ display: "flex", alignItems: "flex-start", gap: 16, flexWrap: "wrap" }}>
					<h1 style={{ margin: 0, fontSize: 20, fontWeight: 500, flex: 1, minWidth: 280, lineHeight: 1.35 }}>
						{unit.title}
					</h1>
					<UnitActions unit={unit} />
				</div>
				<div style={{ display: "flex", gap: 6, flexWrap: "wrap", margin: "10px 0 12px" }}>
					<UnitStateTag unit={unit} />
					<Chip k="project" v={unit.project_name ?? ""} />
					<Chip k="kind" v={kindLabel[unit.kind] ?? unit.kind} />
					<Chip k="origin" v={originLabel[unit.origin] ?? unit.origin} />
					{unit.issues.map((is) => (
						<Chip
							key={is.id}
							k="issue"
							title={is.title}
							v={
								<a href={is.url} target="_blank" rel="noreferrer">
									{is.ref}
								</a>
							}
						/>
					))}
					<Chip k="spend" v={usd(spend)} mono />
				</div>
				<div style={{ marginBottom: 12 }}>
					<StageStepper unit={unit} />
				</div>
				<Tabs
					activeKey={tab}
					onChange={(k) =>
						setParams(
							(p) => {
								const n = new URLSearchParams(p);
								n.set("tab", k);
								return n;
							},
							{ replace: true },
						)
					}
					style={{ marginBottom: -1 }}
					items={[
						{ key: "overview", label: tabLabel(<LayoutList size={15} />, "Overview") },
						{
							key: "requirement",
							label: tabLabel(<FileText size={15} />, "Requirement", docBadge(unit.documents.requirement?.version)),
						},
						{ key: "spec", label: tabLabel(<FileCode2 size={15} />, "Spec", docBadge(unit.documents.spec?.version)) },
						{
							key: "execution",
							label: tabLabel(
								<GitPullRequest size={15} />,
								"Execution",
								docBadge(unit.repos.filter((r) => r.pr_number).length || undefined, "PR"),
							),
						},
						{ key: "release", label: tabLabel(<Rocket size={15} />, "Release") },
						{
							key: "runs",
							label: tabLabel(<ActivityIcon size={15} />, "Runs", docBadge(unit.runs.length || undefined, "")),
						},
					]}
				/>
			</div>
			<div style={{ flex: 1, overflow: "auto", background: "var(--tf-surface)", padding: 20 }}>
				<div style={{ marginBottom: 16 }}>
					<AttentionBanner unit={unit} />
				</div>
				{tab === "overview" && (
					<div style={{ display: "grid", gridTemplateColumns: "minmax(0, 1fr) 380px", gap: 16, alignItems: "start" }}>
						<div>
							{unit.summary && <Card title="Summary">{unit.summary}</Card>}
							{unit.description && (
								<Card title="As asked">
									<Markdown>{unit.description}</Markdown>
								</Card>
							)}
							{unit.feedback.length > 0 && (
								<Card title={`From Slack (${unit.feedback.length})`} padded={false}>
									{unit.feedback.map((f, i) => (
										<div
											key={f.id}
											style={{ padding: "9px 16px", borderTop: i ? "1px solid var(--tf-border)" : undefined }}
										>
											<div style={{ fontSize: 12, marginBottom: 2 }}>
												<b>{f.author}</b>{" "}
												<span className="faint">
													#{f.channel_name} · <TimeAgo at={f.posted_at} />
												</span>
												{f.permalink && (
													<a
														href={f.permalink}
														target="_blank"
														rel="noreferrer"
														style={{ marginLeft: 8, fontSize: 12 }}
													>
														open in Slack
													</a>
												)}
											</div>
											<div style={{ whiteSpace: "pre-wrap" }}>{f.text}</div>
										</div>
									))}
								</Card>
							)}
							<IssuesCard unit={unit} />
							<RetrospectiveCard unit={unit} />
							<Card title="Activity">
								<ActivityList items={unit.activity} />
							</Card>
						</div>
						<Card title="Details" padded={false}>
							<KVTable
								rows={[
									["Stage", stageLabel(unit.stage)],
									["Created", <TimeAgo key="c" at={unit.created_at} />],
									["Updated", <TimeAgo key="u" at={unit.updated_at} />],
									["Created by", unit.created_by || "you"],
									...(unit.parent_unit_id
										? [
												[
													unit.origin === "retrospective" ? "Proposed by" : "Follows",
													<Link key="p" to={`/units/${unit.parent_unit_id}`}>
														the earlier unit
													</Link>,
												] as [string, ReactNode],
											]
										: []),
									["Review rounds", unit.review_iteration],
									["Runs", `${unit.runs.length} (${usd(spend)})`],
									[
										"Workspace",
										<Mono key="w" faint>
											{unit.workspace_path}
										</Mono>,
									],
								]}
							/>
						</Card>
					</div>
				)}
				{tab === "requirement" && <DocumentPanel unit={unit} kind="requirement" />}
				{tab === "spec" && <DocumentPanel unit={unit} kind="spec" />}
				{tab === "execution" && <ExecutionPanel unit={unit} />}
				{tab === "release" && <ReleasePanel unit={unit} />}
				{tab === "runs" && (
					<Card title="Runs" padded={false}>
						{unit.runs.length ? <RunsTable runs={unit.runs} showUnit={false} /> : <EmptyState title="No runs yet" />}
					</Card>
				)}
			</div>
		</div>
	);
}

function docBadge(n: number | undefined, prefix = "v") {
	if (!n) return null;
	return (
		<span
			style={{
				fontSize: 11,
				padding: "0 6px",
				borderRadius: 8,
				background: "var(--tf-fill)",
				color: "var(--tf-text2)",
				fontFamily: "var(--tf-mono)",
			}}
		>
			{prefix}
			{n}
		</span>
	);
}
