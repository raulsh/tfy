import { App, Button, InputNumber, Popconfirm, Select, Space, Switch, Tooltip } from "antd";
import { Hash, RefreshCw, Trash2 } from "lucide-react";
import { useState } from "react";
import {
	useAddSource,
	usePollSource,
	useRemoveSource,
	useSlackChannels,
	useSources,
	useUpdateSource,
} from "@/features/inbox/hooks";
import { TimeAgo } from "@/shared/components/misc";
import { Card } from "@/shared/components/OverlayDrawer";

// The Slack channels a project listens to.
export function SlackSources({ projectId }: { projectId: string }) {
	const { data: sources } = useSources(projectId);
	const update = useUpdateSource();
	const remove = useRemoveSource();
	const poll = usePollSource();
	const { message } = App.useApp();
	const onError = (e: Error) => message.error(e.message);

	return (
		<>
			<Card title="Channels" padded={!sources?.length}>
				{!sources?.length ? (
					<span className="muted">
						No channels yet. Every new message in a channel is read (by polling, through slk) and triaged: actionable
						feedback becomes a proposal for you to accept.
					</span>
				) : (
					sources.map((s, i) => (
						<div
							key={s.id}
							style={{
								display: "grid",
								gridTemplateColumns: "1fr auto",
								gap: 8,
								padding: "10px 16px",
								borderTop: i ? "1px solid var(--tf-border)" : undefined,
							}}
						>
							<div>
								<div style={{ fontWeight: 500, display: "flex", alignItems: "center", gap: 4 }}>
									<Hash size={14} />
									{s.channel_name}
								</div>
								<div className="faint" style={{ fontSize: 12 }}>
									polled every {Math.round(s.poll_interval_s / 60)} min · last{" "}
									{s.last_polled_at ? <TimeAgo at={s.last_polled_at} /> : "never"}
								</div>
								{s.last_error && <div style={{ color: "var(--tf-error)", fontSize: 12 }}>{s.last_error}</div>}
							</div>
							<Space size={10}>
								<Tooltip title="Triage every new message automatically">
									<Space size={4}>
										<Switch
											size="small"
											checked={s.auto_triage}
											onChange={(v) => update.mutate({ id: s.id, auto_triage: v }, { onError })}
										/>
										<span style={{ fontSize: 12 }}>triage</span>
									</Space>
								</Tooltip>
								<Tooltip title="Skip messages from bots and apps">
									<Space size={4}>
										<Switch
											size="small"
											checked={s.exclude_bots}
											onChange={(v) => update.mutate({ id: s.id, exclude_bots: v }, { onError })}
										/>
										<span style={{ fontSize: 12 }}>no bots</span>
									</Space>
								</Tooltip>
								<Tooltip title="Poll now">
									<Button
										size="small"
										type="text"
										icon={<RefreshCw size={14} />}
										loading={poll.isPending && poll.variables === s.id}
										onClick={() =>
											poll.mutate(s.id, { onSuccess: (r) => message.info(`${r.new} new message(s)`), onError })
										}
									/>
								</Tooltip>
								<Popconfirm
									title={`Stop listening to #${s.channel_name}?`}
									description="Its messages stay in the inbox."
									onConfirm={() => remove.mutate(s.id, { onError })}
								>
									<Button size="small" type="text" danger icon={<Trash2 size={14} />} />
								</Popconfirm>
							</Space>
						</div>
					))
				)}
			</Card>
			<AddChannel projectId={projectId} linked={(sources ?? []).map((s) => s.channel_id)} />
		</>
	);
}

function AddChannel({ projectId, linked }: { projectId: string; linked: string[] }) {
	const [search, setSearch] = useState("");
	const [channel, setChannel] = useState<{ id: string; name: string }>();
	const [backfill, setBackfill] = useState(0);
	const [triage, setTriage] = useState(true);
	const channels = useSlackChannels(search, true);
	const add = useAddSource(projectId);
	const { message } = App.useApp();
	return (
		<Card title="Listen to a channel">
			<div style={{ display: "grid", gap: 10 }}>
				<Select
					showSearch
					placeholder={channels.error ? (channels.error as Error).message : "Search your channels"}
					filterOption={false}
					onSearch={setSearch}
					loading={channels.isLoading}
					value={channel?.id}
					onChange={(id, opt) => setChannel({ id, name: (opt as { name: string }).name })}
					options={(channels.data ?? [])
						.filter((c) => !linked.includes(c.id))
						.map((c) => ({ value: c.id, label: `#${c.name}`, name: c.name }))}
				/>
				<Space wrap>
					<span>Import the last</span>
					<InputNumber min={0} max={30} value={backfill} onChange={(v) => setBackfill(v ?? 0)} style={{ width: 70 }} />
					<span>days into the inbox (not auto-triaged)</span>
				</Space>
				<Space>
					<Switch checked={triage} onChange={setTriage} />
					<span>Triage new messages automatically</span>
				</Space>
				<div>
					<Button
						type="primary"
						disabled={!channel}
						loading={add.isPending}
						onClick={() =>
							channel &&
							add.mutate(
								{ channel_id: channel.id, channel_name: channel.name, backfill_days: backfill, auto_triage: triage },
								{
									onSuccess: () => {
										message.success(`Listening to #${channel.name}`);
										setChannel(undefined);
									},
									onError: (e) => message.error(e.message),
								},
							)
						}
					>
						Add channel
					</Button>
				</div>
			</div>
		</Card>
	);
}
