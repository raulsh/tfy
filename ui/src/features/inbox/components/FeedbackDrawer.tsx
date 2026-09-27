import { App, Button, Spin } from "antd";
import { ArrowUpRight, ExternalLink } from "lucide-react";
import { useNavigate } from "react-router-dom";
import { Chip } from "@/shared/components/Chip";
import { KVTable, TimeAgo } from "@/shared/components/misc";
import { Card, OverlayDrawer } from "@/shared/components/OverlayDrawer";
import { StatusTag } from "@/shared/components/StatusTag";
import { kindLabel, toneColor } from "@/shared/lib/status";
import { useFeedback, useSetFeedbackStatus, useThread } from "../hooks";
import { feedbackLabel, feedbackTone } from "../status";

// One Slack message: its thread, how triage judged it, and where it went.
export function FeedbackDrawer({
	feedbackId,
	onClose,
	onPrev,
	onNext,
}: {
	feedbackId?: string;
	onClose: () => void;
	onPrev?: () => void;
	onNext?: () => void;
}) {
	const { data: all } = useFeedback();
	const fb = all?.find((f) => f.id === feedbackId);
	const thread = useThread(feedbackId);
	const setStatus = useSetFeedbackStatus();
	const navigate = useNavigate();
	const { message } = App.useApp();

	return (
		<OverlayDrawer
			open={!!feedbackId}
			onClose={onClose}
			onPrev={onPrev}
			onNext={onNext}
			width="min(860px, 60vw)"
			stripe={fb ? toneColor[feedbackTone[fb.status]] : undefined}
			title={fb ? `${fb.author} in #${fb.channel_name}` : "…"}
			chips={
				fb && (
					<>
						<StatusTag tone={feedbackTone[fb.status]} label={feedbackLabel[fb.status]} />
						<Chip k="posted" v={<TimeAgo at={fb.posted_at} />} />
						{fb.triage?.kind && <Chip k="kind" v={kindLabel[fb.triage.kind] ?? fb.triage.kind} />}
						{fb.triage?.confidence != null && (
							<Chip k="confidence" v={`${Math.round(fb.triage.confidence * 100)}%`} mono />
						)}
					</>
				)
			}
			actions={
				fb && (
					<>
						{fb.permalink && (
							<Button size="small" icon={<ExternalLink size={13} />} href={fb.permalink} target="_blank">
								Slack
							</Button>
						)}
						{fb.unit_id && (
							<Button size="small" icon={<ArrowUpRight size={14} />} onClick={() => navigate(`/units/${fb.unit_id}`)}>
								{fb.unit_label}
							</Button>
						)}
						{!fb.unit_id && fb.status !== "dismissed" && (
							<Button
								size="small"
								loading={setStatus.isPending}
								onClick={() =>
									setStatus.mutate({ id: fb.id, status: "dismissed" }, { onError: (e) => message.error(e.message) })
								}
							>
								Dismiss
							</Button>
						)}
						{!fb.unit_id && fb.status === "dismissed" && (
							<Button
								size="small"
								onClick={() =>
									setStatus.mutate({ id: fb.id, status: "inbox" }, { onError: (e) => message.error(e.message) })
								}
							>
								Restore
							</Button>
						)}
					</>
				)
			}
		>
			{!fb ? (
				<Spin style={{ display: "block", margin: 48 }} />
			) : (
				<>
					<Card title="Conversation" padded={false}>
						{(thread.data ?? [fb]).map((m, i) => (
							<div
								key={m.id}
								style={{
									padding: "10px 16px",
									borderTop: i ? "1px solid var(--tf-border)" : undefined,
									background: m.id === fb.id ? "var(--tf-selected)" : undefined,
									paddingLeft: m.thread_ts ? 32 : 16,
								}}
							>
								<div style={{ fontSize: 12, marginBottom: 2 }}>
									<b>{m.author}</b>{" "}
									<span className="faint">
										· <TimeAgo at={m.posted_at} />
									</span>
								</div>
								<div style={{ whiteSpace: "pre-wrap", lineHeight: 1.55 }}>{m.text}</div>
							</div>
						))}
					</Card>
					{fb.triage?.verdict && (
						<Card title="Triage" padded={false}>
							<KVTable
								rows={[
									["Verdict", fb.triage.verdict],
									["Reason", fb.triage.reason ?? "—"],
									...(fb.triage.title ? ([["Proposed title", fb.triage.title]] as [string, string][]) : []),
									...(fb.triage.summary ? ([["Summary", fb.triage.summary]] as [string, string][]) : []),
									...(fb.triage.unit ? ([["Attached to", fb.triage.unit]] as [string, string][]) : []),
								]}
							/>
						</Card>
					)}
					{!fb.triage?.verdict && fb.triage?.reason && <Card title="Triage">{fb.triage.reason}</Card>}
				</>
			)}
		</OverlayDrawer>
	);
}
