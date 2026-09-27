import { Segmented } from "antd";
import { useState } from "react";
import { Bar, BarChart, CartesianGrid, ResponsiveContainer, Tooltip, XAxis, YAxis } from "recharts";
import { useThemeMode } from "@/app/theme/ThemeMode";
import type { DailyStat } from "@/shared/api/types";
import { Card } from "@/shared/components/OverlayDrawer";

// Single-series columns (validated with the dataviz validator):
// #1878F6 on the light surface, #4C8FF2 on the dark one.
const barColor = { light: "#1878F6", dark: "#4C8FF2" } as const;

function dayLabel(day: string) {
	const d = new Date(`${day}T00:00:00`);
	return d.toLocaleDateString(undefined, { month: "short", day: "numeric" });
}

// One measure per chart, one day per column, with a table view for anyone
// who prefers (or needs) the numbers.
export function DailyBars({
	title,
	data,
	value,
	format,
	integer = false,
}: {
	title: string;
	data: DailyStat[];
	value: (d: DailyStat) => number;
	format: (n: number) => string;
	// integer: counts get whole-number ticks; amounts keep decimals.
	integer?: boolean;
}) {
	const { mode } = useThemeMode();
	const [view, setView] = useState<"chart" | "table">("chart");
	const rows = data.map((d) => ({ day: dayLabel(d.day), v: value(d) }));
	// Counts get distinct whole-number ticks (0, 1, 2 … at most five).
	const maxCount = Math.max(1, ...rows.map((r) => r.v));
	const step = Math.ceil(maxCount / 4);
	const intTicks = Array.from({ length: Math.floor(maxCount / step) + 1 }, (_, i) => i * step);
	return (
		<Card
			title={title}
			extra={
				<Segmented
					size="small"
					value={view}
					onChange={(v) => setView(v as "chart" | "table")}
					options={[
						{ value: "chart", label: "Chart" },
						{ value: "table", label: "Table" },
					]}
				/>
			}
		>
			{view === "chart" ? (
				<div style={{ height: 200 }}>
					<ResponsiveContainer width="100%" height="100%">
						<BarChart data={rows} margin={{ top: 8, right: 4, bottom: 0, left: 0 }}>
							<CartesianGrid vertical={false} stroke="var(--tf-border)" strokeWidth={1} />
							<XAxis
								dataKey="day"
								tickLine={false}
								axisLine={{ stroke: "var(--tf-border)" }}
								tick={{ fill: "var(--tf-text2)", fontSize: 11 }}
								interval="preserveStartEnd"
								minTickGap={16}
							/>
							<YAxis
								tickLine={false}
								axisLine={false}
								width={48}
								allowDecimals={!integer}
								ticks={integer ? intTicks : undefined}
								domain={integer ? [0, intTicks[intTicks.length - 1]] : [0, "auto"]}
								tick={{ fill: "var(--tf-text2)", fontSize: 11 }}
								tickFormatter={(n: number) => format(n)}
							/>
							<Tooltip
								cursor={{ fill: "var(--tf-hover)" }}
								content={({ active, payload, label }) =>
									active && payload?.length ? (
										<div
											style={{
												background: "var(--tf-bg)",
												border: "1px solid var(--tf-border)",
												borderRadius: 6,
												boxShadow: "var(--tf-shadow)",
												padding: "6px 10px",
												fontSize: 12,
											}}
										>
											<div className="muted">{label}</div>
											<div style={{ fontWeight: 500, fontVariantNumeric: "tabular-nums" }}>
												{format(Number(payload[0].value))}
											</div>
										</div>
									) : null
								}
							/>
							<Bar dataKey="v" fill={barColor[mode]} radius={[4, 4, 0, 0]} maxBarSize={24} />
						</BarChart>
					</ResponsiveContainer>
				</div>
			) : (
				<table style={{ width: "100%", borderCollapse: "collapse", fontSize: 12.5 }}>
					<tbody>
						{rows.map((r) => (
							<tr key={r.day} style={{ borderTop: "1px solid var(--tf-border)" }}>
								<td style={{ padding: "4px 0" }} className="muted">
									{r.day}
								</td>
								<td style={{ padding: "4px 0", textAlign: "right", fontVariantNumeric: "tabular-nums" }}>
									{format(r.v)}
								</td>
							</tr>
						))}
					</tbody>
				</table>
			)}
		</Card>
	);
}
