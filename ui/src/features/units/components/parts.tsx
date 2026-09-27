import { App, Button, Checkbox, Input, Modal, Popconfirm, Space, Table, Tooltip } from "antd";
import type { ColumnsType } from "antd/es/table";
import { Check, CircleAlert, GitMerge, RotateCcw, Square, Undo2 } from "lucide-react";
import { useState } from "react";
import type { Stage, Unit, UnitAction, UnitDetail } from "@/shared/api/types";
import { Mono, TimeAgo } from "@/shared/components/misc";
import { StatusTag } from "@/shared/components/StatusTag";
import {
	attentionLabel,
	isWaitingOnHuman,
	kindLabel,
	stageLabel,
	stages,
	stateLabel,
	toneColor,
	unitTone,
} from "@/shared/lib/status";
import { useUnitAction } from "../hooks";

export function UnitStateTag({ unit }: { unit: Pick<Unit, "state" | "attention" | "busy"> }) {
	const tone = unitTone(unit);
	return <StatusTag tone={tone} label={stateLabel(unit.state)} pulse={unit.busy} />;
}

export function AttentionTag({ attention }: { attention: string }) {
	if (!attention) return null;
	return (
		<span
			style={{
				display: "inline-flex",
				alignItems: "center",
				gap: 4,
				color: "var(--tf-error)",
				fontSize: 12,
				fontWeight: 500,
			}}
		>
			<CircleAlert size={13} />
			{attentionLabel[attention] ?? attention}
		</span>
	);
}

// The units table: dense rows with a status-colored left edge.
export function UnitsTable({
	units,
	activeId,
	onOpen,
	loading,
}: {
	units: Unit[];
	activeId?: string | null;
	onOpen: (u: Unit) => void;
	loading?: boolean;
}) {
	const columns: ColumnsType<Unit> = [
		{ title: "ID", dataIndex: "label", width: 72, render: (v: string) => <Mono faint>{v}</Mono> },
		{
			title: "Title",
			dataIndex: "title",
			render: (_: string, u) => (
				<div style={{ minWidth: 0 }}>
					<div style={{ fontWeight: 500, overflow: "hidden", textOverflow: "ellipsis", whiteSpace: "nowrap" }}>
						{u.title}
					</div>
					{u.summary && (
						<div
							className="muted"
							style={{
								fontSize: 12,
								overflow: "hidden",
								textOverflow: "ellipsis",
								whiteSpace: "nowrap",
								maxWidth: 620,
							}}
						>
							{u.summary}
						</div>
					)}
				</div>
			),
		},
		{ title: "State", dataIndex: "state", width: 190, render: (_: string, u) => <UnitStateTag unit={u} /> },
		{ title: "Attention", dataIndex: "attention", width: 150, render: (v: string) => <AttentionTag attention={v} /> },
		{
			title: "Kind",
			dataIndex: "kind",
			width: 110,
			render: (v: string) => <span className="muted">{kindLabel[v] ?? v}</span>,
		},
		{
			title: "Project",
			dataIndex: "project_name",
			width: 150,
			render: (v: string) => <span className="muted">{v}</span>,
		},
		{ title: "Updated", dataIndex: "updated_at", width: 120, render: (v: string) => <TimeAgo at={v} /> },
	];
	return (
		<Table<Unit>
			className="tf-table"
			size="small"
			rowKey="id"
			columns={columns}
			dataSource={units}
			loading={loading}
			pagination={false}
			tableLayout="fixed"
			rowClassName={(u) => (u.id === activeId ? "tf-row-active" : "")}
			onRow={(u) => ({
				onClick: () => onOpen(u),
				style: { "--edge": toneColor[unitTone(u)] } as React.CSSProperties,
			})}
		/>
	);
}

const pipelineStages: Stage[] = ["intake", "definition", "planning", "executing", "release", "done"];

// The summary strip: units per stage, each a filter.
export function StageFunnel({
	counts,
	selected,
	onSelect,
}: {
	counts: Partial<Record<Stage, number>>;
	selected: string[];
	onSelect: (stage: Stage) => void;
}) {
	const max = Math.max(1, ...pipelineStages.map((s) => counts[s] ?? 0));
	return (
		<div style={{ display: "flex", gap: 8, flex: 1, minWidth: 0 }}>
			{pipelineStages.map((s) => {
				const n = counts[s] ?? 0;
				const active = selected.includes(s);
				return (
					<button
						type="button"
						key={s}
						onClick={() => onSelect(s)}
						style={{
							flex: 1,
							minWidth: 92,
							textAlign: "left",
							padding: "8px 12px 10px",
							border: `1px solid ${active ? "var(--tf-accent)" : "var(--tf-border)"}`,
							background: active ? "var(--tf-selected)" : "var(--tf-bg)",
							borderRadius: 8,
							cursor: "pointer",
							fontFamily: "inherit",
							color: "var(--tf-text)",
						}}
					>
						<div style={{ fontSize: 12, color: "var(--tf-text2)" }}>{stageLabel(s)}</div>
						<div style={{ fontSize: 20, fontWeight: 500, fontVariantNumeric: "tabular-nums", lineHeight: 1.3 }}>
							{n}
						</div>
						<div style={{ height: 3, borderRadius: 2, background: "var(--tf-fill)", marginTop: 4 }}>
							<div style={{ width: `${(n / max) * 100}%`, height: 3, borderRadius: 2, background: stageColor(s) }} />
						</div>
					</button>
				);
			})}
		</div>
	);
}

export function stageColor(s: string): string {
	switch (s) {
		case "intake":
			return toneColor.info;
		case "definition":
		case "planning":
			return toneColor.warn;
		case "executing":
		case "release":
			return toneColor.accent;
		case "done":
			return toneColor.ok;
		default:
			return toneColor.neutral;
	}
}

// Five stages and the current one.
export function StageStepper({ unit }: { unit: Unit }) {
	const current = pipelineStages.indexOf(unit.stage);
	const rejected = unit.state === "rejected";
	return (
		<div style={{ display: "flex", alignItems: "center", gap: 0, flexWrap: "wrap" }}>
			{pipelineStages.map((s, i) => {
				const done = !rejected && (i < current || unit.state === "done");
				const here = !rejected && i === current && unit.state !== "done";
				const color = here ? toneColor[unitTone(unit)] : done ? toneColor.ok : "var(--tf-border)";
				return (
					<div key={s} style={{ display: "flex", alignItems: "center" }}>
						{i > 0 && (
							<div style={{ width: 28, height: 1, background: done || here ? toneColor.ok : "var(--tf-border)" }} />
						)}
						<div
							style={{
								display: "flex",
								alignItems: "center",
								gap: 6,
								padding: "3px 10px",
								borderRadius: 12,
								border: `1px solid ${here ? color : "var(--tf-border)"}`,
								background: here ? `color-mix(in srgb, ${color} 12%, transparent)` : "var(--tf-bg)",
								color: done || here ? "var(--tf-text)" : "var(--tf-text3)",
								fontSize: 12,
								fontWeight: here ? 500 : 400,
							}}
						>
							{done ? (
								<Check size={12} color={toneColor.ok} />
							) : (
								<span
									className={here && unit.busy ? "tf-pulse" : undefined}
									style={{ width: 7, height: 7, borderRadius: "50%", background: color }}
								/>
							)}
							{stageLabel(s)}
						</div>
					</div>
				);
			})}
			{rejected && <StatusTag tone="neutral" label="Rejected" style={{ marginLeft: 12 }} />}
		</div>
	);
}

// A banner for units that need a person, with the way out.
export function AttentionBanner({ unit }: { unit: Unit }) {
	const act = useUnitAction(unit.id);
	const { message } = App.useApp();
	const waiting = !unit.attention && isWaitingOnHuman(unit.state) && !unit.busy;
	if (!unit.attention && !waiting) return null;
	const isError = !!unit.attention && unit.attention !== "waiting" && unit.attention !== "new_feedback";
	const color = isError ? toneColor.error : toneColor.warn;
	const title = unit.attention ? (attentionLabel[unit.attention] ?? unit.attention) : "Waiting for you";
	const detail = unit.attention ? unit.attention_detail : waitingText(unit);
	return (
		<div
			style={{
				display: "flex",
				alignItems: "flex-start",
				gap: 12,
				padding: "10px 14px",
				borderRadius: 6,
				background: `color-mix(in srgb, ${color} 10%, var(--tf-bg))`,
				border: `1px solid color-mix(in srgb, ${color} 40%, transparent)`,
				boxShadow: `inset 3px 0 0 ${color}`,
			}}
		>
			<div style={{ flex: 1 }}>
				<div style={{ fontWeight: 500 }}>{title}</div>
				{detail && (
					<div className="muted" style={{ marginTop: 2, whiteSpace: "pre-wrap" }}>
						{detail}
					</div>
				)}
			</div>
			{(unit.actions ?? []).includes("acknowledge") && (
				<Button
					size="small"
					loading={act.isPending}
					onClick={() => act.mutate({ action: "acknowledge" }, { onError: (e) => message.error(e.message) })}
				>
					Got it
				</Button>
			)}
			{(unit.actions ?? []).includes("retry") && unit.attention && (
				<Button
					size="small"
					icon={<RotateCcw size={14} />}
					loading={act.isPending}
					onClick={() => act.mutate({ action: "retry" }, { onError: (e) => message.error(e.message) })}
				>
					Retry
				</Button>
			)}
		</div>
	);
}

function waitingText(u: Unit): string {
	switch (u.state) {
		case "proposed":
			return "Accept the proposal to start defining it, or reject it.";
		case "definition_review":
			return "Read the requirement. Mark it ready to start planning, edit it, or ask Claude for a revision.";
		case "spec_review":
			return "Read the spec. Approve it to start development, edit it, or ask Claude for a revision.";
		case "releasing":
			return "Merged. tfy is writing the release notes and following CI on the merge commits.";
		case "awaiting_merge":
			return "The review approved the change. Merge it from here (tfy checks nothing changed since the review), or on GitHub.";
		default:
			return "";
	}
}

const actionLabels: Partial<Record<UnitAction, string>> = {
	accept: "Accept proposal",
	"mark-ready": "Mark ready",
	"approve-spec": "Approve spec",
	iterate: "Request changes",
	back: "Back to requirement",
	refresh: "Refresh PRs",
	merge: "Merge",
	rereview: "Review again",
	"override-approve": "Approve anyway",
	"revise-spec": "Revise spec",
	reopen: "Reopen",
	"mark-released": "Mark released",
	"follow-up": "Open a follow-up fix",
};

// The action buttons for the unit's current gate.
export function UnitActions({ unit, size = "middle" }: { unit: UnitDetail | Unit; size?: "small" | "middle" }) {
	const act = useUnitAction(unit.id);
	const { message, modal } = App.useApp();
	const [iterateOpen, setIterateOpen] = useState(false);
	const [feedback, setFeedback] = useState("");
	const [closePRs, setClosePRs] = useState(true);
	const run = (action: UnitAction, extra?: { feedback?: string; closePRs?: boolean }) =>
		act.mutate(
			{ action, ...extra },
			{
				onSuccess: () => message.success(`${actionLabels[action] ?? action}: done`),
				onError: (e) => message.error(e.message),
			},
		);
	const has = (a: UnitAction) => (unit.actions ?? []).includes(a);
	const busy = (a: UnitAction) => act.isPending && act.variables?.action === a;
	const primary: UnitAction | undefined = (
		["accept", "mark-ready", "approve-spec", "merge", "reopen"] as UnitAction[]
	).find(has);
	const repos = "repos" in unit ? unit.repos : [];
	const openPRs = repos.filter((r) => r.pr_number && r.pr_state === "open");
	const reviewing = unit.state === "reviewing";

	const confirmMerge = () =>
		modal.confirm({
			title: "Merge the pull requests?",
			content: (
				<div>
					<p className="muted">
						tfy checks each one first: unchanged since the review, no conflicts. Then it merges them all.
					</p>
					<ul style={{ paddingLeft: 18 }}>
						{openPRs.map((r) => (
							<li key={r.repo_id}>
								{r.full_name} #{r.pr_number}
							</li>
						))}
					</ul>
				</div>
			),
			okText: "Merge",
			onOk: () => run("merge"),
		});

	return (
		<Space size={6} wrap>
			{has("cancel") && (
				<Popconfirm
					title="Stop the running work?"
					onConfirm={() => run("cancel")}
					okText="Stop"
					okButtonProps={{ danger: true }}
				>
					<Button size={size} danger icon={<Square size={13} />} loading={busy("cancel")}>
						Cancel run
					</Button>
				</Popconfirm>
			)}
			{has("retry") && !unit.attention && (
				<Button size={size} icon={<RotateCcw size={14} />} onClick={() => run("retry")}>
					Retry
				</Button>
			)}
			{has("refresh") && (
				<Button size={size} onClick={() => run("refresh")} loading={busy("refresh")}>
					Refresh PRs
				</Button>
			)}
			{has("back") && (
				<Button size={size} icon={<Undo2 size={14} />} onClick={() => run("back")}>
					Back to requirement
				</Button>
			)}
			{has("revise-spec") && (
				<Button size={size} icon={<Undo2 size={14} />} onClick={() => run("revise-spec")}>
					Revise spec
				</Button>
			)}
			{has("mark-released") && (
				<Popconfirm
					title="Mark this unit released?"
					description="CI is not watched any further."
					onConfirm={() => run("mark-released")}
				>
					<Button size={size}>Mark released</Button>
				</Popconfirm>
			)}
			{has("rereview") && (
				<Button size={size} onClick={() => run("rereview")} loading={busy("rereview")}>
					Review again
				</Button>
			)}
			{has("override-approve") && (
				<Popconfirm
					title="Approve despite the review?"
					description="The pull requests become mergeable as they are."
					onConfirm={() => run("override-approve")}
				>
					<Button size={size}>Approve anyway</Button>
				</Popconfirm>
			)}
			{has("iterate") && (
				<Button size={size} onClick={() => setIterateOpen(true)}>
					{reviewing ? "Iterate anyway" : "Request changes"}
				</Button>
			)}
			{has("reject") && (
				<Popconfirm
					title="Reject this unit?"
					description={
						<div style={{ maxWidth: 300 }}>
							Running work stops and the workspace is removed; documents are kept and it can be reopened.
							{openPRs.length > 0 && (
								<div style={{ marginTop: 8 }}>
									<Checkbox checked={closePRs} onChange={(e) => setClosePRs(e.target.checked)}>
										Close the {openPRs.length} open pull request(s) and delete their branches
									</Checkbox>
								</div>
							)}
						</div>
					}
					onConfirm={() => run("reject", { closePRs: openPRs.length > 0 && closePRs })}
					okText="Reject"
					okButtonProps={{ danger: true }}
				>
					<Button size={size} type="text" danger>
						Reject
					</Button>
				</Popconfirm>
			)}
			{primary && (
				<Tooltip
					title={
						primary === "approve-spec"
							? "Starts development: Claude implements the spec, tfy opens the pull requests and reviews them"
							: undefined
					}
				>
					<Button
						size={size}
						type="primary"
						icon={primary === "merge" ? <GitMerge size={14} /> : undefined}
						loading={busy(primary)}
						onClick={() => (primary === "merge" ? confirmMerge() : run(primary))}
					>
						{actionLabels[primary]}
					</Button>
				</Tooltip>
			)}
			<Modal
				title={
					reviewing
						? "Another development round"
						: unit.state === "spec_review"
							? "Revise the spec"
							: "Revise the requirement"
				}
				open={iterateOpen}
				okText={reviewing ? "Send back to development" : "Request revision"}
				okButtonProps={{ disabled: !reviewing && !feedback.trim(), loading: act.isPending }}
				onCancel={() => setIterateOpen(false)}
				onOk={() =>
					act.mutate(
						{ action: "iterate", feedback },
						{
							onSuccess: () => {
								setIterateOpen(false);
								setFeedback("");
								message.success(reviewing ? "Claude is addressing the review" : "Claude is revising it");
							},
							onError: (e) => message.error(e.message),
						},
					)
				}
			>
				<p className="muted" style={{ marginTop: 0 }}>
					{reviewing
						? "Claude resumes the development session with the review's findings. Add anything else it should fix."
						: "Claude resumes its session with your feedback and edits the document in place."}
				</p>
				<Input.TextArea
					autoFocus
					value={feedback}
					onChange={(e) => setFeedback(e.target.value)}
					autoSize={{ minRows: 5, maxRows: 14 }}
					placeholder={reviewing ? "Optional: more to fix" : "What should change?"}
				/>
			</Modal>
		</Space>
	);
}

export { stages };
