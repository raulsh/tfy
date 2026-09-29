import { App, Button, Input, Modal, Select, Space, Spin, Tooltip } from "antd";
import {
	ExternalLink,
	FileCode2,
	FileImage,
	FileText,
	Maximize2,
	MessageSquarePlus,
	Minimize2,
	Shapes,
} from "lucide-react";
import { useEffect, useState } from "react";
import { useSearchParams } from "react-router-dom";
import { artifactURL } from "@/shared/api/client";
import type { Artifact, UnitDetail } from "@/shared/api/types";
import { EmptyState, Markdown, Mono } from "@/shared/components/misc";
import { Card } from "@/shared/components/OverlayDrawer";
import { bytes, timeAgo } from "@/shared/lib/format";
import { useArtifactText, useArtifactVersions, useUnitAction } from "../hooks";

type Shown = "html" | "image" | "markdown" | "text" | "other";

function shownAs(contentType: string): Shown {
	const t = contentType.split(";")[0].trim();
	if (t === "text/html") return "html";
	if (t.startsWith("image/")) return "image";
	if (t === "text/markdown") return "markdown";
	if (t.startsWith("text/") || t === "application/json" || t.endsWith("+json") || t.endsWith("xml")) return "text";
	return "other";
}

const icons: Record<Shown, typeof FileText> = {
	html: FileCode2,
	image: FileImage,
	markdown: FileText,
	text: FileText,
	other: Shapes,
};

// Whether a revision can be asked for now, and of which document.
function revisable(unit: UnitDetail): "spec" | "requirement" | undefined {
	if (unit.busy || !unit.actions.includes("iterate")) return undefined;
	if (unit.state === "spec_review") return "spec";
	if (unit.state === "definition_review") return "requirement";
	return undefined;
}

// The files the define and plan runs made for people to look at, such as
// mockups: each shown as itself, with its versions, and open to feedback
// that Claude's next revision takes up.
export function ArtifactsPanel({ unit }: { unit: UnitDetail }) {
	const [params, setParams] = useSearchParams();
	const selected = unit.artifacts.find((a) => a.path === params.get("artifact")) ?? unit.artifacts[0];

	if (!selected) {
		return (
			<Card>
				<EmptyState icon={<Shapes size={28} />} title="No artifacts">
					When the plan needs something to look at, such as a mockup or a diagram, Claude adds it here. Ask for one when
					you revise the spec.
				</EmptyState>
			</Card>
		);
	}
	return (
		<div style={{ display: "grid", gridTemplateColumns: "240px minmax(0, 1fr)", gap: 16, alignItems: "start" }}>
			<Card title={`Artifacts (${unit.artifacts.length})`} padded={false}>
				{unit.artifacts.map((a, i) => {
					const Icon = icons[shownAs(a.content_type)];
					const active = a.path === selected.path;
					return (
						<button
							key={a.path}
							type="button"
							onClick={() =>
								setParams(
									(p) => {
										const n = new URLSearchParams(p);
										n.set("artifact", a.path);
										return n;
									},
									{ replace: true },
								)
							}
							style={{
								display: "grid",
								gridTemplateColumns: "16px minmax(0, 1fr)",
								gap: 8,
								width: "100%",
								padding: "9px 12px",
								border: 0,
								borderTop: i ? "1px solid var(--tf-border)" : undefined,
								background: active ? "var(--tf-selected)" : "none",
								cursor: "pointer",
								textAlign: "left",
								fontFamily: "inherit",
								color: "var(--tf-text)",
							}}
						>
							<Icon size={15} style={{ marginTop: 2, color: "var(--tf-text2)" }} />
							<span style={{ minWidth: 0 }}>
								<span style={{ display: "block", overflowWrap: "anywhere", fontWeight: active ? 500 : 400 }}>
									{a.path}
								</span>
								<span className="faint" style={{ fontSize: 12 }}>
									v{a.version} · {bytes(a.size)} · {timeAgo(a.created_at)}
								</span>
							</span>
						</button>
					);
				})}
			</Card>
			<ArtifactViewer key={selected.path} unit={unit} artifact={selected} />
		</div>
	);
}

function ArtifactViewer({ unit, artifact }: { unit: UnitDetail; artifact: Artifact }) {
	const [version, setVersion] = useState<number>();
	const [maximized, setMaximized] = useState(false);
	const [asking, setAsking] = useState(false);
	const { data: versions } = useArtifactVersions(unit.id, artifact.path, artifact.version);
	const shown = versions?.find((v) => v.version === version) ?? artifact;
	const url = artifactURL(unit.id, artifact.path, shown.version);
	const isLatest = shown.version === artifact.version;
	const revise = revisable(unit);

	useEffect(() => {
		// Follow new versions unless an old one was picked on purpose.
		if (version && version >= artifact.version) setVersion(undefined);
	}, [artifact.version, version]);

	useEffect(() => {
		if (!maximized) return;
		const onKey = (e: KeyboardEvent) => {
			if (e.key === "Escape" && !asking) setMaximized(false);
		};
		window.addEventListener("keydown", onKey);
		return () => window.removeEventListener("keydown", onKey);
	}, [maximized, asking]);

	const header = (
		<Space size={10} wrap>
			<Mono>{artifact.path}</Mono>
			<Select
				size="small"
				value={shown.version}
				onChange={(v) => setVersion(v === artifact.version ? undefined : v)}
				popupMatchSelectWidth={false}
				options={(versions ?? [artifact]).map((v) => ({
					value: v.version,
					disabled: v.removed,
					label: `v${v.version} · ${v.removed ? "removed" : v.author === "claude" ? "Claude" : v.author} · ${timeAgo(v.created_at)}`,
				}))}
			/>
			{!isLatest && <span className="faint">viewing an older version</span>}
		</Space>
	);
	const buttons = (
		<Space size={6}>
			{revise && (
				<Tooltip title={`Claude revises the ${revise} and this artifact with your feedback`}>
					<Button size="small" icon={<MessageSquarePlus size={13} />} onClick={() => setAsking(true)}>
						Request changes
					</Button>
				</Tooltip>
			)}
			<Tooltip title="Open in a new tab">
				<Button
					size="small"
					icon={<ExternalLink size={13} />}
					href={url}
					target="_blank"
					rel="noreferrer"
					aria-label="Open in a new tab"
				/>
			</Tooltip>
			<Tooltip title={maximized ? "Restore (Esc)" : "Maximize"}>
				<Button
					size="small"
					icon={maximized ? <Minimize2 size={13} /> : <Maximize2 size={13} />}
					onClick={() => setMaximized(!maximized)}
					aria-label={maximized ? "Restore" : "Maximize"}
				/>
			</Tooltip>
		</Space>
	);
	const preview = <ArtifactPreview url={url} artifact={shown} fill={maximized} />;

	return (
		<>
			{unit.busy && (unit.state === "planning" || unit.state === "defining") && (
				<div className="muted" style={{ marginBottom: 10 }}>
					<Spin size="small" /> Claude is revising; a changed artifact shows up here as a new version.
				</div>
			)}
			{maximized ? (
				<div
					style={{
						position: "fixed",
						inset: 0,
						zIndex: 900,
						display: "flex",
						flexDirection: "column",
						background: "var(--tf-bg)",
					}}
				>
					<div
						style={{
							display: "flex",
							alignItems: "center",
							justifyContent: "space-between",
							gap: 12,
							padding: "8px 16px",
							borderBottom: "1px solid var(--tf-border)",
						}}
					>
						{header}
						{buttons}
					</div>
					<div style={{ flex: 1, minHeight: 0, overflow: "auto" }}>{preview}</div>
				</div>
			) : (
				<Card title={header} extra={buttons} padded={false}>
					{preview}
				</Card>
			)}
			<RequestArtifactChanges unit={unit} artifact={artifact} open={asking} onClose={() => setAsking(false)} />
		</>
	);
}

// The artifact as itself: a page in a sandboxed frame, an image, or text.
function ArtifactPreview({ url, artifact, fill }: { url: string; artifact: Artifact; fill: boolean }) {
	const as = shownAs(artifact.content_type);
	const text = useArtifactText(as === "markdown" || as === "text" ? url : undefined);
	const height = fill ? "100%" : "max(480px, calc(100vh - 360px))";

	switch (as) {
		case "html":
			// No allow-same-origin: the page gets an opaque origin, so it cannot
			// reach tfy's API or its cookie. The server's CSP says the same, for
			// a page opened in a tab of its own.
			return (
				<iframe
					title={artifact.path}
					src={url}
					sandbox="allow-scripts"
					referrerPolicy="no-referrer"
					style={{ display: "block", width: "100%", height, border: 0, background: "#fff" }}
				/>
			);
		case "image":
			return (
				<div style={{ padding: 16, textAlign: "center", height: fill ? "100%" : undefined, overflow: "auto" }}>
					<img src={url} alt={artifact.path} style={{ maxWidth: "100%" }} />
				</div>
			);
		case "markdown":
		case "text":
			if (text.isLoading) return <Spin style={{ display: "block", margin: 48 }} />;
			if (text.error) return <EmptyState title="Could not load it">{String(text.error)}</EmptyState>;
			return as === "markdown" ? (
				<div style={{ padding: 16 }}>
					<Markdown>{text.data ?? ""}</Markdown>
				</div>
			) : (
				<pre
					style={{
						margin: 0,
						padding: 16,
						fontFamily: "var(--tf-mono)",
						fontSize: 12.5,
						lineHeight: 1.6,
						whiteSpace: "pre-wrap",
						overflowWrap: "anywhere",
					}}
				>
					{text.data}
				</pre>
			);
		default:
			return (
				<EmptyState title="No preview for this kind of file">
					<Mono faint>{artifact.content_type}</Mono>
					<div style={{ marginTop: 10 }}>
						<a href={url} download>
							Download it
						</a>
					</div>
				</EmptyState>
			);
	}
}

function RequestArtifactChanges({
	unit,
	artifact,
	open,
	onClose,
}: {
	unit: UnitDetail;
	artifact: Artifact;
	open: boolean;
	onClose: () => void;
}) {
	const [feedback, setFeedback] = useState("");
	const act = useUnitAction(unit.id);
	const { message } = App.useApp();
	const doc = revisable(unit) ?? "spec";
	return (
		<Modal
			title={`Revise ${artifact.path}`}
			open={open}
			okText="Request revision"
			okButtonProps={{ disabled: !feedback.trim(), loading: act.isPending }}
			onCancel={onClose}
			onOk={() =>
				act.mutate(
					{ action: "iterate", feedback, artifact: artifact.path },
					{
						onSuccess: () => {
							onClose();
							setFeedback("");
							message.success("Claude is revising it");
						},
						onError: (e) => message.error(e.message),
					},
				)
			}
		>
			<p className="muted" style={{ marginTop: 0 }}>
				Claude resumes its session with your feedback, changes this artifact and the {doc} where it refers to it, and
				the changed artifact becomes a new version.
			</p>
			<Input.TextArea
				autoFocus
				value={feedback}
				onChange={(e) => setFeedback(e.target.value)}
				autoSize={{ minRows: 5, maxRows: 14 }}
				placeholder="What should change?"
			/>
		</Modal>
	);
}
