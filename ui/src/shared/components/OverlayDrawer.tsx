import { Button, Drawer, Tooltip } from "antd";
import { ArrowDown, ArrowUp, X } from "lucide-react";
import { type ReactNode, useEffect } from "react";

// The right-hand detail drawer: a status-colored top stripe, the title with
// chips beneath it, ↑/↓ to step through records, and a gray body for cards.
export function OverlayDrawer({
	open,
	onClose,
	stripe,
	title,
	chips,
	actions,
	onPrev,
	onNext,
	width = "min(1180px, 74vw)",
	children,
}: {
	open: boolean;
	onClose: () => void;
	stripe?: string;
	title: ReactNode;
	chips?: ReactNode;
	actions?: ReactNode;
	onPrev?: () => void;
	onNext?: () => void;
	width?: string | number;
	children: ReactNode;
}) {
	useEffect(() => {
		if (!open) return;
		const onKey = (e: KeyboardEvent) => {
			const el = e.target as HTMLElement;
			if (["INPUT", "TEXTAREA"].includes(el.tagName)) return;
			if ((e.key === "ArrowUp" || e.key === "k") && onPrev) {
				e.preventDefault();
				onPrev();
			}
			if ((e.key === "ArrowDown" || e.key === "j") && onNext) {
				e.preventDefault();
				onNext();
			}
		};
		window.addEventListener("keydown", onKey);
		return () => window.removeEventListener("keydown", onKey);
	}, [open, onPrev, onNext]);

	return (
		<Drawer
			open={open}
			onClose={onClose}
			width={width}
			closable={false}
			destroyOnHidden
			styles={{ header: { display: "none" }, body: { padding: 0, display: "flex", flexDirection: "column" } }}
		>
			<div style={{ height: 3, background: stripe ?? "var(--tf-border)", flex: "none" }} />
			<div style={{ padding: "16px 24px 14px", borderBottom: "1px solid var(--tf-border)", flex: "none" }}>
				<div style={{ display: "flex", alignItems: "flex-start", gap: 12 }}>
					<div style={{ flex: 1, minWidth: 0, fontSize: 18, fontWeight: 500, lineHeight: 1.35 }}>{title}</div>
					<div style={{ display: "flex", alignItems: "center", gap: 4, flex: "none" }}>
						{actions}
						{(onPrev || onNext) && (
							<>
								<Tooltip title="Previous (↑)">
									<Button type="text" size="small" icon={<ArrowUp size={16} />} disabled={!onPrev} onClick={onPrev} />
								</Tooltip>
								<Tooltip title="Next (↓)">
									<Button type="text" size="small" icon={<ArrowDown size={16} />} disabled={!onNext} onClick={onNext} />
								</Tooltip>
							</>
						)}
						<Button type="text" size="small" icon={<X size={16} />} onClick={onClose} aria-label="close" />
					</div>
				</div>
				{chips && <div style={{ display: "flex", flexWrap: "wrap", gap: 6, marginTop: 10 }}>{chips}</div>}
			</div>
			<div style={{ flex: 1, overflow: "auto", background: "var(--tf-surface)", padding: 20 }}>{children}</div>
		</Drawer>
	);
}

// Card is a white panel on the drawer's gray body.
export function Card({
	title,
	extra,
	children,
	padded = true,
}: {
	title?: ReactNode;
	extra?: ReactNode;
	children: ReactNode;
	padded?: boolean;
}) {
	return (
		<div
			style={{
				background: "var(--tf-bg)",
				border: "1px solid var(--tf-border)",
				borderRadius: 8,
				marginBottom: 16,
				overflow: "hidden",
			}}
		>
			{(title || extra) && (
				<div
					style={{
						display: "flex",
						alignItems: "center",
						justifyContent: "space-between",
						gap: 12,
						padding: "12px 16px",
						borderBottom: "1px solid var(--tf-border)",
					}}
				>
					<div style={{ fontWeight: 500, fontSize: 14 }}>{title}</div>
					{extra}
				</div>
			)}
			<div style={{ padding: padded ? 16 : 0 }}>{children}</div>
		</div>
	);
}
