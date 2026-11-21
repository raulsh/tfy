import { App, Button, Form, Spin } from "antd";
import { useEffect } from "react";
import type { ConfigView, RunSettings } from "@/shared/api/types";
import { Mono } from "@/shared/components/misc";
import { Card } from "@/shared/components/OverlayDrawer";
import { usd } from "@/shared/lib/format";
import { useAccounts, useEngines, useRunSettings, useSetRunSettings } from "../hooks";
import { AccountOrder } from "./AccountOrder";
import { runKinds, StageTable } from "./StageTable";

// The run settings of projects that have none: their accounts in order of
// preference, and per stage an account, model and effort level. Budgets and
// time limits stay in the config file.
export function RunSettingsCard({ config }: { config?: ConfigView }) {
	const { message } = App.useApp();
	const [form] = Form.useForm<RunSettings>();
	const { data: settings } = useRunSettings();
	const { data: accounts } = useAccounts();
	const { data: engines } = useEngines();
	const save = useSetRunSettings();
	useEffect(() => {
		if (settings) form.setFieldsValue(settings);
	}, [settings, form]);
	const listed: string[] = Form.useWatch("accounts", form) ?? settings?.accounts ?? [];
	const chosen = listed.flatMap((id) => (accounts ?? []).filter((a) => a.id === id));
	if (!settings || !accounts || !engines) return <Spin style={{ display: "block", margin: 24 }} />;
	return (
		<Card
			title="Run stages"
			extra={
				<Button size="small" type="primary" loading={save.isPending} onClick={() => form.submit()}>
					Save
				</Button>
			}
		>
			<Form
				form={form}
				layout="vertical"
				onFinish={(v) =>
					save.mutate(v, { onSuccess: () => message.success("Saved"), onError: (e) => message.error(e.message) })
				}
			>
				<Form.Item
					name="accounts"
					label="Accounts"
					extra="Projects without their own list use these. The first runs every stage that names none; the others take over, in order, while it is at its usage limit."
				>
					<AccountOrder accounts={accounts} />
				</Form.Item>
				<Form.Item
					name="stages"
					label="Per stage"
					extra="Empty fields use the first account, and the config file's model and effort."
				>
					<StageTable
						accounts={chosen}
						engines={engines}
						inherited={(kind) => ({
							account: chosen[0]?.id,
							model: config?.stages[kind]?.model,
							effort: config?.stages[kind]?.effort,
						})}
					/>
				</Form.Item>
			</Form>
			{config && (
				<div className="faint" style={{ fontSize: 12 }}>
					Budgets and time limits, from <Mono faint>{config.config_file}</Mono> (restart tfy after editing it):{" "}
					{runKinds
						.map(([kind]) => `${kind} ${usd(config.stages[kind]?.budget_usd)} / ${config.stages[kind]?.timeout ?? "—"}`)
						.join(" · ")}
				</div>
			)}
		</Card>
	);
}
