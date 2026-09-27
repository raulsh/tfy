import { App, Button, Input, Spin, Tag, Tooltip } from "antd";
import { ChevronDown, ChevronRight, FilePlus2, RefreshCw } from "lucide-react";
import { type ReactNode, useEffect, useMemo, useState } from "react";
import { NewUnitModal } from "@/features/units/components/NewUnitModal";
import type { NewUnit } from "@/features/units/hooks";
import type { ConventionFile, Project, RepoConventions } from "@/shared/api/types";
import { JsonView, Markdown, Mono } from "@/shared/components/misc";
import { Card } from "@/shared/components/OverlayDrawer";
import { StatusTag } from "@/shared/components/StatusTag";
import { useConventions, useUpdateProject } from "../hooks";

// Conventions are Claude Code's to apply: this tab shows what each run of the
// project will load, and where to change it. Changes to a repository's files
// go through a unit, so they land as a reviewed pull request.
export function ConventionsTab({ project }: { project: Project }) {
	const conventions = useConventions(project.id);
	const [propose, setPropose] = useState<Partial<NewUnit> | undefined>();
	const { message } = App.useApp();
	const proposeFor = (repo?: string) =>
		setPropose({
			project_id: project.id,
			kind: "chore",
			title: repo ? `Update the Claude conventions of ${repo}` : "Update the Claude conventions",
			description:
				"What should change in CLAUDE.md, .claude/rules, the hooks in .claude/settings.json, or the pull request template, and why:\n\n",
		});

	return (
		<>
			<div style={{ display: "flex", gap: 12, alignItems: "flex-start", marginBottom: 16 }}>
				<p className="muted" style={{ margin: 0, flex: 1 }}>
					Every run loads each repository's conventions the way Claude Code does in the repository itself:{" "}
					<Mono>CLAUDE.md</Mono> and <Mono>.claude/rules</Mono>, plus the hooks and attribution in{" "}
					<Mono>.claude/settings.json</Mono> from the default branch. Keeping them in the repository means people using
					Claude Code by hand follow them too.{" "}
					{project.settings.learn_from_units
						? "After each unit, a retrospective proposes changes to them as a new unit."
						: "Retrospectives are off for this project (Pipeline tab)."}
				</p>
				<Button icon={<FilePlus2 size={14} />} onClick={() => proposeFor()}>
					Propose a change
				</Button>
				<Tooltip title="Fetch the repositories and read their conventions again">
					<Button
						icon={<RefreshCw size={14} />}
						loading={conventions.refresh.isPending}
						onClick={() => conventions.refresh.mutate(undefined, { onError: (e) => message.error(e.message) })}
					/>
				</Tooltip>
			</div>
			<ProjectConventions project={project} />
			{conventions.isLoading ? (
				<Spin style={{ display: "block", margin: 32 }} />
			) : (
				(conventions.data ?? []).map((rc) => (
					<RepoConventionsCard key={rc.repo_id} rc={rc} onPropose={() => proposeFor(rc.repo)} />
				))
			)}
			<NewUnitModal
				open={!!propose}
				onClose={() => setPropose(undefined)}
				defaults={propose}
				title="Propose a change to the conventions"
			/>
		</>
	);
}

function ProjectConventions({ project }: { project: Project }) {
	const [text, setText] = useState(project.conventions);
	const update = useUpdateProject(project.id);
	const { message } = App.useApp();
	useEffect(() => setText(project.conventions), [project.conventions]);
	const dirty = text.trim() !== project.conventions.trim();
	return (
		<Card
			title="Across the project"
			extra={
				<Button
					size="small"
					type={dirty ? "primary" : "default"}
					disabled={!dirty}
					loading={update.isPending}
					onClick={() =>
						update.mutate(
							{
								name: project.name,
								description: project.description,
								product_context: project.product_context,
								conventions: text,
								settings: project.settings,
							},
							{ onSuccess: () => message.success("Saved"), onError: (e) => message.error(e.message) },
						)
					}
				>
					Save
				</Button>
			}
		>
			<p className="muted" style={{ marginTop: 0 }}>
				For rules every repository of {project.name} shares. They go into the workspace's <Mono>CLAUDE.md</Mono> next to
				the repositories' own, which win where they disagree.
			</p>
			<Input.TextArea
				value={text}
				onChange={(e) => setText(e.target.value)}
				autoSize={{ minRows: 4, maxRows: 16 }}
				style={{ fontFamily: "var(--tf-mono)", fontSize: 12 }}
				placeholder={
					"- Commit messages are a single line in Conventional Commits form, with no Co-Authored-By trailer.\n- Pull request titles are Conventional Commits too; descriptions say what changed, why, and how it was tested."
				}
			/>
		</Card>
	);
}

const kindLabel: Record<ConventionFile["kind"], string> = {
	instructions: "instructions",
	rule: "rule",
	hook_script: "hook script",
	settings: "settings",
	pr_template: "PR template",
};

function RepoConventionsCard({ rc, onPropose }: { rc: RepoConventions; onPropose: () => void }) {
	const groups = useMemo(() => {
		const always = rc.files.filter((f) => f.kind === "instructions" || (f.kind === "rule" && !f.paths?.length));
		const scoped = rc.files.filter((f) => f.kind === "rule" && f.paths?.length);
		const other = rc.files.filter((f) => f.kind === "pr_template" || f.kind === "hook_script" || f.kind === "settings");
		return { always, scoped, other };
	}, [rc.files]);
	const empty = rc.files.length === 0;
	return (
		<Card
			padded={false}
			title={
				<span>
					{rc.repo} <Mono faint>{rc.default_branch}</Mono>
				</span>
			}
			extra={
				<Button size="small" type="text" icon={<FilePlus2 size={13} />} onClick={onPropose}>
					Propose
				</Button>
			}
		>
			{rc.error ? (
				<div style={{ padding: 16 }}>
					<StatusTag tone="error" label="could not read" /> <span className="muted">{rc.error}</span>
				</div>
			) : empty ? (
				<div style={{ padding: 16 }} className="muted">
					No conventions for Claude yet: runs follow only the project's. Propose a <Mono>CLAUDE.md</Mono> with the
					commit and pull request rules, and it lands as a pull request you review.
				</div>
			) : (
				<>
					<Section title="Loaded when a session starts" files={groups.always} />
					<Section title="Loaded when Claude works on matching files" files={groups.scoped} />
					{rc.hooks.length > 0 && (
						<Group title="Hooks">
							{rc.hooks.map((h, i) => (
								<div
									key={`${h.event}:${h.matcher ?? ""}:${h.command ?? ""}:${i}`}
									style={{
										display: "grid",
										gridTemplateColumns: "150px 110px minmax(0, 1fr)",
										gap: 10,
										padding: "6px 16px",
									}}
								>
									<span>
										{h.event}
										{h.type !== "command" && (
											<Tag style={{ marginLeft: 6 }} bordered={false}>
												{h.type}
											</Tag>
										)}
									</span>
									<Mono faint>{h.matcher || "any"}</Mono>
									<Mono>{h.command}</Mono>
								</div>
							))}
						</Group>
					)}
					<Group title="Attribution">
						<div style={{ padding: "6px 16px" }}>
							{rc.attribution ? (
								<span>
									commits: {rc.attribution.commit ? <Mono>{rc.attribution.commit}</Mono> : "none"} · pull requests:{" "}
									{rc.attribution.pr ? <Mono>{rc.attribution.pr}</Mono> : "none"}
								</span>
							) : (
								<span className="muted">Not set here: tfy's runs add none.</span>
							)}
						</div>
					</Group>
					<Section title="Also here" files={groups.other} />
				</>
			)}
		</Card>
	);
}

function Group({ title, children }: { title: string; children: ReactNode }) {
	return (
		<div style={{ borderTop: "1px solid var(--tf-border)", padding: "8px 0" }}>
			<div className="faint" style={{ fontSize: 12, padding: "0 16px 4px" }}>
				{title}
			</div>
			{children}
		</div>
	);
}

function Section({ title, files }: { title: string; files: ConventionFile[] }) {
	if (files.length === 0) return null;
	return (
		<Group title={title}>
			{files.map((f) => (
				<FileRow key={f.path} file={f} />
			))}
		</Group>
	);
}

function FileRow({ file }: { file: ConventionFile }) {
	const [open, setOpen] = useState(false);
	const markdown = file.path.endsWith(".md");
	return (
		<div>
			<button
				type="button"
				onClick={() => setOpen(!open)}
				style={{
					display: "flex",
					alignItems: "center",
					gap: 8,
					width: "100%",
					padding: "5px 16px",
					background: "none",
					border: "none",
					cursor: "pointer",
					color: "inherit",
					textAlign: "left",
				}}
			>
				{open ? <ChevronDown size={13} /> : <ChevronRight size={13} />}
				<Mono>{file.path}</Mono>
				<Tag bordered={false} style={{ marginInlineEnd: 0 }}>
					{kindLabel[file.kind]}
				</Tag>
				{file.paths?.map((p) => (
					<Tag key={p} color="blue" bordered={false} style={{ fontFamily: "var(--tf-mono)", marginInlineEnd: 0 }}>
						{p}
					</Tag>
				))}
			</button>
			{open && (
				<div style={{ padding: "4px 16px 10px 37px" }}>
					{markdown ? (
						<div
							style={{
								border: "1px solid var(--tf-border)",
								borderRadius: 4,
								padding: "4px 12px",
								maxHeight: 420,
								overflow: "auto",
							}}
						>
							<Markdown>{file.content}</Markdown>
						</div>
					) : (
						<JsonView value={file.content} maxHeight={420} />
					)}
				</div>
			)}
		</div>
	);
}
