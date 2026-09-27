import { App, Button, Form, Input, Modal, Table } from "antd";
import type { ColumnsType } from "antd/es/table";
import { FolderGit2, Plus } from "lucide-react";
import { useState } from "react";
import { useSearchParams } from "react-router-dom";
import { useScope } from "@/app/scope";
import type { Project } from "@/shared/api/types";
import { EmptyState, Mono, PageHeader, TimeAgo } from "@/shared/components/misc";
import { toneColor } from "@/shared/lib/status";
import { ProjectDrawer } from "../components/ProjectDrawer";
import { type ProjectInput, useCreateProject, useProjects } from "../hooks";

export default function ProjectsPage() {
	const { data, isLoading } = useProjects();
	const [params, setParams] = useSearchParams();
	const [creating, setCreating] = useState(false);
	const selected = params.get("project") ?? undefined;
	const select = (id?: string) =>
		setParams(
			(p) => {
				const n = new URLSearchParams(p);
				if (id) n.set("project", id);
				else n.delete("project");
				return n;
			},
			{ replace: true },
		);

	const columns: ColumnsType<Project> = [
		{
			title: "Project",
			dataIndex: "name",
			render: (v: string, p) => (
				<div>
					<div style={{ fontWeight: 500 }}>{v}</div>
					{p.description && (
						<div className="muted" style={{ fontSize: 12 }}>
							{p.description}
						</div>
					)}
				</div>
			),
		},
		{
			title: "Repositories",
			dataIndex: "repos",
			render: (_: unknown, p) =>
				p.repos.length ? (
					<span style={{ display: "flex", gap: 8, flexWrap: "wrap" }}>
						{p.repos.map((r) => (
							<Mono key={r.id}>{r.full_name}</Mono>
						))}
					</span>
				) : (
					<span style={{ color: "var(--tf-error)", fontSize: 12 }}>no repositories linked</span>
				),
		},
		{ title: "Updated", dataIndex: "updated_at", width: 130, render: (v: string) => <TimeAgo at={v} /> },
	];

	return (
		<div style={{ display: "flex", flexDirection: "column", height: "100%" }}>
			<PageHeader
				title="Projects"
				extra={
					<Button type="primary" icon={<Plus size={15} />} onClick={() => setCreating(true)}>
						New project
					</Button>
				}
			/>
			<div style={{ flex: 1, overflow: "auto" }}>
				{!isLoading && data?.length === 0 ? (
					<EmptyState icon={<FolderGit2 size={28} />} title="No projects yet">
						A project groups the GitHub repositories a change can touch.{" "}
						<Button type="link" style={{ padding: 0 }} onClick={() => setCreating(true)}>
							Create one
						</Button>
						.
					</EmptyState>
				) : (
					<Table<Project>
						className="tf-table"
						size="small"
						rowKey="id"
						columns={columns}
						dataSource={data}
						loading={isLoading}
						pagination={false}
						rowClassName={(p) => (p.id === selected ? "tf-row-active" : "")}
						onRow={(p) => ({
							onClick: () => select(p.id),
							style: { "--edge": p.repos.length ? toneColor.accent : toneColor.warn } as React.CSSProperties,
						})}
					/>
				)}
			</div>
			<ProjectDrawer projectId={selected} onClose={() => select(undefined)} />
			<NewProjectModal
				open={creating}
				onClose={() => setCreating(false)}
				onCreated={(p) => {
					setCreating(false);
					select(p.id);
				}}
			/>
		</div>
	);
}

function NewProjectModal({
	open,
	onClose,
	onCreated,
}: {
	open: boolean;
	onClose: () => void;
	onCreated: (p: Project) => void;
}) {
	const [form] = Form.useForm<ProjectInput>();
	const create = useCreateProject();
	const { setProjectId } = useScope();
	const { message } = App.useApp();
	return (
		<Modal
			title="New project"
			open={open}
			onCancel={onClose}
			onOk={() => form.submit()}
			okText="Create"
			confirmLoading={create.isPending}
			destroyOnHidden
		>
			<Form<ProjectInput>
				form={form}
				layout="vertical"
				requiredMark={false}
				onFinish={(v) =>
					create.mutate(v, {
						onSuccess: (p) => {
							form.resetFields();
							setProjectId(p.id);
							onCreated(p);
						},
						onError: (e) => message.error(e.message),
					})
				}
			>
				<Form.Item name="name" label="Name" rules={[{ required: true, message: "Name it" }]}>
					<Input placeholder="e.g. Billing" />
				</Form.Item>
				<Form.Item name="description" label="Description">
					<Input placeholder="One line" />
				</Form.Item>
				<Form.Item
					name="product_context"
					label="Product context"
					extra="Optional; editable later. Claude reads it when defining requirements."
				>
					<Input.TextArea autoSize={{ minRows: 3 }} />
				</Form.Item>
			</Form>
		</Modal>
	);
}
