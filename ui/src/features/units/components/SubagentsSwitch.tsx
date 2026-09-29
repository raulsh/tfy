import { App, Switch, Tooltip } from "antd";
import type { Unit } from "@/shared/api/types";
import { useSetSubagents } from "../hooks";

// Whether the unit's runs may start sub-agents. A change holds from the next
// run on; runs already going keep what they started with.
export function SubagentsSwitch({ unit }: { unit: Unit }) {
	const set = useSetSubagents(unit.id);
	const { message } = App.useApp();
	return (
		<Tooltip title="From the next run on. Runs going now keep what they started with.">
			<span style={{ display: "inline-flex", alignItems: "center", gap: 8 }}>
				<Switch
					size="small"
					checked={unit.subagents}
					loading={set.isPending}
					aria-label="Sub-agents"
					onChange={(on) => set.mutate(on, { onError: (e) => message.error(e.message) })}
				/>
				<span className="faint">{unit.subagents ? "runs may start them" : "runs work alone"}</span>
			</span>
		</Tooltip>
	);
}
