import { App, Button, Form, Input, InputNumber, Popconfirm, Select, Spin, Switch, Tabs } from "antd";
import { ExternalLink, Trash2 } from "lucide-react";
import { useEffect } from "react";
import type { Project, ProjectSettings } from "@/shared/api/types";
import { Chip } from "@/shared/components/Chip";
import { Mono, TimeAgo } from "@/shared/components/misc";
import { Card, OverlayDrawer } from "@/shared/components/OverlayDrawer";
import { StatusTag } from "@/shared/components/StatusTag";
import { type ProjectInput, useDeleteProject, useProject, useUnlinkRepo, useUpdateProject } from "../hooks";
import { ConventionsTab } from "./ConventionsTab";
import { RepoPicker } from "./RepoPicker";
import { SlackSources } from "./SlackSources";

export function ProjectDrawer({ projectId, onClose }: { projectId?: string; onClose: () => void }) {
	const { data: project } = useProject(projectId);
	const del = useDeleteProject();
	const { message } = App.useApp();
	return (
		<OverlayDrawer
			open={!!projectId}
			onClose={onClose}
			width="min(900px, 62vw)"
			stripe="var(--tf-accent)"
			title={project?.name ?? "…"}
			chips={
				project && (
					<>
						<Chip k="slug" v={project.slug} mono />
						<Chip k="repositories" v={project.repos.length} />
						<Chip k="created" v={<TimeAgo at={project.created_at} />} />
					</>
				)
			}
			actions={
				project && (
					<Popconfirm
						title="Delete this project?"
						description="Its units, runs and documents are deleted too. GitHub is not touched."
						okText="Delete"
						okButtonProps={{ danger: true }}
						onConfirm={() =>
							del.mutate(project.id, {
								onSuccess: () => {
									onClose();
									message.success("Project deleted");
								},
								onError: (e) => message.error(e.message),
							})
						}
					>
						<Button size="small" type="text" danger icon={<Trash2 size={14} />} />
					</Popconfirm>
				)
			}
		>
			{!project ? (
				<Spin style={{ display: "block", margin: 48 }} />
			) : (
				<Tabs
					items={[
						{ key: "repos", label: "Repositories", children: <ReposTab project={project} /> },
						{ key: "details", label: "Details", children: <DetailsTab project={project} /> },
						{ key: "conventions", label: "Conventions", children: <ConventionsTab project={project} /> },
						{ key: "settings", label: "Pipeline", children: <SettingsTab project={project} /> },
						{ key: "slack", label: "Slack", children: <SlackSources projectId={project.id} /> },
					]}
				/>
			)}
		</OverlayDrawer>
	);
}

function ReposTab({ project }: { project: Project }) {
	const unlink = useUnlinkRepo(project.id);
	const { message } = App.useApp();
	return (
		<>
			<Card title="Linked repositories" padded={project.repos.length === 0}>
				{project.repos.length === 0 ? (
					<span className="muted">
						Link at least one repository: planning checks it out, and development opens pull requests on it.
					</span>
				) : (
					project.repos.map((r, i) => (
						<div
							key={r.id}
							style={{
								display: "flex",
								alignItems: "center",
								gap: 10,
								padding: "10px 16px",
								borderTop: i ? "1px solid var(--tf-border)" : undefined,
							}}
						>
							<div style={{ flex: 1, minWidth: 0 }}>
								<a
									href={`https://github.com/${r.full_name}`}
									target="_blank"
									rel="noreferrer"
									style={{ fontWeight: 500 }}
								>
									{r.full_name} <ExternalLink size={11} />
								</a>
								<div className="faint" style={{ fontSize: 12 }}>
									default branch <Mono>{r.default_branch}</Mono>
								</div>
							</div>
							<StatusTag tone={r.cloned ? "ok" : "warn"} label={r.cloned ? "cloned" : "not cloned yet"} />
							<Popconfirm
								title={`Unlink ${r.full_name}?`}
								onConfirm={() => unlink.mutate(r.id, { onError: (e) => message.error(e.message) })}
							>
								<Button size="small" type="text" danger icon={<Trash2 size={14} />} />
							</Popconfirm>
						</div>
					))
				)}
			</Card>
			<Card title="Link a repository">
				<RepoPicker projectId={project.id} linked={project.repos.map((r) => r.full_name)} />
			</Card>
		</>
	);
}

function DetailsTab({ project }: { project: Project }) {
	const [form] = Form.useForm<ProjectInput>();
	const update = useUpdateProject(project.id);
	const { message } = App.useApp();
	useEffect(() => {
		form.setFieldsValue({
			name: project.name,
			description: project.description,
			product_context: project.product_context,
		});
	}, [project, form]);
	return (
		<Card>
			<Form
				form={form}
				layout="vertical"
				onFinish={(v) =>
					update.mutate(
						{ ...v, settings: project.settings },
						{ onSuccess: () => message.success("Saved"), onError: (e) => message.error(e.message) },
					)
				}
			>
				<Form.Item name="name" label="Name" rules={[{ required: true }]}>
					<Input />
				</Form.Item>
				<Form.Item name="description" label="Description">
					<Input.TextArea autoSize={{ minRows: 2 }} />
				</Form.Item>
				<Form.Item
					name="product_context"
					label="Product context"
					extra="What the product is, who uses it, and what matters. Claude reads this when it defines requirements and triages feedback."
				>
					<Input.TextArea autoSize={{ minRows: 6 }} />
				</Form.Item>
				<Button type="primary" htmlType="submit" loading={update.isPending}>
					Save
				</Button>
			</Form>
		</Card>
	);
}

function SettingsTab({ project }: { project: Project }) {
	const [form] = Form.useForm<ProjectSettings>();
	const update = useUpdateProject(project.id);
	const { message } = App.useApp();
	useEffect(() => form.setFieldsValue(project.settings), [project, form]);
	return (
		<Card>
			<Form
				form={form}
				layout="horizontal"
				labelCol={{ span: 10 }}
				labelAlign="left"
				onFinish={(settings) =>
					update.mutate(
						{
							name: project.name,
							description: project.description,
							product_context: project.product_context,
							settings: { ...project.settings, ...settings },
						},
						{ onSuccess: () => message.success("Saved"), onError: (e) => message.error(e.message) },
					)
				}
			>
				<Form.Item name="draft_prs" label="Open pull requests as drafts" valuePropName="checked">
					<Switch />
				</Form.Item>
				<Form.Item name="merge_method" label="Merge method">
					<Select
						style={{ width: 160 }}
						options={[
							{ value: "squash", label: "Squash" },
							{ value: "merge", label: "Merge commit" },
							{ value: "rebase", label: "Rebase" },
						]}
					/>
				</Form.Item>
				<Form.Item name="delete_branch" label="Delete branch after merge" valuePropName="checked">
					<Switch />
				</Form.Item>
				<Form.Item name="max_review_iterations" label="Automatic review rounds">
					<InputNumber min={0} max={5} />
				</Form.Item>
				<Form.Item name="post_review_to_github" label="Post reviews as PR comments" valuePropName="checked">
					<Switch />
				</Form.Item>
				<Form.Item name="auto_accept_proposals" label="Auto-accept triaged proposals" valuePropName="checked">
					<Switch />
				</Form.Item>
				<Form.Item name="triage_confidence_min" label="Triage confidence threshold">
					<InputNumber min={0.1} max={1} step={0.05} />
				</Form.Item>
				<Form.Item
					name="branch_template"
					label="Branch names"
					extra="{seq} is required; {slug} and {kind} are optional. Used for new units."
					rules={[
						{
							validator: (_, v: string) =>
								!v || (v.includes("{seq}") && /^[A-Za-z0-9._/{}-]+$/.test(v))
									? Promise.resolve()
									: Promise.reject(new Error("Letters, digits, . _ / - and the placeholders; must include {seq}")),
						},
					]}
				>
					<Input style={{ width: 260, fontFamily: "var(--tf-mono)" }} placeholder="tfy/u{seq}-{slug}" />
				</Form.Item>
				<Form.Item
					name="learn_from_units"
					label="Suggest convention updates after each unit"
					valuePropName="checked"
					extra="A retrospective looks at what went back and forth and may propose changes to CLAUDE.md, rules or hooks, as a unit to accept."
				>
					<Switch />
				</Form.Item>
				<Button type="primary" htmlType="submit" loading={update.isPending}>
					Save
				</Button>
			</Form>
		</Card>
	);
}
