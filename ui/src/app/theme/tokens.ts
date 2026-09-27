// Design tokens, after groundcover's UI: a calm, light-first palette where
// color only ever signals status. Values approximated from groundcover's CSS.

export type Mode = "light" | "dark";

export interface Palette {
	bg: string; // page and table background
	surface: string; // behind cards (drawer bodies)
	hover: string;
	fill: string; // subtle fills: key segments, active nav pill
	border: string;
	text: string;
	text2: string;
	text3: string;
	accent: string;
	selected: string;
	shadow: string;
}

export const palettes: Record<Mode, Palette> = {
	light: {
		bg: "#FFFFFF",
		surface: "#F5F6F8",
		hover: "#F9FAFB",
		fill: "#EFF2F5",
		border: "#EAECF0",
		text: "#101828",
		text2: "#475467",
		text3: "#98A2B3",
		accent: "#1878F6",
		selected: "#F0F6FE",
		shadow: "0 1px 2px rgba(16, 24, 40, 0.06), 0 1px 3px rgba(16, 24, 40, 0.08)",
	},
	dark: {
		bg: "#1D2125",
		surface: "#16191C",
		hover: "#22272B",
		fill: "#2C333A",
		border: "#38414A",
		text: "#DEE4EA",
		text2: "#9FADBC",
		text3: "#738496",
		accent: "#7FB4FA",
		selected: "#04316C",
		shadow: "0 1px 2px rgba(0, 0, 0, 0.4)",
	},
};

// Status colors, from the groundcover brand.
export const status = {
	ok: "#319B6A",
	warn: "#F6C73C",
	amber: "#F59E0D",
	info: "#7FB4FA",
	accent: "#1878F6",
	error: "#FF6161",
	neutral: "#98A2B3",
} as const;

// Pastel colors per tool, for run waterfalls.
export const toolColors: Record<string, string> = {
	Bash: "#F7C77E",
	Monitor: "#F7C77E",
	Read: "#9FD8C4",
	Edit: "#C8B6F0",
	Write: "#C8B6F0",
	NotebookEdit: "#C8B6F0",
	WebFetch: "#F2A7B8",
	WebSearch: "#F2A7B8",
	StructuredOutput: "#A9C8F5",
	Task: "#E8D48A",
};

export const fonts = {
	sans: "'IBM Plex Sans', system-ui, -apple-system, 'Segoe UI', sans-serif",
	mono: "'IBM Plex Mono', ui-monospace, SFMono-Regular, Menlo, monospace",
};
