import { Select } from "antd";
import { Activity } from "lucide-react";
import { useCallback, useMemo } from "react";
import { ranges, useScope } from "@/app/scope";
import type { Run } from "@/shared/api/types";
import { FacetPanel } from "@/shared/components/FacetPanel";
import { EmptyState, Metric, PageHeader } from "@/shared/components/misc";
import { QueryBar } from "@/shared/components/QueryBar";
import { type FacetDef, useFacetedList } from "@/shared/lib/facets";
import { duration, usd } from "@/shared/lib/format";
import { runStatusLabel, runTone, toneColor } from "@/shared/lib/status";
import { RunsTable } from "../components/RunsTable";
import { useRuns } from "../hooks";

const defs: FacetDef<Run>[] = [
	{ key: "status", label: "Status", get: (r) => r.status, format: runStatusLabel, color: (v) => toneColor[runTone(v)] },
	{
		key: "kind",
		label: "Kind",
		get: (r) => r.kind,
		order: ["triage", "define", "plan", "develop", "review", "release"],
	},
	{ key: "model", label: "Model", get: (r) => r.model },
	{ key: "unit", label: "Unit", get: (r) => r.unit_label },
];

export default function RunsPage() {
	const { projectId, range, setRange, inRange } = useScope();
	const { data, isLoading } = useRuns({ projectId });
	const items = useMemo(() => (data ?? []).filter((r) => inRange(r.created_at)), [data, inRange]);
	const text = useCallback((r: Run) => `${r.id} ${r.kind} ${r.unit_label ?? ""} ${r.unit_title ?? ""} ${r.reason}`, []);
	const f = useFacetedList(items, defs, text);

	const totals = useMemo(() => {
		const cost = f.filtered.reduce((s, r) => s + r.cost_usd, 0);
		const done = f.filtered.filter((r) => r.duration_ms > 0);
		const avg = done.length ? done.reduce((s, r) => s + r.duration_ms, 0) / done.length : 0;
		const failed = f.filtered.filter((r) => ["failed", "aborted"].includes(r.status)).length;
		return { cost, avg, failed, live: f.filtered.filter((r) => r.status === "running").length };
	}, [f.filtered]);

	return (
		<div style={{ display: "flex", flexDirection: "column", height: "100%" }}>
			<PageHeader
				title="Runs"
				extra={<Select value={range} onChange={setRange} options={ranges} style={{ width: 150 }} />}
			>
				<div style={{ display: "flex", gap: 10, marginTop: 12, flexWrap: "wrap" }}>
					<Metric label="Runs" value={f.filtered.length} />
					<Metric label="Running" value={totals.live} tone={totals.live ? toneColor.accent : undefined} />
					<Metric label="Failed" value={totals.failed} tone={totals.failed ? toneColor.error : undefined} />
					<Metric label="Spend" value={usd(totals.cost)} hint="estimated on a subscription" />
					<Metric label="Avg. duration" value={duration(Math.round(totals.avg))} />
				</div>
			</PageHeader>
			<QueryBar groups={f.groups} search={f.search} onSearch={f.setSearch} onRemove={f.toggle} />
			<div style={{ display: "flex", flex: 1, minHeight: 0 }}>
				<FacetPanel groups={f.groups} onToggle={f.toggle} onOnly={f.only} onClear={f.clear} total={f.filtered.length} />
				<div style={{ flex: 1, overflow: "auto" }}>
					{!isLoading && items.length === 0 ? (
						<EmptyState icon={<Activity size={28} />} title="No runs yet">
							Every Claude session tfy starts shows up here, live.
						</EmptyState>
					) : (
						<RunsTable runs={f.filtered} loading={isLoading} />
					)}
				</div>
			</div>
		</div>
	);
}
