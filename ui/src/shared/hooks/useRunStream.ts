import { useEffect, useRef, useState } from "react";
import { streamURL } from "@/shared/api/client";
import type { RunEvent } from "@/shared/api/types";
import { isRunLive } from "@/shared/lib/status";

interface RunStream {
	events: RunEvent[];
	status: string | null;
	connected: boolean;
}

// Streams a run's events: stored history first, then live events. The
// server numbers events, so reconnects (Last-Event-ID) never duplicate.
export function useRunStream(runId: string | undefined): RunStream {
	const [events, setEvents] = useState<RunEvent[]>([]);
	const [status, setStatus] = useState<string | null>(null);
	const [connected, setConnected] = useState(false);
	const seen = useRef(new Set<number>());

	useEffect(() => {
		setEvents([]);
		setStatus(null);
		seen.current = new Set();
		if (!runId) return;

		const es = new EventSource(streamURL(`/runs/${runId}/stream`));
		let buffer: RunEvent[] = [];
		let frame: number | undefined;
		const flush = () => {
			frame = undefined;
			const batch = buffer;
			buffer = [];
			setEvents((prev) => [...prev, ...batch]);
		};
		es.onopen = () => setConnected(true);
		es.addEventListener("event", (e) => {
			const ev = JSON.parse((e as MessageEvent).data) as RunEvent;
			if (seen.current.has(ev.seq)) return;
			seen.current.add(ev.seq);
			buffer.push(ev);
			if (frame === undefined) frame = requestAnimationFrame(flush);
		});
		es.addEventListener("status", (e) => {
			const { status: s } = JSON.parse((e as MessageEvent).data) as { status: string };
			setStatus(s);
			// The server ends the stream after a final status; do not let the
			// browser reconnect to it.
			if (!isRunLive(s)) {
				es.close();
				setConnected(false);
			}
		});
		es.onerror = () => setConnected(false);
		return () => {
			es.close();
			if (frame !== undefined) cancelAnimationFrame(frame);
		};
	}, [runId]);

	return { events, status, connected };
}
