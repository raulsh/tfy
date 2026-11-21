import { App, Button, Form, Spin } from "antd";
import { useEffect } from "react";
import { useUpdateProject } from "@/features/projects/hooks";
import { useConfig } from "@/shared/api/system";
import type { Project, RunSettings } from "@/shared/api/types";
import { Card } from "@/shared/components/OverlayDrawer";
import { useAccounts, useEngines, useRunSettings } from "../hooks";
import { AccountOrder } from "./AccountOrder";
import { StageTable } from "./StageTable";

// A project's accounts, in order of preference, and per stage an account,
// model and effort level, over the global run settings.
export function ProjectAccountsTab({ project }: { project: Project }) {
	const { message } = App.useApp();
	const [form] = Form.useForm<RunSettings>();
	const update = useUpdateProject(project.id);
	const { data: accounts } = useAccounts();
	const { data: engines } = useEngines();
	const { data: global } = useRunSettings();
	const { data: config } = useConfig();
	useEffect(() => form.setFieldsValue(project.settings.runs ?? { accounts: [], stages: {} }), [project, form]);
	const own: string[] = Form.useWatch("accounts", form) ?? project.settings.runs?.accounts ?? [];
	if (!accounts || !engines || !global) return <Spin style={{ display: "block", margin: 24 }} />;
	const listed = own.length ? own : global.accounts;
	const chosen = listed.flatMap((id) => accounts.filter((a) => a.id === id));
	const names = Object.fromEntries(accounts.map((a) => [a.id, a.name]));
	return (
		<Card>
			<Form
				form={form}
				layout="vertical"
				onFinish={(runs) =>
					update.mutate(
						{
							name: project.name,
							description: project.description,
							product_context: project.product_context,
							settings: { ...project.settings, runs: { accounts: runs.accounts ?? [], stages: runs.stages ?? {} } },
						},
						{ onSuccess: () => message.success("Saved"), onError: (e) => message.error(e.message) },
					)
				}
			>
				<Form.Item
					name="accounts"
					label="Accounts"
					extra="The first runs every stage that names none; the others take over, in order, while it is at its usage limit."
				>
					<AccountOrder
						accounts={accounts}
						emptyText={`None of its own: the project uses the global accounts (${global.accounts.map((id) => names[id] ?? id).join(", ")}).`}
					/>
				</Form.Item>
				<Form.Item name="stages" label="Per stage" extra="Empty fields use the global settings, shown greyed out.">
					<StageTable
						accounts={chosen}
						engines={engines}
						inherited={(kind) => {
							const g = global.stages[kind] ?? {};
							return {
								account: g.account && listed.includes(g.account) ? g.account : chosen[0]?.id,
								model: g.model || config?.stages[kind]?.model,
								effort: g.effort || config?.stages[kind]?.effort,
							};
						}}
					/>
				</Form.Item>
				<Button type="primary" htmlType="submit" loading={update.isPending}>
					Save
				</Button>
			</Form>
		</Card>
	);
}
