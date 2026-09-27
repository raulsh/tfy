import { createContext, type ReactNode, useContext, useEffect, useMemo, useState } from "react";
import type { Mode } from "./tokens";

export type ModePreference = Mode | "system";

interface ThemeModeValue {
	preference: ModePreference;
	mode: Mode;
	setPreference: (p: ModePreference) => void;
}

const ThemeModeContext = createContext<ThemeModeValue | null>(null);
const STORAGE_KEY = "tfy-theme";

function readPreference(): ModePreference {
	// ?theme=dark|light|system picks the theme, e.g. for screenshots.
	const fromURL = new URLSearchParams(window.location.search).get("theme");
	if (fromURL === "light" || fromURL === "dark" || fromURL === "system") return fromURL;
	try {
		const v = localStorage.getItem(STORAGE_KEY);
		if (v === "light" || v === "dark" || v === "system") return v;
	} catch {
		// storage unavailable: fall back to the system preference
	}
	return "system";
}

function systemMode(): Mode {
	return window.matchMedia?.("(prefers-color-scheme: dark)").matches ? "dark" : "light";
}

export function ThemeModeProvider({ children }: { children: ReactNode }) {
	const [preference, setPreferenceState] = useState<ModePreference>(readPreference);
	const [system, setSystem] = useState<Mode>(systemMode);

	useEffect(() => {
		const mq = window.matchMedia("(prefers-color-scheme: dark)");
		const onChange = () => setSystem(mq.matches ? "dark" : "light");
		mq.addEventListener("change", onChange);
		return () => mq.removeEventListener("change", onChange);
	}, []);

	const mode = preference === "system" ? system : preference;

	useEffect(() => {
		document.documentElement.dataset.theme = mode;
	}, [mode]);

	const value = useMemo<ThemeModeValue>(
		() => ({
			preference,
			mode,
			setPreference: (p) => {
				setPreferenceState(p);
				try {
					localStorage.setItem(STORAGE_KEY, p);
				} catch {
					// not persisted; still applied for this session
				}
			},
		}),
		[preference, mode],
	);
	return <ThemeModeContext.Provider value={value}>{children}</ThemeModeContext.Provider>;
}

export function useThemeMode(): ThemeModeValue {
	const v = useContext(ThemeModeContext);
	if (!v) throw new Error("useThemeMode outside ThemeModeProvider");
	return v;
}
