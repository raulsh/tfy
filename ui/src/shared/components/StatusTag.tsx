import type { CSSProperties } from "react";
import { type Tone, toneColor } from "@/shared/lib/status";

export function StatusDot({ tone, pulse, size = 8 }: { tone: Tone; pulse?: boolean; size?: number }) {
	return (
		<span
			className={pulse ? "tf-pulse" : undefined}
			style={{
				display: "inline-block",
				width: size,
				height: size,
				borderRadius: "50%",
				background: toneColor[tone],
				flex: "none",
			}}
		/>
	);
}

// A tinted pill: a dot and a label in the tone's color family.
export function StatusTag({
	tone,
	label,
	pulse,
	style,
}: {
	tone: Tone;
	label: string;
	pulse?: boolean;
	style?: CSSProperties;
}) {
	const color = toneColor[tone];
	return (
		<span
			style={{
				display: "inline-flex",
				alignItems: "center",
				gap: 6,
				padding: "0 8px",
				height: 22,
				borderRadius: 11,
				fontSize: 12,
				fontWeight: 500,
				whiteSpace: "nowrap",
				color: "var(--tf-text)",
				background: `color-mix(in srgb, ${color} 14%, transparent)`,
				border: `1px solid color-mix(in srgb, ${color} 35%, transparent)`,
				...style,
			}}
		>
			<StatusDot tone={tone} pulse={pulse} size={7} />
			{label}
		</span>
	);
}
