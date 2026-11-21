import { Button, Select } from "antd";
import { ArrowDown, ArrowUp, X } from "lucide-react";
import type { Account } from "@/shared/api/types";
import { kindLabels } from "../hooks";

// An ordered list of accounts: the first is the default, the others take
// over, in order, while it is at its usage limit. Works as a form field.
export function AccountOrder({
	value,
	onChange,
	accounts,
	emptyText,
}: {
	value?: string[];
	onChange?: (v: string[]) => void;
	accounts: Account[];
	emptyText?: string;
}) {
	const ids = value ?? [];
	const byId = Object.fromEntries(accounts.map((a) => [a.id, a]));
	const move = (i: number, by: number) => {
		const next = [...ids];
		[next[i], next[i + by]] = [next[i + by], next[i]];
		onChange?.(next);
	};
	const rest = accounts.filter((a) => !ids.includes(a.id));
	return (
		<div>
			{ids.length === 0 && emptyText && (
				<div className="muted" style={{ fontSize: 12.5, marginBottom: 8 }}>
					{emptyText}
				</div>
			)}
			{ids.map((id, i) => (
				<div
					key={id}
					style={{
						display: "grid",
						gridTemplateColumns: "20px minmax(0, 1fr) auto",
						alignItems: "center",
						gap: 8,
						padding: "4px 0",
						borderTop: i ? "1px solid var(--tf-border)" : undefined,
					}}
				>
					<span className="faint" style={{ fontVariantNumeric: "tabular-nums" }}>
						{i + 1}
					</span>
					<span style={{ minWidth: 0 }}>
						<span style={{ fontWeight: 500 }}>{byId[id]?.name ?? id}</span>{" "}
						<span className="faint">
							{byId[id] ? `${byId[id].cli} · ${kindLabels[byId[id].kind] ?? byId[id].kind}` : "no such account"}
							{i === 0 ? " · first choice" : " · takes over at a usage limit"}
						</span>
					</span>
					<span style={{ display: "flex", gap: 2 }}>
						<Button
							size="small"
							type="text"
							icon={<ArrowUp size={13} />}
							disabled={i === 0}
							onClick={() => move(i, -1)}
							aria-label="Move up"
						/>
						<Button
							size="small"
							type="text"
							icon={<ArrowDown size={13} />}
							disabled={i === ids.length - 1}
							onClick={() => move(i, 1)}
							aria-label="Move down"
						/>
						<Button
							size="small"
							type="text"
							icon={<X size={13} />}
							onClick={() => onChange?.(ids.filter((x) => x !== id))}
							aria-label="Remove"
						/>
					</span>
				</div>
			))}
			{rest.length > 0 && (
				<Select
					size="small"
					style={{ width: 260, marginTop: 8 }}
					placeholder="Add an account…"
					value={null}
					options={rest.map((a) => ({ value: a.id, label: `${a.name} (${kindLabels[a.kind] ?? a.kind})` }))}
					onChange={(id: string) => onChange?.([...ids, id])}
				/>
			)}
		</div>
	);
}
