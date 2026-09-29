import dayjs from "dayjs";
import relativeTime from "dayjs/plugin/relativeTime";

dayjs.extend(relativeTime);

export function timeAgo(iso: string | null | undefined): string {
	if (!iso) return "—";
	return dayjs(iso).fromNow();
}

export function absTime(iso: string | null | undefined): string {
	if (!iso) return "";
	return dayjs(iso).format("YYYY-MM-DD HH:mm:ss");
}

export function duration(ms: number | null | undefined): string {
	if (ms == null || ms < 0) return "—";
	if (ms < 1000) return `${ms}ms`;
	const s = ms / 1000;
	if (s < 60) return `${s.toFixed(s < 10 ? 1 : 0)}s`;
	const m = Math.floor(s / 60);
	const rs = Math.round(s % 60);
	if (m < 60) return `${m}m ${rs}s`;
	return `${Math.floor(m / 60)}h ${m % 60}m`;
}

export function usd(n: number | null | undefined): string {
	if (!n) return "$0";
	if (n < 0.01) return `$${n.toFixed(4)}`;
	return `$${n.toFixed(2)}`;
}

export function shortSha(sha: string | null | undefined): string {
	return sha ? sha.slice(0, 7) : "";
}

export function shortId(id: string | null | undefined): string {
	return id ? id.slice(-8) : "";
}

export function humanize(s: string): string {
	return s.replace(/[_-]/g, " ");
}

export function compact(n: number): string {
	if (n < 1000) return String(n);
	if (n < 1_000_000) return `${(n / 1000).toFixed(n < 10_000 ? 1 : 0)}k`;
	return `${(n / 1_000_000).toFixed(1)}M`;
}

export function bytes(n: number): string {
	if (n < 1024) return `${n} B`;
	if (n < 1024 * 1024) return `${(n / 1024).toFixed(n < 10 * 1024 ? 1 : 0)} KB`;
	return `${(n / (1024 * 1024)).toFixed(1)} MB`;
}
