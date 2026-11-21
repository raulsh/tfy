import { AutoComplete, Select } from "antd";
import type { Account, Engine, RunOverride, RunOverrides } from "@/shared/api/types";

// The kinds of run, in pipeline order, and what each one does.
export const runKinds: [string, string][] = [
	["triage", "sorts Slack feedback"],
	["define", "writes the requirement"],
	["plan", "writes the spec"],
	["develop", "changes the code"],
	["review", "reviews the pull requests"],
	["merge", "decides how they merge"],
	["release", "writes the release notes"],
	["learn", "runs the retrospective"],
	["issue", "checks linked issues"],
];

// Per kind of run: the account, model and effort level, over what the less
// specific settings say (shown greyed out). Works as a form field.
export function StageTable({
	value,
	onChange,
	kinds = runKinds,
	accounts,
	engines,
	inherited,
}: {
	value?: RunOverrides;
	onChange?: (v: RunOverrides) => void;
	kinds?: [string, string][];
	// The accounts the runs may choose from, in order.
	accounts: Account[];
	engines: Engine[];
	inherited: (kind: string) => RunOverride;
}) {
	const set = (kind: string, patch: RunOverride) => {
		const row = { ...value?.[kind], ...patch };
		const next = { ...value };
		if (!row.account && !row.model && !row.effort) delete next[kind];
		else next[kind] = row;
		onChange?.(next);
	};
	const name = (id?: string) => accounts.find((a) => a.id === id)?.name ?? id;
	return (
		<div style={{ display: "grid", gridTemplateColumns: "minmax(0, 1fr) 180px 150px 100px", gap: "6px 8px" }}>
			{kinds.map(([kind, what]) => {
				const row = value?.[kind] ?? {};
				const up = inherited(kind);
				const accountId = row.account || up.account;
				const cli = accounts.find((a) => a.id === accountId)?.cli ?? "claude";
				const engine = engines.find((e) => e.name === cli);
				const candidates = accounts.filter((a) => engines.find((e) => e.name === a.cli)?.stages.includes(kind));
				return (
					<div key={kind} style={{ display: "contents" }}>
						<div style={{ alignSelf: "center", minWidth: 0 }}>
							<span style={{ fontWeight: 500 }}>{kind}</span> <span className="faint">{what}</span>
						</div>
						<Select
							size="small"
							allowClear
							value={row.account || undefined}
							placeholder={name(up.account) ?? "first account"}
							options={candidates.map((a) => ({ value: a.id, label: a.name }))}
							onChange={(v?: string) => set(kind, { account: v ?? "" })}
							aria-label={`${kind} account`}
						/>
						<AutoComplete
							size="small"
							allowClear
							value={row.model ?? ""}
							placeholder={up.model || "default model"}
							options={(engine?.models ?? []).map((m) => ({ value: m }))}
							onChange={(v?: string) => set(kind, { model: v ?? "" })}
							aria-label={`${kind} model`}
						/>
						<Select
							size="small"
							allowClear
							value={row.effort || undefined}
							placeholder={up.effort || "effort"}
							options={(engine?.efforts ?? []).map((e) => ({ value: e, label: e }))}
							onChange={(v?: string) => set(kind, { effort: v ?? "" })}
							aria-label={`${kind} effort`}
						/>
					</div>
				);
			})}
		</div>
	);
}
