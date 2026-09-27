import { App, Form, Input, Modal, Segmented, Select } from "antd";
import { useEffect } from "react";
import { useNavigate } from "react-router-dom";
import { useScope } from "@/app/scope";
import { useProjects } from "@/features/projects/hooks";
import { type NewUnit, useCreateUnit } from "../hooks";

// A developer's feature or bugfix: it skips intake and goes straight to
// definition.
export function NewUnitModal({ open, onClose }: { open: boolean; onClose: () => void }) {
	const [form] = Form.useForm<NewUnit>();
	const { data: projects } = useProjects();
	const { projectId } = useScope();
	const create = useCreateUnit();
	const navigate = useNavigate();
	const { message } = App.useApp();

	useEffect(() => {
		if (open) {
			form.resetFields();
			form.setFieldsValue({ project_id: projectId ?? projects?.[0]?.id, kind: "feature" });
		}
	}, [open, form, projectId, projects]);

	return (
		<Modal
			title="New unit"
			open={open}
			onCancel={onClose}
			okText="Create and start defining"
			confirmLoading={create.isPending}
			onOk={() => form.submit()}
			width={620}
			destroyOnHidden
		>
			<p className="muted" style={{ marginTop: 0 }}>
				Describe what you want. Claude drafts the requirement first; nothing touches code until you approve a spec.
			</p>
			<Form<NewUnit>
				form={form}
				layout="vertical"
				requiredMark={false}
				onFinish={(v) =>
					create.mutate(v, {
						onSuccess: (u) => {
							onClose();
							navigate(`/units/${u.id}`);
						},
						onError: (e) => message.error(e.message),
					})
				}
			>
				<Form.Item name="project_id" label="Project" rules={[{ required: true, message: "Pick a project" }]}>
					<Select
						placeholder="Project"
						options={(projects ?? []).map((p) => ({ value: p.id, label: p.name }))}
						notFoundContent="Create a project first"
					/>
				</Form.Item>
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
				<Form.Item name="title" label="Title" rules={[{ required: true, message: "Give it a title" }]}>
					<Input placeholder="e.g. /health returns 200 even when the database is down" maxLength={140} />
				</Form.Item>
				<Form.Item name="description" label="Description">
					<Input.TextArea
						autoSize={{ minRows: 5, maxRows: 14 }}
						placeholder="What is wrong or missing, who is affected, anything Claude should know."
					/>
				</Form.Item>
			</Form>
		</Modal>
	);
}
