import { Spin } from "antd";
import { KeyRound, PlugZap } from "lucide-react";
import { lazy, Suspense, useEffect, useState } from "react";
import { Navigate, Route, Routes } from "react-router-dom";
import { api } from "@/shared/api/client";
import { EmptyState } from "@/shared/components/misc";
import { Shell } from "./Shell";

const OverviewPage = lazy(() => import("@/features/overview/pages/OverviewPage"));
const UnitsPage = lazy(() => import("@/features/units/pages/UnitsPage"));
const UnitPage = lazy(() => import("@/features/units/pages/UnitPage"));
const RunsPage = lazy(() => import("@/features/runs/pages/RunsPage"));
const InboxPage = lazy(() => import("@/features/inbox/pages/InboxPage"));
const ProjectsPage = lazy(() => import("@/features/projects/pages/ProjectsPage"));
const SettingsPage = lazy(() => import("@/features/settings/pages/SettingsPage"));

type Session = "loading" | "ok" | "unauthenticated" | "offline";

// useSession trades a ?token= from the printed link for a cookie, then checks
// the session is valid.
function useSession(): Session {
	const [session, setSession] = useState<Session>("loading");
	useEffect(() => {
		const url = new URL(window.location.href);
		const token = url.searchParams.get("token");
		(async () => {
			try {
				if (token) {
					await api.post("/session", { token });
					url.searchParams.delete("token");
					window.history.replaceState(null, "", url.pathname + url.search + url.hash);
				}
				const health = await api.get<{ authenticated: boolean }>("/health");
				setSession(health.authenticated ? "ok" : "unauthenticated");
			} catch {
				setSession(token ? "unauthenticated" : "offline");
			}
		})();
	}, []);
	return session;
}

export function App() {
	const session = useSession();
	if (session === "loading")
		return (
			<Centered>
				<Spin />
			</Centered>
		);
	if (session === "unauthenticated")
		return (
			<Centered>
				<EmptyState icon={<KeyRound size={28} />} title="Open thefactory from its link">
					For safety, the UI only opens through the link <code>thefactory serve</code> prints in your terminal. It holds
					a token for this machine; after the first visit a cookie remembers it.
				</EmptyState>
			</Centered>
		);
	if (session === "offline")
		return (
			<Centered>
				<EmptyState icon={<PlugZap size={28} />} title="thefactory is not reachable">
					Start it with <code>thefactory serve</code>, then reload this page.
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
