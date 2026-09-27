import { Link } from "react-router-dom";
import { useScope } from "@/app/scope";
import { status } from "@/app/theme/tokens";
import { StageFunnel } from "@/features/units/components/parts";
import { useDailyStats, useRecentActivity, useStats } from "@/shared/api/system";
import { Metric, Mono, PageHeader, TimeAgo } from "@/shared/components/misc";
import { Card } from "@/shared/components/OverlayDrawer";
import { usd } from "@/shared/lib/format";
import { DailyBars } from "../components/DailyBars";

// The factory at a glance: what waits for you, what runs, what it costs,
// what shipped.
export default function OverviewPage() {
	const { projectId } = useScope();
	const { data: stats } = useStats(projectId);
	const { data: daily } = useDailyStats(14);
	const { data: activity } = useRecentActivity();
	const shipped = (daily ?? []).reduce((s, d) => s + d.done, 0);
	const feedback = stats?.feedback_by_status ?? {};
	const toReview = (feedback.uncertain ?? 0) + (feedback.inbox ?? 0);

	return (
		<div style={{ display: "flex", flexDirection: "column", height: "100%" }}>
			<PageHeader title="Overview" />
			<div style={{ flex: 1, overflow: "auto", background: "var(--tf-surface)", padding: 20 }}>
				<div style={{ display: "flex", gap: 10, flexWrap: "wrap", marginBottom: 16 }}>
					<Metric
						label="Needs you"
						value={stats?.waiting_on_you ?? "—"}
						hint={stats?.needs_attention ? `${stats.needs_attention} stopped` : "reviews and merges"}
						tone={stats?.needs_attention ? status.error : stats?.waiting_on_you ? status.warn : undefined}
					/>
					<Metric
						label="Live runs"
						value={stats?.live_runs ?? "—"}
						tone={stats?.live_runs ? status.accent : undefined}
					/>
					<Metric label="Inbox to review" value={toReview} hint={`${feedback.proposal ?? 0} proposals`} />
					<Metric label="Shipped, 14 days" value={shipped} />
					<Metric label="Spend today" value={usd(stats?.cost_today_usd)} hint={`7 days ${usd(stats?.cost_7d_usd)}`} />
					{stats?.paused_until && (
						<Metric
							label="Claude paused until"
							value={new Date(stats.paused_until).toLocaleTimeString()}
							tone={status.warn}
						/>
					)}
				</div>
				<Card title="Pipeline">
					<StageFunnel counts={stats?.units_by_stage ?? {}} selected={[]} onSelect={() => {}} />
				</Card>
				<div style={{ display: "grid", gridTemplateColumns: "repeat(auto-fit, minmax(380px, 1fr))", gap: 16 }}>
					<DailyBars title="Claude spend per day" data={daily ?? []} value={(d) => d.cost_usd} format={(n) => usd(n)} />
					<DailyBars
						title="Units shipped per day"
						data={daily ?? []}
						value={(d) => d.done}
						format={(n) => String(Math.round(n))}
						integer
					/>
				</div>
				<Card title="Recent activity" padded={false}>
					{(activity ?? []).map((a, i) => (
						<div
							key={a.id}
							style={{
								display: "grid",
								gridTemplateColumns: "64px minmax(0, 260px) 1fr 110px",
								gap: 10,
								padding: "8px 16px",
								borderTop: i ? "1px solid var(--tf-border)" : undefined,
								alignItems: "baseline",
							}}
						>
							<Mono faint>{a.unit_label}</Mono>
							<Link
								to={a.unit_id ? `/units/${a.unit_id}` : "/units"}
								style={{ color: "var(--tf-text)", overflow: "hidden", textOverflow: "ellipsis", whiteSpace: "nowrap" }}
							>
								{a.unit_title}
							</Link>
							<span className="muted" style={{ overflow: "hidden", textOverflow: "ellipsis", whiteSpace: "nowrap" }}>
								{a.message}
							</span>
							<span className="faint" style={{ fontSize: 12, textAlign: "right" }}>
								<TimeAgo at={a.at} />
							</span>
						</div>
					))}
					{activity?.length === 0 && (
						<div style={{ padding: 16 }} className="muted">
							Nothing has happened yet.
						</div>
					)}
				</Card>
			</div>
		</div>
	);
}
