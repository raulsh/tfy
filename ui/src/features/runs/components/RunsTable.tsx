import { Table } from "antd";
import type { ColumnsType } from "antd/es/table";
import type { Run } from "@/shared/api/types";
import { Mono, TimeAgo } from "@/shared/components/misc";
import { StatusTag } from "@/shared/components/StatusTag";
import { useOpenRun } from "@/shared/hooks/useOpenRun";
import { duration, shortId, usd } from "@/shared/lib/format";
import { isRunLive, runStatusLabel, runTone, toneColor } from "@/shared/lib/status";

// Runs as traces: one dense row per Claude session.
export function RunsTable({ runs, loading, showUnit = true }: { runs: Run[]; loading?: boolean; showUnit?: boolean }) {
	const openRun = useOpenRun();
	const columns: ColumnsType<Run> = [
		{ title: "Run", dataIndex: "id", width: 96, render: (v: string) => <Mono faint>{shortId(v)}</Mono> },
		{
			title: "Kind",
			dataIndex: "kind",
			width: 90,
			render: (v: string) => <span style={{ fontWeight: 500 }}>{v}</span>,
		},
		...(showUnit
			? [
					{
						title: "Unit",
						dataIndex: "unit_label",
						render: (_: string, r: Run) =>
							r.unit_label ? (
								<span style={{ display: "flex", gap: 8, minWidth: 0 }}>
									<Mono faint>{r.unit_label}</Mono>
									<span style={{ overflow: "hidden", textOverflow: "ellipsis", whiteSpace: "nowrap" }}>
										{r.unit_title}
									</span>
								</span>
							) : (
								<span className="faint">—</span>
							),
					},
				]
			: []),
		{
			title: "Status",
			dataIndex: "status",
			width: 140,
			render: (v: string) => <StatusTag tone={runTone(v)} label={runStatusLabel(v)} pulse={isRunLive(v)} />,
		},
		{ title: "Model", dataIndex: "model", width: 90, render: (v: string) => <span className="muted">{v}</span> },
		{
			title: "Started",
			dataIndex: "started_at",
			width: 120,
			render: (v: string | null, r) => <TimeAgo at={v ?? r.created_at} />,
		},
		{
			title: "Duration",
			dataIndex: "duration_ms",
			width: 90,
			align: "right",
			render: (v: number) => <Mono>{duration(v)}</Mono>,
		},
		{ title: "Cost", dataIndex: "cost_usd", width: 80, align: "right", render: (v: number) => <Mono>{usd(v)}</Mono> },
		{
			title: "Turns",
			dataIndex: "turns",
			width: 64,
			align: "right",
			render: (v: number) => <Mono faint>{v || "—"}</Mono>,
		},
	];
	return (
		<Table<Run>
			className="tf-table"
			size="small"
			rowKey="id"
			columns={columns}
			dataSource={runs}
			loading={loading}
			pagination={false}
			tableLayout="fixed"
			onRow={(r) => ({
				onClick: () => openRun(r.id),
				style: { "--edge": toneColor[runTone(r.status)] } as React.CSSProperties,
			})}
		/>
	);
}
