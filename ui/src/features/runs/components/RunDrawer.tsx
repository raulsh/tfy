import { App, Button, Popconfirm, Spin } from "antd";
import { Square } from "lucide-react";
import { Link } from "react-router-dom";
import { Chip } from "@/shared/components/Chip";
import { JsonView, Metric, Mono } from "@/shared/components/misc";
import { Card, OverlayDrawer } from "@/shared/components/OverlayDrawer";
import { StatusTag } from "@/shared/components/StatusTag";
import { useRunStream } from "@/shared/hooks/useRunStream";
import { compact, duration, shortId, usd } from "@/shared/lib/format";
import { isRunLive, runStatusLabel, runTone, toneColor } from "@/shared/lib/status";
import { useCancelRun, useRun } from "../hooks";
import { EventTimeline } from "./EventTimeline";

// One Claude run: its metrics, its structured result, and its events as a
// live trace.
export function RunDrawer({ runId, onClose }: { runId?: string; onClose: () => void }) {
	const { data: run, isLoading } = useRun(runId);
	const stream = useRunStream(runId);
	const cancel = useCancelRun();
	const { message } = App.useApp();
	const status = stream.status ?? run?.status ?? "queued";
	const live = isRunLive(status);
	const tone = runTone(status);

	return (
		<OverlayDrawer
			open={!!runId}
			onClose={onClose}
			stripe={toneColor[tone]}
			title={
				run ? (
					<span style={{ display: "flex", alignItems: "center", gap: 10, flexWrap: "wrap" }}>
						<span>{run.kind} run</span>
						<Mono faint>{shortId(run.id)}</Mono>
						<StatusTag tone={tone} label={runStatusLabel(status)} pulse={live} />
					</span>
				) : (
					"Run"
				)
			}
			chips={
				run && (
					<>
						{run.unit_label && (
							<Link to={`/units/${run.unit_id}`} onClick={onClose}>
								<Chip k="unit" v={`${run.unit_label} ${run.unit_title ?? ""}`} />
							</Link>
						)}
						<Chip k="model" v={run.model || "—"} mono />
						<Chip k="effort" v={run.effort || "—"} mono />
						<Chip k="mode" v={run.permission_mode || "—"} mono />
						{run.session_id && <Chip k="session" v={run.session_id.slice(0, 8)} mono title={run.session_id} />}
						{run.parent_run_id && <Chip k="resumed from" v={shortId(run.parent_run_id)} mono />}
						{run.prompt_version && <Chip k="prompt" v={run.prompt_version} mono />}
					</>
				)
			}
			actions={
				live && (
					<Popconfirm
						title="Cancel this run?"
						description="Claude is interrupted; the unit waits for you to retry."
						okText="Cancel run"
						okButtonProps={{ danger: true }}
						onConfirm={() =>
							runId &&
							cancel.mutate(runId, {
								onSuccess: () => message.info("Cancelling…"),
								onError: (e) => message.error(e.message),
							})
						}
					>
						<Button size="small" danger icon={<Square size={13} />} loading={cancel.isPending}>
							Cancel
						</Button>
					</Popconfirm>
				)
			}
		>
			{isLoading || !run ? (
				<Spin style={{ display: "block", margin: 48 }} />
			) : (
				<>
					<div style={{ display: "flex", gap: 10, flexWrap: "wrap", marginBottom: 16 }}>
						<Metric label="Duration" value={duration(run.duration_ms)} />
						<Metric
							label="Cost"
							value={usd(run.cost_usd)}
							hint={run.parent_run_id ? `session total ${usd(run.cost_total_usd)}` : undefined}
						/>
						<Metric label="Turns" value={run.turns || "—"} />
						<Metric
							label="Tokens"
							value={run.input_tokens ? `${compact(run.input_tokens)} / ${compact(run.output_tokens)}` : "—"}
							hint="in / out"
						/>
						<Metric label="Denials" value={run.denials} tone={run.denials ? toneColor.warn : undefined} />
					</div>
					{run.reason && !live && run.status !== "succeeded" && (
						<Card title="Why it stopped">
							<div style={{ whiteSpace: "pre-wrap" }}>{run.reason}</div>
						</Card>
					)}
					<Card title={live ? "Events (live)" : "Events"} padded={false}>
						<EventTimeline events={stream.events} live={live} />
					</Card>
					{run.result != null && !live && (
						<Card title="Result">
							<JsonView value={run.result} />
						</Card>
					)}
				</>
			)}
		</OverlayDrawer>
	);
}
