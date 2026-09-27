import { Button, Spin } from "antd";
import { ArrowUpRight } from "lucide-react";
import { useNavigate } from "react-router-dom";
import { RunsTable } from "@/features/runs/components/RunsTable";
import { Chip } from "@/shared/components/Chip";
import { KVTable, Mono, TimeAgo } from "@/shared/components/misc";
import { Card, OverlayDrawer } from "@/shared/components/OverlayDrawer";
import { kindLabel, originLabel, toneColor, unitTone } from "@/shared/lib/status";
import { useUnit } from "../hooks";
import { ActivityList } from "./ActivityList";
import { AttentionBanner, StageStepper, UnitActions, UnitStateTag } from "./parts";

// A unit at a glance, over the units list.
export function UnitQuickView({
	unitId,
	onClose,
	onPrev,
	onNext,
}: {
	unitId?: string;
	onClose: () => void;
	onPrev?: () => void;
	onNext?: () => void;
}) {
	const { data: unit } = useUnit(unitId);
	const navigate = useNavigate();
	return (
		<OverlayDrawer
			open={!!unitId}
			onClose={onClose}
			onPrev={onPrev}
			onNext={onNext}
			width="min(980px, 64vw)"
			stripe={unit ? toneColor[unitTone(unit)] : undefined}
			title={
				unit ? (
					<span>
						<Mono faint>{unit.label}</Mono> {unit.title}
					</span>
				) : (
					"…"
				)
			}
			chips={
				unit && (
					<>
						<UnitStateTag unit={unit} />
						<Chip k="project" v={unit.project_name ?? ""} />
						<Chip k="kind" v={kindLabel[unit.kind] ?? unit.kind} />
						<Chip k="origin" v={originLabel[unit.origin] ?? unit.origin} />
					</>
				)
			}
			actions={
				unit && (
					<Button size="small" icon={<ArrowUpRight size={14} />} onClick={() => navigate(`/units/${unit.id}`)}>
						Open
					</Button>
				)
			}
		>
			{!unit ? (
				<Spin style={{ display: "block", margin: 48 }} />
			) : (
				<>
					<div
						style={{
							display: "flex",
							justifyContent: "space-between",
							alignItems: "center",
							gap: 12,
							marginBottom: 14,
							flexWrap: "wrap",
						}}
					>
						<StageStepper unit={unit} />
						<UnitActions unit={unit} size="small" />
					</div>
					<div style={{ marginBottom: 16 }}>
						<AttentionBanner unit={unit} />
					</div>
					{(unit.summary || unit.description) && <Card title="Summary">{unit.summary || unit.description}</Card>}
					<Card padded={false}>
						<KVTable
							rows={[
								["Created", <TimeAgo key="c" at={unit.created_at} />],
								["Updated", <TimeAgo key="u" at={unit.updated_at} />],
								[
									"Pull requests",
									unit.repos.filter((r) => r.pr_number).length ? (
										<span key="p" style={{ display: "flex", gap: 10, flexWrap: "wrap" }}>
											{unit.repos
												.filter((r) => r.pr_number)
												.map((r) => (
													<a key={r.repo_id} href={r.pr_url} target="_blank" rel="noreferrer">
														{r.full_name}#{r.pr_number} ({r.pr_state})
													</a>
												))}
										</span>
									) : (
										<span key="p" className="faint">
											none yet
										</span>
									),
								],
								[
									"Workspace",
									<Mono key="w" faint>
										{unit.workspace_path}
									</Mono>,
								],
							]}
						/>
					</Card>
					<Card title="Recent runs" padded={false}>
						<RunsTable runs={unit.runs.slice(0, 5)} showUnit={false} />
					</Card>
					<Card title="Activity">
						<ActivityList items={unit.activity} limit={10} />
					</Card>
				</>
			)}
		</OverlayDrawer>
	);
}
