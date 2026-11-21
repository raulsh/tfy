import { Button, Segmented, Spin } from "antd";
import { CircleAlert, CircleCheck, CircleX, RefreshCw } from "lucide-react";
import { useThemeMode } from "@/app/theme/ThemeMode";
import { AccountsCard } from "@/features/accounts/components/AccountsCard";
import { RunSettingsCard } from "@/features/accounts/components/RunSettingsCard";
import { useConfig, useDoctor } from "@/shared/api/system";
import type { Check } from "@/shared/api/types";
import { KVTable, Mono, PageHeader } from "@/shared/components/misc";
import { Card } from "@/shared/components/OverlayDrawer";
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
						<AccountsCard />
					</div>
					<div>
						<Card title="Configuration" padded={false}>
							{config.data && (
								<KVTable
									rows={[
										["Config file", <Mono key="f">{config.data.config_file}</Mono>],
										["Data directory", <Mono key="d">{config.data.data_dir}</Mono>],
										["Address", <Mono key="a">{`${config.data.host}:${config.data.port}`}</Mono>],
										["Concurrent runs", config.data.max_concurrent_runs],
										["PR poll interval", config.data.pr_poll_interval],
										["Version", config.data.version],
									]}
								/>
							)}
						</Card>
						<RunSettingsCard config={config.data} />
					</div>
				</div>
			</div>
		</div>
	);
}
