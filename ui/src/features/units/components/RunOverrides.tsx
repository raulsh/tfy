import { App, Button, Form, Modal, Switch } from "antd";
import { ChevronDown, ChevronRight } from "lucide-react";
import { useEffect, useState } from "react";
import { runKinds as allRunKinds, StageTable } from "@/features/accounts/components/StageTable";
import { accountNames, useAccounts, useEngines, useRunSettings } from "@/features/accounts/hooks";
import { useProject } from "@/features/projects/hooks";
import { useConfig } from "@/shared/api/system";
import type { Account, RunOverride, RunOverrides, Unit } from "@/shared/api/types";
import { Mono } from "@/shared/components/misc";
import { absTime } from "@/shared/lib/format";
import { toneColor } from "@/shared/lib/status";
import { useSetRunOverrides, useUnitAction } from "../hooks";

// The kinds of run a unit has: all but triage, which runs before there is
// a unit.
const runKinds = allRunKinds.filter(([kind]) => kind !== "triage");

// The accounts a project's units may run on, in order, and what each kind of
// run uses when the unit sets nothing: the project's settings, then the
// global ones, then the config file.
function useStageDefaults(projectId?: string) {
	const { data: config } = useConfig();
	const { data: accounts } = useAccounts();
	const { data: global } = useRunSettings();
	const { data: project } = useProject(projectId);
	const own = project?.settings.runs;
	const listed = own?.accounts.length ? own.accounts : (global?.accounts ?? []);
	const chosen = listed.flatMap((id) => (accounts ?? []).filter((a) => a.id === id));
	const inherited = (kind: string): RunOverride => {
		const p = own?.stages[kind] ?? {};
		const g = global?.stages[kind] ?? {};
		const account = [p.account, g.account].find((id) => id && listed.includes(id)) ?? chosen[0]?.id;
		return {
			account,
			model: p.model || g.model || config?.stages[kind]?.model,
			effort: p.effort || g.effort || config?.stages[kind]?.effort,
		};
	};
	return { chosen, inherited };
}

// Advanced options for a new unit, hidden until asked for: whether its runs
// may start sub-agents, and another account, model or effort level for some
// of them. What is left empty runs as the project's settings say.
export function RunOverridesField({ projectId }: { projectId?: string }) {
	const [open, setOpen] = useState(false);
	const { data: engines } = useEngines();
	const { chosen, inherited } = useStageDefaults(projectId);
	const form = Form.useFormInstance();
	const values: RunOverrides | undefined = Form.useWatch("run_overrides", form);
	const subagents: boolean | undefined = Form.useWatch("subagents", form);
	const changed =
		Object.values(values ?? {}).filter((v) => v?.account || v?.model?.trim() || v?.effort).length +
		(subagents === false ? 1 : 0);
	return (
		<div>
			<button
				type="button"
				onClick={() => setOpen(!open)}
				style={{
					display: "flex",
					alignItems: "center",
					gap: 6,
					border: 0,
					background: "none",
					padding: 0,
					cursor: "pointer",
					color: "var(--tf-text2)",
					fontFamily: "inherit",
				}}
			>
				{open ? <ChevronDown size={14} /> : <ChevronRight size={14} />}
				Advanced: sub-agents, accounts, models and effort
				{changed > 0 && <span className="faint">· {changed} changed</span>}
			</button>
			{/* Kept mounted while closed, so what was chosen is still sent. */}
			<div hidden={!open} style={{ marginTop: 12 }}>
				<div style={{ display: "flex", alignItems: "center", gap: 8, marginBottom: 14 }}>
					<Form.Item name="subagents" valuePropName="checked" initialValue={true} noStyle>
						<Switch size="small" aria-label="Sub-agents" />
					</Form.Item>
					<span>
						<span style={{ fontWeight: 500 }}>Sub-agents</span>{" "}
						<span className="faint">runs may hand parts of their work to Claude Code sub-agents, in parallel</span>
					</span>
				</div>
				<p className="muted" style={{ marginTop: 0, fontSize: 12.5 }}>
					For this unit only. Leave a field empty to use the project's settings, shown greyed out.
				</p>
				<Unavailable accounts={chosen} />
				<Form.Item name="run_overrides" noStyle>
					<StageTable kinds={runKinds} accounts={chosen} engines={engines ?? []} inherited={inherited} />
				</Form.Item>
			</div>
		</div>
	);
}

// What the accounts cannot run now: those at their usage limit, and the
// models some of them cannot run, such as one whose own limit is used up.
function Unavailable({ accounts }: { accounts: Account[] }) {
	const paused = accounts.filter((a) => a.paused_until);
	const models = new Map<string, string[]>();
	for (const a of accounts) {
		for (const [model, b] of Object.entries(a.model_blocks ?? {})) {
			const why = b.cause === "limit" ? "limit used up" : "needs usage credits";
			models.set(model, [...(models.get(model) ?? []), `${a.name} until ${absTime(b.until)} (${why})`]);
		}
	}
	if (paused.length === 0 && models.size === 0) return null;
	return (
		<div
			style={{
				fontSize: 12.5,
				marginBottom: 12,
				padding: "6px 10px",
				borderRadius: 6,
				background: `color-mix(in srgb, ${toneColor.warn} 8%, var(--tf-bg))`,
				border: `1px solid color-mix(in srgb, ${toneColor.warn} 35%, transparent)`,
			}}
		>
			<div style={{ fontWeight: 500, marginBottom: 2 }}>Not available now</div>
			{[...models].map(([model, where]) => (
				<div key={model}>
					<Mono>{model || "the default model"}</Mono> <span className="muted">on {where.join(", ")}</span>
				</div>
			))}
			{paused.map((a) => (
				<div key={a.id} className="muted">
					{a.name}: every model, until {absTime(a.paused_until)}
				</div>
			))}
		</div>
	);
}

// Changes a unit's own account, model and effort per kind of run, from its
// next run on. focus is the kind of run the unit stopped or waits on: with a
// retry possible, the change can retry it at once.
export function RunSettingsModal({
	unit,
	open,
	focus,
	onClose,
}: {
	unit: Unit;
	open: boolean;
	focus?: string;
	onClose: () => void;
}) {
	const { message } = App.useApp();
	const { data: engines } = useEngines();
	const { chosen, inherited } = useStageDefaults(unit.project_id);
	const save = useSetRunOverrides(unit.id);
	const act = useUnitAction(unit.id);
	const [value, setValue] = useState<RunOverrides>({});
	useEffect(() => {
		if (open) setValue(unit.run_overrides ?? {});
	}, [open, unit.run_overrides]);
	const canRetry = !!focus && !!unit.attention && (unit.actions ?? []).includes("retry");
	const submit = (retry: boolean) =>
		save.mutate(value, {
			onSuccess: () => {
				if (!retry) {
					onClose();
					message.success(
						unit.attention === "waiting" ? "Saved: the waiting run tries again with it" : "Saved for the next runs",
					);
					return;
				}
				act.mutate(
					{ action: "retry" },
					{
						onSuccess: () => {
							onClose();
							message.success(`Retrying the ${focus} run`);
						},
						onError: (e) => message.error(e.message),
					},
				);
			},
			onError: (e) => message.error(e.message),
		});
	return (
		<Modal
			title="Accounts and models for this unit"
			open={open}
			width={760}
			onCancel={onClose}
			footer={[
				<Button key="cancel" onClick={onClose}>
					Cancel
				</Button>,
				<Button
					key="save"
					type={canRetry ? "default" : "primary"}
					loading={save.isPending}
					onClick={() => submit(false)}
				>
					Save
				</Button>,
				...(canRetry
					? [
							<Button key="retry" type="primary" loading={save.isPending || act.isPending} onClick={() => submit(true)}>
								Save and retry {focus}
							</Button>,
						]
					: []),
			]}
		>
			<p className="muted" style={{ marginTop: 0, fontSize: 12.5 }}>
				From the next run on; runs going now keep theirs. Leave a field empty to use the project's settings, shown
				greyed out.
			</p>
			<Unavailable accounts={chosen} />
			<StageTable
				kinds={runKinds}
				accounts={chosen}
				engines={engines ?? []}
				inherited={inherited}
				highlight={focus}
				value={value}
				onChange={setValue}
			/>
		</Modal>
	);
}

// The unit's own choices, with a way to change them.
export function RunOverridesDetail({ unit }: { unit: Unit }) {
	const [open, setOpen] = useState(false);
	const overrides = unit.run_overrides ?? {};
	return (
		<div style={{ display: "flex", flexDirection: "column", alignItems: "flex-start", gap: 2 }}>
			{Object.keys(overrides).length > 0 ? (
				<RunOverridesList overrides={overrides} />
			) : (
				<span className="faint">as the project's settings say</span>
			)}
			<Button size="small" type="link" style={{ padding: 0, height: "auto" }} onClick={() => setOpen(true)}>
				Change
			</Button>
			<RunSettingsModal unit={unit} open={open} onClose={() => setOpen(false)} />
		</div>
	);
}

// The unit's own choices, one run kind per line.
export function RunOverridesList({ overrides }: { overrides: RunOverrides }) {
	const { data: accounts } = useAccounts();
	const names = accountNames(accounts);
	return (
		<div>
			{runKinds
				.filter(([kind]) => overrides[kind])
				.map(([kind]) => (
					<div key={kind}>
						{kind}{" "}
						<Mono faint>
							{[
								overrides[kind].account && (names[overrides[kind].account ?? ""] ?? overrides[kind].account),
								overrides[kind].model,
								overrides[kind].effort && `${overrides[kind].effort} effort`,
							]
								.filter(Boolean)
								.join(" · ")}
						</Mono>
					</div>
				))}
		</div>
	);
}
