import { Button, Select } from "antd";
import { Boxes, Plus } from "lucide-react";
import { useCallback, useMemo, useState } from "react";
import { useSearchParams } from "react-router-dom";
import { ranges, useScope } from "@/app/scope";
import { status } from "@/app/theme/tokens";
import { useProjects } from "@/features/projects/hooks";
import { useStats } from "@/shared/api/system";
import type { Unit } from "@/shared/api/types";
import { FacetPanel } from "@/shared/components/FacetPanel";
import { EmptyState, Metric, PageHeader } from "@/shared/components/misc";
import { QueryBar } from "@/shared/components/QueryBar";
import { type FacetDef, useFacetedList } from "@/shared/lib/facets";
import { usd } from "@/shared/lib/format";
import {
	attentionLabel,
	kindLabel,
	originLabel,
	stageLabel,
	stages,
	stateLabel,
	toneColor,
	unitTone,
} from "@/shared/lib/status";
import { NewUnitModal } from "../components/NewUnitModal";
import { StageFunnel, stageColor, UnitsTable } from "../components/parts";
import { UnitQuickView } from "../components/UnitQuickView";
import { useUnits } from "../hooks";

const defs: FacetDef<Unit>[] = [
	{
		key: "stage",
		label: "Stage",
		get: (u) => u.stage,
		format: stageLabel,
		order: stages.map((s) => s.key),
		color: stageColor,
	},
	{
		key: "state",
		label: "State",
		get: (u) => u.state,
		format: stateLabel,
		color: (v) => toneColor[unitTone({ state: v as Unit["state"], attention: "" })],
	},
	{
		key: "attention",
		label: "Attention",
		get: (u) => u.attention || undefined,
		format: (v) => attentionLabel[v] ?? v,
		color: () => status.error,
	},
	{ key: "kind", label: "Kind", get: (u) => u.kind, format: (v) => kindLabel[v] ?? v },
	{ key: "origin", label: "Origin", get: (u) => u.origin, format: (v) => originLabel[v] ?? v },
	{ key: "project", label: "Project", get: (u) => u.project_name },
];

export default function UnitsPage() {
	const { projectId, range, setRange, inRange } = useScope();
	const { data, isLoading } = useUnits(projectId);
	const { data: stats } = useStats(projectId);
	const { data: projects } = useProjects();
	const [params, setParams] = useSearchParams();
	const [creating, setCreating] = useState(false);

	const items = useMemo(() => (data ?? []).filter((u) => inRange(u.updated_at)), [data, inRange]);
	const text = useCallback((u: Unit) => `${u.label} ${u.title} ${u.summary}`, []);
	const f = useFacetedList(items, defs, text);

	const stageCounts = useMemo(() => {
		const c: Partial<Record<string, number>> = {};
		for (const u of items) c[u.stage] = (c[u.stage] ?? 0) + 1;
		return c;
	}, [items]);

	const selectedId = params.get("unit");
	const index = f.filtered.findIndex((u) => u.id === selectedId);
	const select = (id: string | undefined) =>
		setParams(
			(p) => {
				const n = new URLSearchParams(p);
				if (id) n.set("unit", id);
				else n.delete("unit");
				return n;
			},
			{ replace: true },
		);

	const noProjects = projects && projects.length === 0;

	return (
		<div style={{ display: "flex", flexDirection: "column", height: "100%" }}>
			<PageHeader
				title="Units"
				extra={
					<>
						<Select value={range} onChange={setRange} options={ranges} style={{ width: 150 }} />
						<Button type="primary" icon={<Plus size={15} />} onClick={() => setCreating(true)} disabled={noProjects}>
							New unit
						</Button>
					</>
				}
			>
				<div style={{ display: "flex", gap: 10, marginTop: 12, alignItems: "stretch", flexWrap: "wrap" }}>
					<StageFunnel counts={stageCounts} selected={f.selection.stage ?? []} onSelect={(s) => f.toggle("stage", s)} />
					<Metric
						label="Needs you"
						value={stats?.needs_attention ?? "—"}
						tone={stats?.needs_attention ? status.error : undefined}
					/>
					<Metric
						label="Live runs"
						value={stats?.live_runs ?? "—"}
						tone={stats?.live_runs ? status.accent : undefined}
					/>
					<Metric label="Spend today" value={usd(stats?.cost_today_usd)} hint={`7 days ${usd(stats?.cost_7d_usd)}`} />
				</div>
			</PageHeader>
			<QueryBar groups={f.groups} search={f.search} onSearch={f.setSearch} onRemove={f.toggle} />
			<div style={{ display: "flex", flex: 1, minHeight: 0 }}>
				<FacetPanel groups={f.groups} onToggle={f.toggle} onOnly={f.only} onClear={f.clear} total={f.filtered.length} />
				<div style={{ flex: 1, overflow: "auto" }}>
					{noProjects ? (
						<EmptyState icon={<Boxes size={28} />} title="Start with a project">
							A project links the GitHub repositories tfy works on. Create one under <a href="/projects">Projects</a>,
							then add units to it.
						</EmptyState>
					) : !isLoading && items.length === 0 ? (
						<EmptyState icon={<Boxes size={28} />} title="No units yet">
							A unit is one feature or fix, taken from idea to merged pull request.{" "}
							<Button type="link" style={{ padding: 0 }} onClick={() => setCreating(true)}>
								Create the first one
							</Button>
							.
						</EmptyState>
					) : (
						<UnitsTable units={f.filtered} loading={isLoading} activeId={selectedId} onOpen={(u) => select(u.id)} />
					)}
				</div>
			</div>
			<UnitQuickView
				unitId={selectedId ?? undefined}
				onClose={() => select(undefined)}
				onPrev={index > 0 ? () => select(f.filtered[index - 1].id) : undefined}
				onNext={index >= 0 && index < f.filtered.length - 1 ? () => select(f.filtered[index + 1].id) : undefined}
			/>
			<NewUnitModal open={creating} onClose={() => setCreating(false)} />
		</div>
	);
}
