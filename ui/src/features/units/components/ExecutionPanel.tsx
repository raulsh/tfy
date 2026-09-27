import { Table, Tooltip } from "antd";
import type { ColumnsType } from "antd/es/table";
import { ExternalLink, GitBranch } from "lucide-react";
import type { UnitDetail, UnitRepo } from "@/shared/api/types";
import { EmptyState, Mono, TimeAgo } from "@/shared/components/misc";
import { Card } from "@/shared/components/OverlayDrawer";
import { StatusTag } from "@/shared/components/StatusTag";
import { humanize, shortSha } from "@/shared/lib/format";
import { checksTone, prTone, toneColor } from "@/shared/lib/status";
import { ReviewPanel } from "./ReviewPanel";

// The repositories a unit touches, and where each stands on GitHub.
export function ExecutionPanel({ unit }: { unit: UnitDetail }) {
	if (unit.repos.length === 0) {
		return (
			<Card>
				<EmptyState icon={<GitBranch size={28} />} title="No checkouts yet">
					Each project repository is checked out into the unit's workspace when planning starts.
				</EmptyState>
			</Card>
		);
	}
	const columns: ColumnsType<UnitRepo> = [
		{
			title: "Repository",
			dataIndex: "full_name",
			render: (v: string, r) => (
				<div>
					<div style={{ fontWeight: 500 }}>{v}</div>
					<div className="faint" style={{ fontSize: 12 }}>
						{r.is_target ? "changes here" : "context only"}
					</div>
				</div>
			),
		},
		{
			title: "Branch",
			dataIndex: "branch",
			render: (v: string, r) =>
				v ? (
					<Tooltip title={`from ${r.default_branch} @ ${shortSha(r.base_sha)}`}>
						<Mono>{v}</Mono>
					</Tooltip>
				) : (
					<Mono faint>
						{r.default_branch} @ {shortSha(r.base_sha)}
					</Mono>
				),
		},
		{
			title: "Published",
			dataIndex: "publish_state",
			width: 120,
			render: (v: string, r) =>
				r.is_target ? <span className="muted">{humanize(v)}</span> : <span className="faint">—</span>,
		},
		{
			title: "Pull request",
			dataIndex: "pr_number",
			width: 200,
			render: (n: number, r) =>
				n ? (
					<span style={{ display: "inline-flex", alignItems: "center", gap: 8 }}>
						<a
							href={r.pr_url}
							target="_blank"
							rel="noreferrer"
							style={{ display: "inline-flex", alignItems: "center", gap: 4 }}
						>
							#{n} <ExternalLink size={12} />
						</a>
						<StatusTag tone={prTone(r.pr_state)} label={r.pr_state || "open"} />
					</span>
				) : (
					<span className="faint">—</span>
				),
		},
		{
			title: "Checks",
			dataIndex: "checks_state",
			width: 110,
			render: (v: string) => (v ? <StatusTag tone={checksTone(v)} label={v} /> : <span className="faint">—</span>),
		},
		{
			title: "Head",
			dataIndex: "head_sha",
			width: 100,
			render: (v: string, r) =>
				r.merged_at ? (
					<span className="muted">
						merged <TimeAgo at={r.merged_at} />
					</span>
				) : (
					<Mono faint>{shortSha(v)}</Mono>
				),
		},
	];
	return (
		<>
			<ReviewPanel unit={unit} />
			<Card title="Repositories" padded={false}>
				<Table<UnitRepo>
					className="tf-table"
					size="small"
					rowKey="repo_id"
					columns={columns}
					dataSource={unit.repos}
					pagination={false}
					onRow={(r) => ({
						style: {
							"--edge": r.pr_state === "merged" ? toneColor.ok : r.pr_number ? toneColor.accent : "transparent",
							cursor: "default",
						} as React.CSSProperties,
					})}
				/>
			</Card>
		</>
	);
}
