import { Bot, GitPullRequest, User, Workflow } from "lucide-react";
import type { Activity } from "@/shared/api/types";
import { TimeAgo } from "@/shared/components/misc";
import { useOpenRun } from "@/shared/hooks/useOpenRun";

function actorIcon(actor: string) {
	switch (actor) {
		case "claude":
			return <Bot size={14} />;
		case "system":
			return <Workflow size={14} />;
		case "github":
			return <GitPullRequest size={14} />;
		default:
			return <User size={14} />;
	}
}

// The unit's timeline, newest first.
export function ActivityList({ items, limit }: { items: Activity[]; limit?: number }) {
	const openRun = useOpenRun();
	const shown = limit ? items.slice(0, limit) : items;
	if (shown.length === 0) return <span className="faint">Nothing yet</span>;
	return (
		<div style={{ display: "grid", gap: 0 }}>
			{shown.map((a) => {
				const runId = typeof a.data?.run_id === "string" ? a.data.run_id : undefined;
				const url = typeof a.data?.url === "string" ? a.data.url : undefined;
				const tone =
					a.kind === "attention"
						? "var(--tf-error)"
						: a.kind === "transition"
							? "var(--tf-accent)"
							: "var(--tf-border)";
				return (
					<div
						key={a.id}
						style={{
							display: "grid",
							gridTemplateColumns: "22px 1fr auto",
							gap: 8,
							padding: "7px 0",
							borderTop: "1px solid var(--tf-border)",
						}}
					>
						<span style={{ color: "var(--tf-text2)", paddingTop: 1 }}>{actorIcon(a.actor)}</span>
						<span style={{ boxShadow: `inset 2px 0 0 ${tone}`, paddingLeft: 8, wordBreak: "break-word" }}>
							{a.message}
							{runId && (
								<>
									{" "}
									<a
										href={`?run=${runId}`}
										onClick={(e) => {
											e.preventDefault();
											openRun(runId);
										}}
									>
										view run
									</a>
								</>
							)}
							{url && (
								<>
									{" "}
									<a href={url} target="_blank" rel="noreferrer">
										open
									</a>
								</>
							)}
						</span>
						<span className="faint" style={{ fontSize: 12 }}>
							<TimeAgo at={a.at} />
						</span>
					</div>
				);
			})}
		</div>
	);
}
