import { App, Button, Input, Select, Space, Spin } from "antd";
import { FileText, Pencil } from "lucide-react";
import { useEffect, useState } from "react";
import { Link } from "react-router-dom";
import type { UnitDetail } from "@/shared/api/types";
import { EmptyState, Markdown, Mono } from "@/shared/components/misc";
import { Card } from "@/shared/components/OverlayDrawer";
import { timeAgo } from "@/shared/lib/format";
import { useDocument, useSaveDocument } from "../hooks";

interface Criterion {
	id: string;
	text: string;
}

// A unit document: rendered, editable by a person, with its version history.
// Claude's runs and people's edits both add versions.
export function DocumentPanel({ unit, kind }: { unit: UnitDetail; kind: "requirement" | "spec" }) {
	const [version, setVersion] = useState<number | undefined>();
	const { data, isLoading, error } = useDocument(unit.id, kind, version);
	const save = useSaveDocument(unit.id, kind);
	const { message } = App.useApp();
	const [editing, setEditing] = useState(false);
	const [draft, setDraft] = useState("");
	const latest = unit.documents[kind]?.version;

	useEffect(() => {
		// Follow new versions unless an old one was picked on purpose.
		if (version && latest && version > latest) setVersion(undefined);
	}, [latest, version]);

	if (isLoading) return <Spin style={{ display: "block", margin: 48 }} />;
	if (error || !data) {
		const drafting = unit.busy && (kind === "requirement" ? unit.state === "defining" : unit.state === "planning");
		return (
			<Card>
				<EmptyState icon={<FileText size={28} />} title={drafting ? "Claude is drafting it" : "No document yet"}>
					{drafting
						? "Follow the run live from the Runs tab."
						: kind === "spec"
							? "The spec is written once the requirement is marked ready."
							: "The requirement is drafted when the unit is defined."}
				</EmptyState>
			</Card>
		);
	}

	const doc = data.document;
	const isLatest = !version || version === latest;
	const editable = isLatest && !unit.busy && !["done", "rejected"].includes(unit.state);
	const meta = doc.meta as {
		acceptance_criteria?: Criterion[];
		target_repos?: string[];
		new_dependencies?: string[];
		open_questions?: string[];
		summary?: string;
	};

	return (
		<div style={{ display: "grid", gridTemplateColumns: "minmax(0, 1fr) 300px", gap: 16, alignItems: "start" }}>
			<Card
				title={
					<Space size={10}>
						<Select
							size="small"
							value={doc.version}
							onChange={(v) => setVersion(v === latest ? undefined : v)}
							popupMatchSelectWidth={false}
							options={data.versions.map((v) => ({
								value: v.version,
								label: `v${v.version} · ${v.author === "claude" ? "Claude" : v.author} · ${timeAgo(v.created_at)}`,
							}))}
						/>
						{!isLatest && <span className="faint">viewing an older version</span>}
					</Space>
				}
				extra={
					editing ? (
						<Space>
							<Button size="small" onClick={() => setEditing(false)}>
								Cancel
							</Button>
							<Button
								size="small"
								type="primary"
								loading={save.isPending}
								onClick={() =>
									save.mutate(draft, {
										onSuccess: () => {
											setEditing(false);
											message.success("Saved as a new version");
										},
										onError: (e) => message.error(e.message),
									})
								}
							>
								Save version
							</Button>
						</Space>
					) : (
						editable && (
							<Button
								size="small"
								icon={<Pencil size={13} />}
								onClick={() => {
									setDraft(doc.content);
									setEditing(true);
								}}
							>
								Edit
							</Button>
						)
					)
				}
			>
				{editing ? (
					<Input.TextArea
						value={draft}
						onChange={(e) => setDraft(e.target.value)}
						autoSize={{ minRows: 24 }}
						style={{ fontFamily: "var(--tf-mono)", fontSize: 12.5, lineHeight: 1.6 }}
					/>
				) : (
					<Markdown>{doc.content}</Markdown>
				)}
			</Card>
			<div>
				{kind === "spec" && (
					<>
						{unit.artifacts.length > 0 && (
							<Card title="Artifacts">
								<Space direction="vertical" size={4}>
									{unit.artifacts.map((a) => (
										<Link key={a.path} to={`?tab=artifacts&artifact=${encodeURIComponent(a.path)}`}>
											{a.path} <span className="faint">v{a.version}</span>
										</Link>
									))}
								</Space>
							</Card>
						)}
						<Card title="Acceptance criteria">
							{meta.acceptance_criteria?.length ? (
								<ul style={{ margin: 0, paddingLeft: 0, listStyle: "none", display: "grid", gap: 8 }}>
									{meta.acceptance_criteria.map((c) => (
										<li key={c.id} style={{ display: "grid", gridTemplateColumns: "44px 1fr", gap: 6 }}>
											<Mono faint>{c.id}</Mono>
											<span>{c.text}</span>
										</li>
									))}
								</ul>
							) : (
								<span className="faint">None recorded</span>
							)}
						</Card>
						<Card title="Target repositories">
							{meta.target_repos?.length ? (
								<Space wrap>
									{meta.target_repos.map((r) => (
										<Mono key={r}>{r}</Mono>
									))}
								</Space>
							) : (
								<span className="faint">—</span>
							)}
						</Card>
						<Card title="New dependencies">
							{meta.new_dependencies?.length ? (
								<Space direction="vertical" size={2}>
									{meta.new_dependencies.map((d) => (
										<Mono key={d}>{d}</Mono>
									))}
								</Space>
							) : (
								<span className="faint">None</span>
							)}
						</Card>
					</>
				)}
				{kind === "requirement" && (
					<>
						{meta.summary && <Card title="Summary">{meta.summary}</Card>}
						<Card title="Open questions">
							{meta.open_questions?.length ? (
								<ol style={{ margin: 0, paddingLeft: 18, display: "grid", gap: 6 }}>
									{meta.open_questions.map((q, i) => (
										<li key={i}>{q}</li>
									))}
								</ol>
							) : (
								<span className="faint">None</span>
							)}
						</Card>
					</>
				)}
			</div>
		</div>
	);
}
