import { App, Button, Input, Popconfirm, Spin, Switch, Tabs, Tag, Tooltip } from "antd";
import {
	CircleCheck,
	CircleDot,
	MessageSquarePlus,
	PencilLine,
	RefreshCw,
	Sparkles,
	TriangleAlert,
	Unlink,
} from "lucide-react";
import { type ReactNode, useState } from "react";
import { IssuePicker } from "@/features/projects/components/IssuePicker";
import type { IssueSuggestion, LinkedIssue, UnitDetail } from "@/shared/api/types";
import { JsonView, Markdown, Mono, TimeAgo } from "@/shared/components/misc";
import { Card } from "@/shared/components/OverlayDrawer";
import { toneColor } from "@/shared/lib/status";
import { useIssueActions } from "../hooks";

// The GitHub issues a unit is linked to: what its runs read, whether merging
// closes them, and suggestions to improve them — which only a person posts.
export function IssuesCard({ unit }: { unit: UnitDetail }) {
	const actions = useIssueActions(unit.id);
	const { message } = App.useApp();
	const [ref, setRef] = useState("");
	const link = (v: string) => {
		if (!v.trim()) return;
		actions.link.mutate(
			{ ref: v.trim() },
			{
				onSuccess: (is) => {
					setRef("");
					message.success(`Linked ${is.ref}`);
				},
				onError: (e) => message.error(e.message),
			},
		);
	};
	return (
		<Card title={`GitHub issues${unit.issues.length ? ` (${unit.issues.length})` : ""}`} padded={false}>
			{unit.issues.length === 0 && (
				<div className="muted" style={{ padding: "10px 16px" }}>
					Link an issue and Claude reads it while it defines and plans the work; the pull requests reference it, and
					merging them closes it.
				</div>
			)}
			{unit.issues.map((is, i) => (
				<IssueRow key={is.id} unit={unit} issue={is} divider={i > 0} />
			))}
			<div style={{ display: "flex", gap: 8, padding: "10px 16px", borderTop: "1px solid var(--tf-border)" }}>
				<IssuePicker
					projectId={unit.project_id}
					value={ref}
					onChange={setRef}
					onPick={(o) => link(o.ref)}
					style={{ flex: 1 }}
				/>
				<Button onClick={() => link(ref)} disabled={!ref.trim()} loading={actions.link.isPending}>
					Link
				</Button>
			</div>
		</Card>
	);
}

function IssueRow({ unit, issue, divider }: { unit: UnitDetail; issue: LinkedIssue; divider: boolean }) {
	const actions = useIssueActions(unit.id);
	const { message } = App.useApp();
	const [open, setOpen] = useState(false);
	const fail = (e: Error) => message.error(e.message);
	const closed = issue.state === "closed";
	return (
		<div style={{ padding: "10px 16px", borderTop: divider ? "1px solid var(--tf-border)" : undefined }}>
			<div style={{ display: "flex", alignItems: "center", gap: 8, minWidth: 0 }}>
				{closed ? (
					<CircleCheck size={15} color={toneColor.neutral} style={{ flex: "none" }} />
				) : (
					<CircleDot size={15} color={toneColor.ok} style={{ flex: "none" }} />
				)}
				<a href={issue.url} target="_blank" rel="noreferrer" style={{ flex: "none" }}>
					<Mono>{issue.ref}</Mono>
				</a>
				<span
					title={issue.title}
					style={{
						fontWeight: 500,
						flex: 1,
						minWidth: 0,
						overflow: "hidden",
						textOverflow: "ellipsis",
						whiteSpace: "nowrap",
					}}
				>
					{issue.title}
				</span>
				{issue.labels.map((l) => (
					<Tag key={l} bordered={false} style={{ marginInlineEnd: 0 }}>
						{l}
					</Tag>
				))}
			</div>
			<div style={{ display: "flex", alignItems: "center", gap: 14, marginTop: 6, fontSize: 12, flexWrap: "wrap" }}>
				<span className="muted">
					{issue.state || "unknown"} · @{issue.author} · {issue.comments.length} comment
					{issue.comments.length === 1 ? "" : "s"}
					{issue.fetched_at && (
						<>
							{" "}
							· read <TimeAgo at={issue.fetched_at} />
						</>
					)}
				</span>
				<Tooltip
					title={
						issue.closes
							? "The pull requests say “Closes”: merging them closes the issue."
							: "The pull requests only reference the issue: it stays open after merging."
					}
				>
					<span style={{ display: "inline-flex", gap: 6, alignItems: "center" }}>
						<Switch
							size="small"
							checked={issue.closes}
							loading={actions.setCloses.isPending}
							onChange={(closes) => actions.setCloses.mutate({ id: issue.id, closes }, { onError: fail })}
						/>
						closes on merge
					</span>
				</Tooltip>
				<span style={{ flex: 1 }} />
				<Button size="small" type="text" onClick={() => setOpen(!open)}>
					{open ? "Hide the issue" : "Show the issue"}
				</Button>
				<Tooltip title="Read it from GitHub again">
					<Button
						size="small"
						type="text"
						icon={<RefreshCw size={13} />}
						loading={actions.refresh.isPending}
						onClick={() => actions.refresh.mutate(issue.id, { onError: fail })}
					/>
				</Tooltip>
				<Tooltip title="Claude compares the issue with what tfy gathered — Slack reports, the requirement, the spec, the pull requests — and suggests a comment or a clearer description. Nothing is posted until you do.">
					<Button
						size="small"
						icon={<Sparkles size={13} />}
						loading={issue.suggestion_state === "running" || actions.check.isPending}
						onClick={() => actions.check.mutate(issue.id, { onError: fail })}
					>
						Suggest improvements
					</Button>
				</Tooltip>
				<Popconfirm
					title={`Unlink ${issue.ref}?`}
					description="The issue itself is not touched."
					okText="Unlink"
					onConfirm={() => actions.unlink.mutate(issue.id, { onError: fail })}
				>
					<Button size="small" type="text" danger icon={<Unlink size={13} />} />
				</Popconfirm>
			</div>
			{open && <IssueText issue={issue} />}
			<SuggestionPanel unit={unit} issue={issue} />
		</div>
	);
}

function IssueText({ issue }: { issue: LinkedIssue }) {
	return (
		<div
			style={{
				marginTop: 8,
				border: "1px solid var(--tf-border)",
				borderRadius: 4,
				padding: "4px 12px",
				maxHeight: 420,
				overflow: "auto",
			}}
		>
			{issue.body.trim() ? <Markdown>{issue.body}</Markdown> : <p className="muted">No description.</p>}
			{issue.comments.map((c) => (
				<div key={`${c.author}-${c.at}`} style={{ borderTop: "1px solid var(--tf-border)", paddingTop: 6 }}>
					<div className="faint" style={{ fontSize: 12 }}>
						@{c.author} · <TimeAgo at={c.at} />
					</div>
					<Markdown>{c.body}</Markdown>
				</div>
			))}
		</div>
	);
}

function Note({ children, tone }: { children: ReactNode; tone?: "error" }) {
	return (
		<div
			style={{
				marginTop: 8,
				fontSize: 12,
				display: "flex",
				gap: 6,
				alignItems: "center",
				color: tone === "error" ? toneColor.error : "var(--tf-text2)",
			}}
		>
			{children}
		</div>
	);
}

function SuggestionPanel({ unit, issue }: { unit: UnitDetail; issue: LinkedIssue }) {
	const actions = useIssueActions(unit.id);
	const { message } = App.useApp();
	const s = issue.suggestion;
	const dismiss = () => actions.dismiss.mutate(issue.id, { onError: (e) => message.error(e.message) });
	switch (issue.suggestion_state) {
		case "running":
			return (
				<Note>
					<Spin size="small" /> Checking {issue.ref} against what tfy gathered…
				</Note>
			);
		case "failed":
			return <Note tone="error">Could not check the issue: {s?.error}</Note>;
		case "none":
			return (
				<Note>
					No update needed: {s?.reason}
					<Button type="link" size="small" onClick={dismiss} style={{ padding: 0, height: "auto" }}>
						Dismiss
					</Button>
				</Note>
			);
		case "ready":
		case "applied":
			return s ? (
				<SuggestionEditor key={`${issue.id}-${s.at}`} unit={unit} issue={issue} s={s} onDismiss={dismiss} />
			) : null;
		default:
			return null;
	}
}

// A suggestion, editable before it is posted. It is keyed by the suggestion,
// so a new one starts from its own text.
function SuggestionEditor({
	unit,
	issue,
	s,
	onDismiss,
}: {
	unit: UnitDetail;
	issue: LinkedIssue;
	s: IssueSuggestion;
	onDismiss: () => void;
}) {
	const actions = useIssueActions(unit.id);
	const { message } = App.useApp();
	const [comment, setComment] = useState(s.comment);
	const [title, setTitle] = useState(s.title || issue.title);
	const [body, setBody] = useState(s.body || s.base_body);
	const apply = (mode: "comment" | "edit") =>
		actions.apply.mutate(
			{ id: issue.id, mode, comment, title, body },
			{
				onSuccess: () => message.success(mode === "comment" ? `Commented on ${issue.ref}` : `Updated ${issue.ref}`),
				onError: (e) => message.error(e.message),
			},
		);
	const busy = (mode: string) => actions.apply.isPending && actions.apply.variables?.mode === mode;
	const items = [];
	if (s.comment) {
		items.push({
			key: "comment",
			label: s.commented ? "Comment ✓" : "Comment",
			children: s.commented ? (
				<p style={{ margin: 0 }}>
					Posted.{" "}
					<a href={s.comment_url} target="_blank" rel="noreferrer">
						View the comment
					</a>
				</p>
			) : (
				<>
					<Input.TextArea
						value={comment}
						onChange={(e) => setComment(e.target.value)}
						autoSize={{ minRows: 4, maxRows: 16 }}
					/>
					<Popconfirm
						title={`Post this comment on ${issue.ref}?`}
						description="It is posted with your GitHub account."
						okText="Post"
						onConfirm={() => apply("comment")}
					>
						<Button
							type="primary"
							size="small"
							icon={<MessageSquarePlus size={13} />}
							loading={busy("comment")}
							disabled={!comment.trim()}
							style={{ marginTop: 8 }}
						>
							Post comment
						</Button>
					</Popconfirm>
				</>
			),
		});
	}
	if (s.title || s.body) {
		items.push({
			key: "edit",
			label: s.edited ? "Title & description ✓" : "Title & description",
			children: s.edited ? (
				<p style={{ margin: 0 }}>Updated on GitHub.</p>
			) : (
				<>
					<Input
						value={title}
						onChange={(e) => setTitle(e.target.value)}
						style={{ marginBottom: 8, fontWeight: 500 }}
					/>
					<Input.TextArea
						value={body}
						onChange={(e) => setBody(e.target.value)}
						autoSize={{ minRows: 6, maxRows: 20 }}
					/>
					<details style={{ marginTop: 6 }}>
						<summary className="muted" style={{ cursor: "pointer", fontSize: 12 }}>
							The description as it was when checked
						</summary>
						<JsonView value={s.base_body || "(empty)"} maxHeight={240} />
					</details>
					<Popconfirm
						title={`Replace the title and description of ${issue.ref}?`}
						description="tfy first checks that nobody changed the issue since this suggestion."
						okText="Update"
						onConfirm={() => apply("edit")}
					>
						<Button
							type="primary"
							size="small"
							icon={<PencilLine size={13} />}
							loading={busy("edit")}
							disabled={!body.trim()}
							style={{ marginTop: 8 }}
						>
							Update the issue
						</Button>
					</Popconfirm>
				</>
			),
		});
	}
	return (
		<div
			style={{
				marginTop: 10,
				border: "1px solid var(--tf-border)",
				borderRadius: 6,
				background: "var(--tf-surface)",
				padding: "8px 12px 4px",
			}}
		>
			<div style={{ display: "flex", gap: 8, alignItems: "baseline" }}>
				<Sparkles size={13} style={{ flex: "none", alignSelf: "center" }} />
				<b style={{ flex: "none" }}>Suggested update</b>
				<span className="muted" style={{ flex: 1, fontSize: 12 }}>
					{s.reason}
				</span>
				{issue.suggestion_state === "ready" && (
					<Button size="small" type="text" onClick={onDismiss}>
						Dismiss
					</Button>
				)}
			</div>
			{issue.public && (
				<div style={{ fontSize: 12, margin: "6px 0 0", display: "flex", gap: 6, alignItems: "center" }}>
					<TriangleAlert size={12} color={toneColor.warn} /> {issue.repo} is public: anyone can read what you post.
				</div>
			)}
			<Tabs size="small" items={items} />
		</div>
	);
}
