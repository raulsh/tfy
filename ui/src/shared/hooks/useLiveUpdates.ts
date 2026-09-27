import { useQueryClient } from "@tanstack/react-query";
import { useEffect, useState } from "react";
import { streamURL } from "@/shared/api/client";

export type LiveState = "connecting" | "live" | "offline";

// Subscribes to /events and invalidates the queries an entity change touches.
// Invalidations are batched so a burst of changes refetches once.
export function useLiveUpdates(): LiveState {
	const qc = useQueryClient();
	const [state, setState] = useState<LiveState>("connecting");

	useEffect(() => {
		const pending = new Set<string>();
		let timer: number | undefined;
		const flush = () => {
			timer = undefined;
			for (const key of pending) {
				const [kind, id] = key.split(":");
				switch (kind) {
					case "unit":
						qc.invalidateQueries({ queryKey: ["units"] });
						qc.invalidateQueries({ queryKey: ["unit", id] });
						qc.invalidateQueries({ queryKey: ["doc", id] });
						qc.invalidateQueries({ queryKey: ["stats"] });
						break;
					case "run":
						qc.invalidateQueries({ queryKey: ["runs"] });
						qc.invalidateQueries({ queryKey: ["run", id] });
						qc.invalidateQueries({ queryKey: ["unit"] });
						qc.invalidateQueries({ queryKey: ["stats"] });
						break;
					case "feedback":
						qc.invalidateQueries({ queryKey: ["feedback"] });
						qc.invalidateQueries({ queryKey: ["units"] });
						qc.invalidateQueries({ queryKey: ["stats"] });
						break;
					case "project":
						qc.invalidateQueries({ queryKey: ["projects"] });
						qc.invalidateQueries({ queryKey: ["project", id] });
						break;
					default:
						qc.invalidateQueries();
				}
			}
			pending.clear();
		};

		const es = new EventSource(streamURL("/events"));
		es.addEventListener("hello", () => setState("live"));
		es.addEventListener("change", (e) => {
			const { kind, id } = JSON.parse((e as MessageEvent).data) as { kind: string; id: string };
			pending.add(`${kind}:${id}`);
			if (timer === undefined) timer = window.setTimeout(flush, 200);
		});
		es.onerror = () => setState(es.readyState === EventSource.CLOSED ? "offline" : "connecting");
		es.onopen = () => {
			setState("live");
			// Anything may have changed while disconnected.
			qc.invalidateQueries();
		};
		return () => {
			es.close();
			if (timer !== undefined) window.clearTimeout(timer);
		};
	}, [qc]);

	return state;
}
