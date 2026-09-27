import { Button, Progress, Segmented, Spin, Table } from "antd";
import { CircleAlert, CircleCheck, CircleX, RefreshCw } from "lucide-react";
import { useThemeMode } from "@/app/theme/ThemeMode";
import { useConfig, useDoctor, useStats } from "@/shared/api/system";
import type { Check, StageConfig } from "@/shared/api/types";
import { KVTable, Mono, PageHeader } from "@/shared/components/misc";
import { Card } from "@/shared/components/OverlayDrawer";
import { absTime, usd } from "@/shared/lib/format";
import { toneColor } from "@/shared/lib/status";

const checkIcon = {
	ok: <CircleCheck size={16} color={toneColor.ok} />,
	warn: <CircleAlert size={16} color={toneColor.warn} />,
	fail: <CircleX size={16} color={toneColor.error} />,
};

export default function SettingsPage() {
	const { preference, setPreference } = useThemeMode();
	const config = useConfig();
	const doctor = useDoctor();
	const { data: stats } = useStats();

	return (
		<div style={{ display: "flex", flexDirection: "column", height: "100%" }}>
			<PageHeader title="Settings" />
			<div style={{ flex: 1, overflow: "auto", background: "var(--tf-surface)", padding: 20 }}>
				<div
					style={{
						display: "grid",
						gridTemplateColumns: "minmax(0, 1fr) minmax(0, 1fr)",
						gap: 16,
						alignItems: "start",
					}}
				>
					<div>
						<Card title="Appearance">
							<Segmented
								value={preference}
								onChange={(v) => setPreference(v as typeof preference)}
								options={[
									{ value: "system", label: "System" },
									{ value: "light", label: "Light" },
									{ value: "dark", label: "Dark" },
								]}
							/>
						</Card>
						<Card
							title="Dependencies"
							extra={
								<Button
									size="small"
									icon={<RefreshCw size={13} />}
									loading={doctor.isFetching}
									onClick={() => doctor.refetch()}
								>
									Check again
								</Button>
							}
							padded={false}
						>
							{doctor.isLoading ? (
								<Spin style={{ display: "block", margin: 24 }} />
							) : (
								(doctor.data ?? []).map((c: Check, i) => (
									<div
										key={c.name}
										style={{
											display: "grid",
											gridTemplateColumns: "22px 130px 1fr",
											gap: 8,
											padding: "10px 16px",
											borderTop: i ? "1px solid var(--tf-border)" : undefined,
										}}
									>
										{checkIcon[c.status]}
										<span style={{ fontWeight: 500 }}>{c.name}</span>
										<span>
											{c.detail}
											{c.fix && c.status !== "ok" && <div className="muted">→ {c.fix}</div>}
										</span>
									</div>
								))
							)}
						</Card>
						<Card title="Claude usage">
							{stats?.quota ? (
								<div style={{ display: "grid", gap: 12 }}>
									{Object.entries(stats.quota.windows ?? {}).map(([name, w]) => (
										<div key={name}>
											<div style={{ display: "flex", justifyContent: "space-between", fontSize: 12 }}>
												<span>{name.replace("_", " ")} window</span>
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
									<div className="faint" style={{ fontSize: 12 }}>
										As reported by the last run. Spend today {usd(stats.cost_today_usd)}, last 7 days{" "}
										{usd(stats.cost_7d_usd)}.
									</div>
								</div>
							) : (
								<span className="muted">Shown after the first Claude run.</span>
							)}
						</Card>
					</div>
					<div>
						<Card title="Configuration" padded={false}>
							{config.data && (
								<KVTable
									rows={[
										["Config file", <Mono key="f">{config.data.config_file}</Mono>],
										["Data directory", <Mono key="d">{config.data.data_dir}</Mono>],
										["Port", config.data.port],
										["Concurrent runs", config.data.max_concurrent_runs],
										["PR poll interval", config.data.pr_poll_interval],
										["Version", config.data.version],
									]}
								/>
							)}
						</Card>
						<Card title="Run stages" padded={false}>
							{config.data && (
								<Table<{ kind: string } & StageConfig>
									className="tf-table"
									size="small"
									rowKey="kind"
									pagination={false}
									dataSource={["triage", "define", "plan", "develop", "review", "release"].map((k) => ({
										kind: k,
										...config.data.stages[k],
									}))}
									onRow={() => ({ style: { cursor: "default" } })}
									columns={[
										{
											title: "Stage",
											dataIndex: "kind",
											render: (v: string) => <span style={{ fontWeight: 500 }}>{v}</span>,
										},
										{ title: "Model", dataIndex: "model", render: (v: string) => <Mono>{v}</Mono> },
										{ title: "Effort", dataIndex: "effort" },
										{ title: "Budget", dataIndex: "budget_usd", render: (v: number) => usd(v) },
										{ title: "Timeout", dataIndex: "timeout", render: (v: string) => <Mono faint>{v}</Mono> },
									]}
								/>
							)}
							<div className="faint" style={{ fontSize: 12, padding: "10px 16px" }}>
								Edit the config file to change these; restart thefactory to apply.
							</div>
						</Card>
					</div>
				</div>
			</div>
		</div>
	);
}
