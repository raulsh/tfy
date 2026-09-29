import { App, Button, Form, Input, Modal, Segmented, Select, Space, Table } from "antd";
import type { ColumnsType } from "antd/es/table";
import { Inbox, Link2, Plus } from "lucide-react";
import { useCallback, useMemo, useState } from "react";
import { useNavigate, useSearchParams } from "react-router-dom";
import { ranges, useScope } from "@/app/scope";
import { useProjects } from "@/features/projects/hooks";
import { RunOverridesField } from "@/features/units/components/RunOverrides";
import { useStats } from "@/shared/api/system";
import type { Feedback } from "@/shared/api/types";
import { FacetPanel } from "@/shared/components/FacetPanel";
import { EmptyState, Mono, PageHeader, TimeAgo } from "@/shared/components/misc";
import { QueryBar } from "@/shared/components/QueryBar";
import { StatusTag } from "@/shared/components/StatusTag";
import { type FacetDef, useFacetedList } from "@/shared/lib/facets";
import { kindLabel, toneColor } from "@/shared/lib/status";
import { FeedbackDrawer } from "../components/FeedbackDrawer";
import { type FromFeedback, useFeedback, useImportPermalinks, useUnitFromFeedback } from "../hooks";
import { feedbackLabel, feedbackOrder, feedbackTone } from "../status";

const defs: FacetDef<Feedback>[] = [
	{
		key: "status",
		label: "Triage",
		get: (f) => f.status,
		format: (v) => feedbackLabel[v as Feedback["status"]] ?? v,
		order: feedbackOrder,
		color: (v) => toneColor[feedbackTone[v as Feedback["status"]] ?? "neutral"],
	},
	{ key: "channel", label: "Channel", get: (f) => (f.channel_name ? `#${f.channel_name}` : undefined) },
	{ key: "kind", label: "Kind", get: (f) => f.triage?.kind, format: (v) => kindLabel[v] ?? v },
	{ key: "author", label: "Author", get: (f) => f.author || undefined },
];

// The inbox: every Slack message tfy has read, how triage judged it,
// and where it went. Pick messages to turn them into a unit by hand.
export default function InboxPage() {
	const { projectId, range, setRange, inRange } = useScope();
	const { data, isLoading } = useFeedback(projectId);
	const { data: stats } = useStats(projectId);
	const [params, setParams] = useSearchParams();
	const [selected, setSelected] = useState<string[]>([]);
	const [creating, setCreating] = useState(false);
	const [importing, setImporting] = useState(false);

	const items = useMemo(() => (data ?? []).filter((f) => inRange(f.posted_at)), [data, inRange]);
	const text = useCallback((f: Feedback) => `${f.author} ${f.text} ${f.channel_name} ${f.unit_label ?? ""}`, []);
	const f = useFacetedList(items, defs, text);
	const openId = params.get("message") ?? undefined;
	const index = f.filtered.findIndex((x) => x.id === openId);
	const open = (id?: string) =>
		setParams(
			(p) => {
				const n = new URLSearchParams(p);
				if (id) n.set("message", id);
				else n.delete("message");
				return n;
			},
			{ replace: true },
		);

	const columns: ColumnsType<Feedback> = [
		{ title: "Time", dataIndex: "posted_at", width: 120, render: (v: string) => <TimeAgo at={v} /> },
		{ title: "Channel", dataIndex: "channel_name", width: 130, render: (v: string) => <Mono faint>#{v}</Mono> },
		{
			title: "Author",
			dataIndex: "author",
			width: 120,
			render: (v: string) => <span style={{ fontWeight: 500 }}>{v}</span>,
		},
		{
			title: "Message",
			dataIndex: "text",
			render: (v: string, r) => (
				<div style={{ overflow: "hidden", textOverflow: "ellipsis", whiteSpace: "nowrap" }}>
					{r.thread_ts && <span className="faint">↳ </span>}
					{v}
					{r.reply_count > 0 && <span className="faint"> · {r.reply_count} replies</span>}
				</div>
			),
		},
		{
			title: "Triage",
			dataIndex: "status",
			width: 180,
			render: (v: Feedback["status"], r) => (
				<Space size={6}>
					<StatusTag tone={feedbackTone[v]} label={feedbackLabel[v]} pulse={v === "triaging"} />
					{r.triage?.confidence != null && v !== "inbox" && (
						<span className="faint mono" style={{ fontSize: 11 }}>
							{Math.round(r.triage.confidence * 100)}%
						</span>
					)}
				</Space>
			),
		},
		{
			title: "Unit",
			dataIndex: "unit_label",
			width: 90,
			render: (v: string | undefined) => (v ? <Mono>{v}</Mono> : <span className="faint">—</span>),
		},
	];

	const slackOff = stats && !stats.slack_enabled;
	return (
		<div style={{ display: "flex", flexDirection: "column", height: "100%" }}>
			<PageHeader
				title="Inbox"
				extra={
					<>
						<Select value={range} onChange={setRange} options={ranges} style={{ width: 150 }} />
						<Button icon={<Link2 size={15} />} onClick={() => setImporting(true)}>
							Import links
						</Button>
						<Button
							type="primary"
							icon={<Plus size={15} />}
							disabled={selected.length === 0}
							onClick={() => setCreating(true)}
						>
							Create unit{selected.length > 0 ? ` from ${selected.length}` : ""}
						</Button>
					</>
				}
			/>
			<QueryBar groups={f.groups} search={f.search} onSearch={f.setSearch} onRemove={f.toggle} />
			<div style={{ display: "flex", flex: 1, minHeight: 0 }}>
				<FacetPanel groups={f.groups} onToggle={f.toggle} onOnly={f.only} onClear={f.clear} total={f.filtered.length} />
				<div style={{ flex: 1, overflow: "auto" }}>
					{!isLoading && items.length === 0 ? (
						<EmptyState icon={<Inbox size={28} />} title={slackOff ? "Slack intake is off" : "Nothing in the inbox"}>
							{slackOff
								? "Install slk and run `slk configure`, then restart tfy."
								: "Add Slack channels to a project (Projects → Slack): every new message is read, triaged, and shows up here."}
						</EmptyState>
					) : (
						<Table<Feedback>
							className="tf-table"
							size="small"
							rowKey="id"
							columns={columns}
							dataSource={f.filtered}
							loading={isLoading}
							pagination={false}
							tableLayout="fixed"
							rowSelection={{
								selectedRowKeys: selected,
								onChange: (keys) => setSelected(keys as string[]),
								getCheckboxProps: (r) => ({ disabled: !!r.unit_id }),
								columnWidth: 36,
							}}
							rowClassName={(r) => (r.id === openId ? "tf-row-active" : "")}
							onRow={(r) => ({
								onClick: () => open(r.id),
								style: { "--edge": toneColor[feedbackTone[r.status]] } as React.CSSProperties,
							})}
						/>
					)}
				</div>
			</div>
			<FeedbackDrawer
				feedbackId={openId}
				onClose={() => open(undefined)}
				onPrev={index > 0 ? () => open(f.filtered[index - 1].id) : undefined}
				onNext={index >= 0 && index < f.filtered.length - 1 ? () => open(f.filtered[index + 1].id) : undefined}
			/>
			<CreateUnitModal
				open={creating}
				feedback={(data ?? []).filter((x) => selected.includes(x.id))}
				onClose={() => setCreating(false)}
				onCreated={() => {
					setCreating(false);
					setSelected([]);
				}}
			/>
			<ImportModal open={importing} onClose={() => setImporting(false)} />
		</div>
	);
}

function CreateUnitModal({
	open,
	feedback,
	onClose,
	onCreated,
}: {
	open: boolean;
	feedback: Feedback[];
	onClose: () => void;
	onCreated: () => void;
}) {
	const [form] = Form.useForm<FromFeedback>();
	const create = useUnitFromFeedback();
	const navigate = useNavigate();
	const { message } = App.useApp();
	const projects = new Set(feedback.map((f) => f.project_id));
	return (
		<Modal
			title={`New unit from ${feedback.length} message(s)`}
			open={open}
			onCancel={onClose}
			onOk={() => form.submit()}
			okText="Create and start defining"
			confirmLoading={create.isPending}
			okButtonProps={{ disabled: projects.size !== 1 }}
			destroyOnHidden
			width={620}
		>
			{projects.size > 1 && (
				<p style={{ color: "var(--tf-error)" }}>The messages come from different projects; pick messages from one.</p>
			)}
			<div
				style={{
					maxHeight: 180,
					overflow: "auto",
					border: "1px solid var(--tf-border)",
					borderRadius: 4,
					marginBottom: 14,
				}}
			>
				{feedback.map((f, i) => (
					<div
						key={f.id}
						style={{ padding: "6px 10px", borderTop: i ? "1px solid var(--tf-border)" : undefined, fontSize: 12.5 }}
					>
						<b>{f.author}</b> <span className="faint">#{f.channel_name}</span> — {f.text}
					</div>
				))}
			</div>
			<Form<FromFeedback>
				form={form}
				layout="vertical"
				requiredMark={false}
				initialValues={{ kind: "feature" }}
				onFinish={(v) =>
					create.mutate(
						{ ...v, project_id: feedback[0]?.project_id ?? "", feedback_ids: feedback.map((f) => f.id) },
						{
							onSuccess: (u) => {
								onCreated();
								navigate(`/units/${u.id}`);
							},
							onError: (e) => message.error(e.message),
						},
					)
				}
			>
				<Form.Item name="kind" label="Kind">
					<Segmented
						options={[
							{ value: "feature", label: "Feature" },
							{ value: "bugfix", label: "Bug fix" },
							{ value: "improvement", label: "Improvement" },
							{ value: "chore", label: "Chore" },
						]}
					/>
				</Form.Item>
				<Form.Item name="title" label="Title" extra="Optional: Claude proposes one when it defines the requirement.">
					<Input placeholder="Leave empty to let Claude title it" />
				</Form.Item>
				<Form.Item name="description" label="Note for Claude">
					<Input.TextArea autoSize={{ minRows: 2 }} placeholder="Optional context the messages don't carry" />
				</Form.Item>
				<RunOverridesField />
			</Form>
		</Modal>
	);
}

function ImportModal({ open, onClose }: { open: boolean; onClose: () => void }) {
	const { projectId } = useScope();
	const { data: projects } = useProjects();
	const [project, setProject] = useState<string | undefined>(projectId);
	const [links, setLinks] = useState("");
	const imp = useImportPermalinks();
	const { message } = App.useApp();
	const target = project ?? projectId ?? projects?.[0]?.id;
	return (
		<Modal
			title="Import Slack messages by link"
			open={open}
			onCancel={onClose}
			okText="Import"
			confirmLoading={imp.isPending}
			okButtonProps={{ disabled: !target || !links.trim() }}
			onOk={() =>
				imp.mutate(
					{ project_id: target ?? "", permalinks: links.split(/\s+/).filter(Boolean) },
					{
						onSuccess: (fbs) => {
							message.success(`${fbs.length} message(s) imported to the inbox`);
							setLinks("");
							onClose();
						},
						onError: (e) => message.error(e.message),
					},
				)
			}
		>
			<p className="muted" style={{ marginTop: 0 }}>
				Paste one or more message links ("Copy link" in Slack). Their whole threads land in the inbox, where you can
				group them into a unit.
			</p>
			<Select
				style={{ width: "100%", marginBottom: 10 }}
				value={target}
				onChange={setProject}
				options={(projects ?? []).map((p) => ({ value: p.id, label: p.name }))}
				placeholder="Project"
			/>
			<Input.TextArea
				value={links}
				onChange={(e) => setLinks(e.target.value)}
				autoSize={{ minRows: 4 }}
				placeholder="https://yourteam.slack.com/archives/C0123/p1790000000000100"
			/>
		</Modal>
	);
}
