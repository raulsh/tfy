import { Alert, App, Button, Form, Input, Modal, Popconfirm, Progress, Radio, Space } from "antd";
import { Pencil, Plus, Trash2 } from "lucide-react";
import { useState } from "react";
import type { Account, AccountInput, AccountKind, AccountTest } from "@/shared/api/types";
import { Mono } from "@/shared/components/misc";
import { Card } from "@/shared/components/OverlayDrawer";
import { absTime, usd } from "@/shared/lib/format";
import { toneColor } from "@/shared/lib/status";
import {
	kindLabels,
	useAccounts,
	useCreateAccount,
	useDeleteAccount,
	useResumeAccount,
	useTestAccount,
	useUpdateAccount,
} from "../hooks";

const aliases = ["opus", "sonnet", "haiku", "fable"];

// The accounts runs sign in with: add, change, test and resume them.
export function AccountsCard() {
	const { data: accounts } = useAccounts();
	const [editing, setEditing] = useState<Account | "new" | null>(null);
	return (
		<Card
			title="Accounts"
			extra={
				<Button size="small" icon={<Plus size={13} />} onClick={() => setEditing("new")}>
					Add account
				</Button>
			}
			padded={false}
		>
			{(accounts ?? []).map((a, i) => (
				<AccountRow key={a.id} account={a} first={i === 0} onEdit={() => setEditing(a)} />
			))}
			<div className="faint" style={{ fontSize: 12, padding: "10px 16px", borderTop: "1px solid var(--tf-border)" }}>
				An account is used once a list names it: the global one under Run stages, or a project's.
			</div>
			{editing && <AccountModal account={editing === "new" ? undefined : editing} onClose={() => setEditing(null)} />}
		</Card>
	);
}

function AccountRow({ account: a, first, onEdit }: { account: Account; first: boolean; onEdit: () => void }) {
	const { message } = App.useApp();
	const test = useTestAccount();
	const resume = useResumeAccount();
	const del = useDeleteAccount();
	const [result, setResult] = useState<AccountTest>();
	const run = (probe: boolean) =>
		test.mutate({ id: a.id, probe }, { onSuccess: setResult, onError: (e) => message.error(e.message) });
	return (
		<div style={{ padding: "12px 16px", borderTop: first ? undefined : "1px solid var(--tf-border)" }}>
			<div style={{ display: "flex", alignItems: "baseline", gap: 8, flexWrap: "wrap" }}>
				<span style={{ fontWeight: 500 }}>{a.name}</span>
				<Mono faint>{a.id}</Mono>
				<span className="faint">
					{a.cli} · {kindLabels[a.kind] ?? a.kind}
					{a.kind === "bedrock" && a.settings.region && ` · ${a.settings.region}`}
					{(a.kind === "token" || a.kind === "api_key") && !a.has_secret && " · no secret set"}
				</span>
				<span style={{ marginLeft: "auto", display: "flex", gap: 4 }}>
					<Button size="small" loading={test.isPending} onClick={() => run(false)}>
						Test
					</Button>
					<Button size="small" loading={test.isPending} onClick={() => run(true)} title="A one-word prompt on haiku">
						Send a prompt
					</Button>
					<Button size="small" type="text" icon={<Pencil size={13} />} onClick={onEdit} aria-label="Edit" />
					<Popconfirm
						title="Delete this account?"
						onConfirm={() => del.mutate(a.id, { onError: (e) => message.error(e.message) })}
					>
						<Button size="small" type="text" icon={<Trash2 size={13} />} aria-label="Delete" />
					</Popconfirm>
				</span>
			</div>
			{a.paused_until && (
				<div style={{ marginTop: 6, color: toneColor.warn, fontSize: 12.5 }}>
					At its usage limit until {absTime(a.paused_until)}
					{a.pause_reason && <span className="faint"> · {a.pause_reason}</span>}
				</div>
			)}
			{Object.entries(a.model_blocks ?? {}).map(([model, until]) => (
				<div key={model} style={{ marginTop: 6, color: toneColor.warn, fontSize: 12.5 }}>
					Cannot run <Mono>{model || "its default model"}</Mono> until {absTime(until)}
					<span className="faint"> · its plan needs usage credits for it; other models run</span>
				</div>
			))}
			{(a.paused_until || Object.keys(a.model_blocks ?? {}).length > 0) && (
				<Button
					size="small"
					type="link"
					style={{ padding: 0 }}
					loading={resume.isPending}
					onClick={() => resume.mutate(a.id, { onError: (e) => message.error(e.message) })}
				>
					Resume now
				</Button>
			)}
			{a.quota && (
				<div style={{ display: "grid", gap: 4, marginTop: 8 }}>
					{Object.entries(a.quota.windows ?? {}).map(([name, w]) => (
						<div key={name}>
							<div style={{ display: "flex", justifyContent: "space-between", fontSize: 12 }}>
								<span>{name.replaceAll("_", " ")} window</span>
								<span className="muted">resets {absTime(new Date(w.resetsAt * 1000).toISOString())}</span>
							</div>
							<Progress
								percent={Math.round(w.utilization * 100)}
								size="small"
								strokeColor={
									w.utilization > 0.8 ? toneColor.error : w.utilization > 0.5 ? toneColor.warn : toneColor.ok
								}
							/>
						</div>
					))}
				</div>
			)}
			{result && <TestResult result={result} />}
		</div>
	);
}

function TestResult({ result: r }: { result: AccountTest }) {
	const signedIn = r.loggedIn && !r.error;
	return (
		<div style={{ marginTop: 8, fontSize: 12.5 }}>
			<span style={{ color: signedIn ? toneColor.ok : toneColor.error }}>
				{signedIn ? "Signed in" : "Not signed in"}
			</span>
			{r.apiProvider && <span className="faint"> · provider {r.apiProvider}</span>}
			{r.authMethod && <span className="faint"> · {r.authMethod}</span>}
			{r.subscriptionType && <span className="faint"> · {r.subscriptionType} plan</span>}
			{r.error && <div className="muted">{r.error}</div>}
			{r.probe && (
				<div>
					Prompt {r.probe.status}
					{r.probe.model && <span className="faint"> · {r.probe.model}</span>}
					{r.probe.reply && <span className="faint"> · replied “{r.probe.reply}”</span>}
					<span className="faint"> · {usd(r.probe.cost_usd)}</span>
					{r.probe.reason && <div className="muted">{r.probe.reason}</div>}
				</div>
			)}
		</div>
	);
}

const loopback = ["localhost", "127.0.0.1", "[::1]", "::1"].includes(window.location.hostname);

function AccountModal({ account, onClose }: { account?: Account; onClose: () => void }) {
	const { message } = App.useApp();
	const [form] = Form.useForm();
	const create = useCreateAccount();
	const update = useUpdateAccount(account?.id ?? "");
	const kind: AccountKind = Form.useWatch("kind", form) ?? account?.kind ?? "token";
	const profile: string | undefined = Form.useWatch(["settings", "aws_profile"], form);
	const secretLabel = { token: "Token", api_key: "API key", bedrock: "Bedrock API key", machine: "" }[kind];
	const needsSecret = kind === "token" || kind === "api_key" || (kind === "bedrock" && !profile?.trim());
	const save = (v: AccountInput & { secret?: string }) => {
		const input: AccountInput = { ...v, cli: "claude", kind };
		if (!v.secret) delete input.secret;
		const done = { onSuccess: onClose, onError: (e: Error) => message.error(e.message) };
		if (account) update.mutate(input, done);
		else create.mutate(input, done);
	};
	return (
		<Modal
			open
			title={account ? `Edit ${account.name}` : "Add an account"}
			okText={account ? "Save" : "Add"}
			onOk={() => form.submit()}
			confirmLoading={create.isPending || update.isPending}
			onCancel={onClose}
			destroyOnHidden
		>
			<Form
				form={form}
				layout="vertical"
				initialValues={account ?? { kind: "token", settings: {} }}
				onFinish={save}
				onValuesChange={(changed) => {
					// The id follows the name until set by hand.
					if (!account && "name" in changed && !form.isFieldTouched("id")) {
						form.setFieldValue(
							"id",
							String(changed.name)
								.toLowerCase()
								.replace(/[^a-z0-9]+/g, "-")
								.replace(/^-+|-+$/g, "")
								.slice(0, 40),
						);
					}
				}}
			>
				{!account && (
					<Form.Item name="kind" label="Signs in with">
						<Radio.Group
							optionType="button"
							options={(["token", "bedrock", "api_key", "machine"] as AccountKind[]).map((k) => ({
								value: k,
								label: kindLabels[k],
							}))}
						/>
					</Form.Item>
				)}
				<Form.Item name="name" label="Name" rules={[{ required: true, message: "A name is required" }]}>
					<Input placeholder={kind === "bedrock" ? "Bedrock, us-east-1" : "Work subscription"} />
				</Form.Item>
				{!account && (
					<Form.Item
						name="id"
						label="Id"
						extra="Lowercase letters, digits and dashes; it names the account in the settings."
						rules={[
							{ required: true, pattern: /^[a-z0-9][a-z0-9-]{0,39}$/, message: "Lowercase letters, digits and dashes" },
						]}
					>
						<Input />
					</Form.Item>
				)}
				{kind === "machine" && (
					<Alert
						type="info"
						showIcon
						message="Runs sign in the way the claude command does on this machine: its login, and the provider its settings choose."
						style={{ marginBottom: 16 }}
					/>
				)}
				{kind === "bedrock" && (
					<>
						<Form.Item
							name={["settings", "region"]}
							label="AWS region"
							rules={[{ required: true, message: "A region is required" }]}
						>
							<Input placeholder="us-east-1" />
						</Form.Item>
						<Form.Item
							name={["settings", "aws_profile"]}
							label="AWS profile"
							extra="From ~/.aws on this machine. Leave it empty to use a Bedrock API key instead. A profile allowed only bedrock:InvokeModel* is safest."
						>
							<Input placeholder="bedrock" />
						</Form.Item>
						<Form.Item
							label="Models"
							extra="The Bedrock model each alias runs; empty uses Claude Code's default. Fable refuses on Bedrock: map it to Opus."
						>
							<Space direction="vertical" style={{ width: "100%" }}>
								{aliases.map((alias) => (
									<Form.Item key={alias} name={["settings", "models", alias]} noStyle>
										<Input
											addonBefore={<span style={{ display: "inline-block", width: 48 }}>{alias}</span>}
											placeholder="us.anthropic.claude-…"
										/>
									</Form.Item>
								))}
							</Space>
						</Form.Item>
					</>
				)}
				{kind !== "machine" && (needsSecret || account?.has_secret) && (
					<>
						{kind === "token" && (
							<p className="muted" style={{ fontSize: 12.5 }}>
								Run <Mono>claude setup-token</Mono> in a terminal, sign in with this subscription, and paste the token.
							</p>
						)}
						{!loopback && (
							<Alert
								type="warning"
								showIcon
								style={{ marginBottom: 12 }}
								message="This page is not on localhost: what you type here crosses the network in plain HTTP."
								description={
									<>
										On the server, run{" "}
										<Mono>{`tfy account secret ${form.getFieldValue("id") || account?.id || "<id>"}`}</Mono> instead and
										paste it there.
									</>
								}
							/>
						)}
						<Form.Item
							name="secret"
							label={secretLabel}
							rules={[
								{
									required: needsSecret && !account?.has_secret,
									message: `The ${secretLabel?.toLowerCase()} is required`,
								},
							]}
							extra={account?.has_secret ? "Set. Leave empty to keep it." : undefined}
						>
							<Input.Password autoComplete="off" placeholder={account?.has_secret ? "unchanged" : undefined} />
						</Form.Item>
					</>
				)}
			</Form>
		</Modal>
	);
}
