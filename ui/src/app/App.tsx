import { Button, Input, Space, Spin } from "antd";
import { KeyRound, PlugZap } from "lucide-react";
import { lazy, Suspense, useEffect, useState } from "react";
import { Navigate, Route, Routes } from "react-router-dom";
import { ApiError, api } from "@/shared/api/client";
import { EmptyState } from "@/shared/components/misc";
import { Shell } from "./Shell";

const OverviewPage = lazy(() => import("@/features/overview/pages/OverviewPage"));
const UnitsPage = lazy(() => import("@/features/units/pages/UnitsPage"));
const UnitPage = lazy(() => import("@/features/units/pages/UnitPage"));
const RunsPage = lazy(() => import("@/features/runs/pages/RunsPage"));
const InboxPage = lazy(() => import("@/features/inbox/pages/InboxPage"));
const ProjectsPage = lazy(() => import("@/features/projects/pages/ProjectsPage"));
const SettingsPage = lazy(() => import("@/features/settings/pages/SettingsPage"));

type Session = "loading" | "ok" | "unauthenticated" | "rejected" | "offline";

// cleanToken keeps only the token's hex: a link copied with a trailing
// period or quote still works.
function cleanToken(raw: string | null): string {
	const m = (raw ?? "").match(/[0-9a-f]{32,}/i);
	return m ? m[0] : "";
}

// signIn trades a token for the session cookie.
async function signIn(token: string): Promise<Session> {
	try {
		await api.post("/session", { token });
	} catch (e) {
		return e instanceof ApiError && e.status === 401 ? "rejected" : "offline";
	}
	return "ok";
}

// useSession trades a ?token= from the printed link for a cookie, then checks
// the session is valid.
function useSession(): [Session, (s: Session) => void] {
	const [session, setSession] = useState<Session>("loading");
	useEffect(() => {
		const url = new URL(window.location.href);
		const token = cleanToken(url.searchParams.get("token"));
		(async () => {
			if (token) {
				const result = await signIn(token);
				if (result !== "ok") {
					setSession(result);
					return;
				}
				url.searchParams.delete("token");
				window.history.replaceState(null, "", url.pathname + url.search + url.hash);
			}
			try {
				const health = await api.get<{ authenticated: boolean }>("/health");
				setSession(health.authenticated ? "ok" : "unauthenticated");
			} catch {
				setSession("offline");
			}
		})();
	}, []);
	return [session, setSession];
}

export function App() {
	const [session, setSession] = useSession();
	if (session === "loading")
		return (
			<Centered>
				<Spin />
			</Centered>
		);
	if (session === "unauthenticated" || session === "rejected")
		return (
			<Centered>
				<EmptyState
					icon={<KeyRound size={28} />}
					title={session === "rejected" ? "That link's token is not valid" : "Open tfy from its link"}
				>
					<p style={{ marginTop: 0 }}>
						For safety, the UI opens through the link <code>tfy serve</code> prints in your terminal. It holds a token
						for this machine; after the first visit a cookie remembers it.
						{session === "rejected" && " Copy the link again, or paste it here."}
					</p>
					<PasteLink onSignedIn={() => setSession("ok")} />
				</EmptyState>
			</Centered>
		);
	if (session === "offline")
		return (
			<Centered>
				<EmptyState icon={<PlugZap size={28} />} title="tfy is not reachable">
					Start it with <code>tfy serve</code>, then reload this page.
				</EmptyState>
			</Centered>
		);
	return (
		<Routes>
			<Route element={<Shell />}>
				<Route index element={<Navigate to="/units" replace />} />
				<Route
					path="overview"
					element={
						<Page>
							<OverviewPage />
						</Page>
					}
				/>
				<Route
					path="units"
					element={
						<Page>
							<UnitsPage />
						</Page>
					}
				/>
				<Route
					path="units/:id"
					element={
						<Page>
							<UnitPage />
						</Page>
					}
				/>
				<Route
					path="inbox"
					element={
						<Page>
							<InboxPage />
						</Page>
					}
				/>
				<Route
					path="runs"
					element={
						<Page>
							<RunsPage />
						</Page>
					}
				/>
				<Route
					path="projects"
					element={
						<Page>
							<ProjectsPage />
						</Page>
					}
				/>
				<Route
					path="settings"
					element={
						<Page>
							<SettingsPage />
						</Page>
					}
				/>
				<Route path="*" element={<Navigate to="/units" replace />} />
			</Route>
		</Routes>
	);
}

function Page({ children }: { children: React.ReactNode }) {
	return (
		<Suspense
			fallback={
				<Centered>
					<Spin />
				</Centered>
			}
		>
			{children}
		</Suspense>
	);
}

function Centered({ children }: { children: React.ReactNode }) {
	return (
		<div style={{ height: "100%", display: "flex", alignItems: "center", justifyContent: "center" }}>{children}</div>
	);
}

// PasteLink signs in with a pasted link or token.
function PasteLink({ onSignedIn }: { onSignedIn: () => void }) {
	const [value, setValue] = useState("");
	const [error, setError] = useState("");
	const [busy, setBusy] = useState(false);
	const submit = async () => {
		const token = cleanToken(value);
		if (!token) {
			setError("Paste the whole link, or the token after ?token=");
			return;
		}
		setBusy(true);
		const result = await signIn(token);
		setBusy(false);
		if (result === "ok") onSignedIn();
		else setError(result === "rejected" ? "That token is not valid for this installation" : "tfy is not reachable");
	};
	return (
		<div style={{ maxWidth: 460, margin: "12px auto 0" }}>
			<Space.Compact style={{ width: "100%" }}>
				<Input
					placeholder={`${window.location.origin}/?token=…`}
					value={value}
					onChange={(e) => {
						setValue(e.target.value);
						setError("");
					}}
					onPressEnter={submit}
					style={{ fontFamily: "var(--tf-mono)", fontSize: 12 }}
				/>
				<Button type="primary" loading={busy} onClick={submit}>
					Open
				</Button>
			</Space.Compact>
			{error && <div style={{ color: "var(--tf-error)", fontSize: 12, marginTop: 6 }}>{error}</div>}
		</div>
	);
}
