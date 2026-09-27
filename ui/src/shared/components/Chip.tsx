import { X } from "lucide-react";
import type { ReactNode } from "react";

// A split key | value chip: a gray key segment joined to a light value.
export function Chip({
	k,
	v,
	mono,
	onRemove,
	title,
}: {
	k: ReactNode;
	v: ReactNode;
	mono?: boolean;
	onRemove?: () => void;
	title?: string;
}) {
	return (
		<span
			title={title}
			style={{
				display: "inline-flex",
				alignItems: "stretch",
				border: "1px solid var(--tf-border)",
				borderRadius: 4,
				overflow: "hidden",
				fontSize: 12,
				lineHeight: "20px",
				whiteSpace: "nowrap",
				maxWidth: "100%",
			}}
		>
			<span style={{ background: "var(--tf-fill)", color: "var(--tf-text2)", padding: "0 7px" }}>{k}</span>
			<span
				style={{
					background: "var(--tf-bg)",
					color: "var(--tf-text)",
					padding: "0 7px",
					fontFamily: mono ? "var(--tf-mono)" : undefined,
					overflow: "hidden",
					textOverflow: "ellipsis",
				}}
			>
				{v}
			</span>
			{onRemove && (
				<button
					type="button"
					onClick={onRemove}
					aria-label="remove filter"
					style={{
						border: 0,
						borderLeft: "1px solid var(--tf-border)",
						background: "var(--tf-bg)",
						color: "var(--tf-text3)",
						cursor: "pointer",
						padding: "0 5px",
						display: "inline-flex",
						alignItems: "center",
					}}
				>
					<X size={12} />
				</button>
			)}
		</span>
	);
}
