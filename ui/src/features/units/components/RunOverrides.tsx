import { AutoComplete, Form, Select, Switch } from "antd";
import { ChevronDown, ChevronRight } from "lucide-react";
import { useState } from "react";
import { useConfig } from "@/shared/api/system";
import type { RunOverrides } from "@/shared/api/types";
import { Mono } from "@/shared/components/misc";

// The kinds of run a unit has, in pipeline order, and what each one does.
const runKinds: [string, string][] = [
	["define", "writes the requirement"],
	["plan", "writes the spec"],
	["develop", "changes the code"],
	["review", "reviews the pull requests"],
	["merge", "decides how they merge"],
	["release", "writes the release notes"],
	["learn", "runs the retrospective"],
	["issue", "checks linked issues"],
];
const models = ["fable", "opus", "sonnet", "haiku"].map((m) => ({ value: m }));
const efforts = ["low", "medium", "high", "xhigh", "max"].map((e) => ({ value: e, label: e }));

// Advanced options for a new unit, hidden until asked for: whether its runs
// may start sub-agents, and another model or effort level for some of them.
// What is left empty runs as the config file says.
export function RunOverridesField() {
	const [open, setOpen] = useState(false);
	const { data: config } = useConfig();
	const form = Form.useFormInstance();
	const values: RunOverrides | undefined = Form.useWatch("run_overrides", form);
	const subagents: boolean | undefined = Form.useWatch("subagents", form);
	const changed =
		Object.values(values ?? {}).filter((v) => v?.model?.trim() || v?.effort).length + (subagents === false ? 1 : 0);
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
				Advanced: sub-agents, models and effort
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
					For this unit only. Leave a field empty to use the config file's setting, shown greyed out.
				</p>
				<div style={{ display: "grid", gridTemplateColumns: "minmax(0, 1fr) 150px 110px", gap: "6px 8px" }}>
					{runKinds.map(([kind, what]) => (
						<div key={kind} style={{ display: "contents" }}>
							<div style={{ alignSelf: "center", minWidth: 0 }}>
								<span style={{ fontWeight: 500 }}>{kind}</span> <span className="faint">{what}</span>
							</div>
							<Form.Item name={["run_overrides", kind, "model"]} noStyle>
								<AutoComplete
									size="small"
									options={models}
									placeholder={config?.stages[kind]?.model ?? "model"}
									allowClear
									aria-label={`${kind} model`}
								/>
							</Form.Item>
							<Form.Item name={["run_overrides", kind, "effort"]} noStyle>
								<Select
									size="small"
									options={efforts}
									placeholder={config?.stages[kind]?.effort ?? "effort"}
									allowClear
									aria-label={`${kind} effort`}
								/>
							</Form.Item>
						</div>
					))}
				</div>
			</div>
		</div>
	);
}

// The overrides a unit was created with, one run kind per line.
export function RunOverridesList({ overrides }: { overrides: RunOverrides }) {
	return (
		<div>
			{runKinds
				.filter(([kind]) => overrides[kind])
				.map(([kind]) => (
					<div key={kind}>
						{kind}{" "}
						<Mono faint>
							{[overrides[kind].model, overrides[kind].effort && `${overrides[kind].effort} effort`]
								.filter(Boolean)
								.join(" · ")}
						</Mono>
					</div>
				))}
		</div>
	);
}
