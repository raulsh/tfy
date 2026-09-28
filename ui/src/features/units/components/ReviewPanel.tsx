import { Select, Space, Table } from "antd";
import type { ColumnsType } from "antd/es/table";
import { useState } from "react";
import type { UnitDetail } from "@/shared/api/types";
import { Mono } from "@/shared/components/misc";
import { Card } from "@/shared/components/OverlayDrawer";
import { StatusTag } from "@/shared/components/StatusTag";
import { timeAgo } from "@/shared/lib/format";
import type { Tone } from "@/shared/lib/status";
import { toneColor } from "@/shared/lib/status";
import { useDocument } from "../hooks";

interface CriterionVerdict {
	id: string;
	status: "met" | "partial" | "unmet" | "not_verifiable";
	evidence: string;
}

interface Finding {
	severity: "blocker" | "major" | "minor" | "nit";
	repo: string;
	file: string;
	line?: number;
	message: string;
}

interface ReviewMeta {
	decision: "approve" | "request_changes";
	summary: string;
	round: number;
	criteria: CriterionVerdict[];
	findings: Finding[];
	spec_criteria?: { id: string; text: string }[];
	reviewed?: Record<string, string>;
	// The person who asked for changes; absent for the review run's reviews.
	by?: string;
}

const criterionTone: Record<string, Tone> = { met: "ok", partial: "warn", unmet: "error", not_verifiable: "neutral" };
const severityTone: Record<string, Tone> = { blocker: "error", major: "error", minor: "warn", nit: "neutral" };

// The reviewer's verdict: the acceptance-criteria matrix and the findings.
export function ReviewPanel({ unit }: { unit: UnitDetail }) {
	const [version, setVersion] = useState<number>();
	const has = !!unit.documents.review;
	const { data } = useDocument(has ? unit.id : undefined, "review", version);
	if (!has || !data) return null;
	const meta = data.document.meta as unknown as ReviewMeta;
	const texts = new Map((meta.spec_criteria ?? []).map((c) => [c.id, c.text]));
	const approved = meta.decision === "approve";

	const columns: ColumnsType<CriterionVerdict> = [
		{ title: "", dataIndex: "id", width: 64, render: (v: string) => <Mono faint>{v}</Mono> },
		{
			title: "Criterion",
			dataIndex: "id",
			key: "text",
			render: (v: string) => texts.get(v) ?? <span className="faint">—</span>,
		},
		{
			title: "Status",
			dataIndex: "status",
			width: 140,
			render: (v: string) => <StatusTag tone={criterionTone[v] ?? "neutral"} label={v.replace("_", " ")} />,
		},
		{ title: "Evidence", dataIndex: "evidence", render: (v: string) => <span className="muted">{v}</span> },
	];

	return (
		<Card
			title={
				<Space size={10}>
					<span>Review</span>
					<StatusTag tone={approved ? "ok" : "warn"} label={approved ? "Approved" : "Changes requested"} />
					<span className="faint" style={{ fontWeight: 400, fontSize: 12 }}>
						round {meta.round}
						{meta.by && ` · requested by ${meta.by}`}
					</span>
				</Space>
			}
			extra={
				data.versions.length > 1 && (
					<Select
						size="small"
						value={data.document.version}
						onChange={(v) => setVersion(v === unit.documents.review?.version ? undefined : v)}
						popupMatchSelectWidth={false}
						options={data.versions.map((v) => ({
							value: v.version,
							label: `round ${v.version} · ${timeAgo(v.created_at)}`,
						}))}
					/>
				)
			}
			padded={false}
		>
			<div style={{ padding: "12px 16px" }}>{meta.summary}</div>
			{meta.criteria?.length > 0 && (
				<Table<CriterionVerdict>
					className="tf-table"
					size="small"
					rowKey="id"
					columns={columns}
					dataSource={meta.criteria}
					pagination={false}
					onRow={(c) => ({
						style: {
							"--edge": toneColor[criterionTone[c.status] ?? "neutral"],
							cursor: "default",
						} as React.CSSProperties,
					})}
				/>
			)}
			{meta.findings?.length > 0 && (
				<div style={{ borderTop: "1px solid var(--tf-border)" }}>
					<div style={{ padding: "10px 16px 4px", fontWeight: 500 }}>Findings</div>
					{meta.findings.map((f, i) => (
						<div
							key={i}
							style={{
								display: "grid",
								gridTemplateColumns: "84px minmax(0, 260px) 1fr",
								gap: 10,
								padding: "8px 16px",
								borderTop: i ? "1px solid var(--tf-border)" : undefined,
								alignItems: "baseline",
							}}
						>
							<StatusTag tone={severityTone[f.severity] ?? "neutral"} label={f.severity} />
							<Mono faint>
								{f.repo}
								{f.file ? `/${f.file}` : ""}
								{f.line ? `:${f.line}` : ""}
							</Mono>
							<span>{f.message}</span>
						</div>
					))}
				</div>
			)}
		</Card>
	);
}
