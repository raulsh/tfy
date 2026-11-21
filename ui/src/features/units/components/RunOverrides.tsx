import { Form, Switch } from "antd";
import { ChevronDown, ChevronRight } from "lucide-react";
import { useState } from "react";
import { runKinds as allRunKinds, StageTable } from "@/features/accounts/components/StageTable";
import { accountNames, useAccounts, useEngines, useRunSettings } from "@/features/accounts/hooks";
import { useProject } from "@/features/projects/hooks";
import { useConfig } from "@/shared/api/system";
import type { RunOverrides } from "@/shared/api/types";
import { Mono } from "@/shared/components/misc";

// The kinds of run a unit has: all but triage, which runs before there is
// a unit.
const runKinds = allRunKinds.filter(([kind]) => kind !== "triage");

// Advanced options for a new unit, hidden until asked for: whether its runs
// may start sub-agents, and another account, model or effort level for some
// of them. What is left empty runs as the project's settings say.
export function RunOverridesField({ projectId }: { projectId?: string }) {
	const [open, setOpen] = useState(false);
	const { data: config } = useConfig();
	const { data: accounts } = useAccounts();
	const { data: engines } = useEngines();
	const { data: global } = useRunSettings();
	const { data: project } = useProject(projectId);
	const form = Form.useFormInstance();
	const values: RunOverrides | undefined = Form.useWatch("run_overrides", form);
	const subagents: boolean | undefined = Form.useWatch("subagents", form);
	const changed =
		Object.values(values ?? {}).filter((v) => v?.account || v?.model?.trim() || v?.effort).length +
		(subagents === false ? 1 : 0);
	const own = project?.settings.runs;
	const listed = own?.accounts.length ? own.accounts : (global?.accounts ?? []);
	const chosen = listed.flatMap((id) => (accounts ?? []).filter((a) => a.id === id));
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
				<Form.Item name="run_overrides" noStyle>
					<StageTable
						kinds={runKinds}
						accounts={chosen}
						engines={engines ?? []}
						inherited={(kind) => {
							const p = own?.stages[kind] ?? {};
							const g = global?.stages[kind] ?? {};
							const account = [p.account, g.account].find((id) => id && listed.includes(id)) ?? chosen[0]?.id;
							return {
								account,
								model: p.model || g.model || config?.stages[kind]?.model,
								effort: p.effort || g.effort || config?.stages[kind]?.effort,
							};
						}}
					/>
				</Form.Item>
			</div>
		</div>
	);
}

// The overrides a unit was created with, one run kind per line.
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
