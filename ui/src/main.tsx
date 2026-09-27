import "@ant-design/v5-patch-for-react-19";
import "@fontsource/ibm-plex-sans/400.css";
import "@fontsource/ibm-plex-sans/500.css";
import "@fontsource/ibm-plex-sans/600.css";
import "@fontsource/ibm-plex-mono/400.css";
import "@fontsource/ibm-plex-mono/500.css";
import "./app/styles/global.css";

import { QueryClient, QueryClientProvider } from "@tanstack/react-query";
import { App as AntApp, ConfigProvider } from "antd";
import { StrictMode } from "react";
import { createRoot } from "react-dom/client";
import { BrowserRouter } from "react-router-dom";
import { App } from "./app/App";
import { ScopeProvider } from "./app/scope";
import { antdTheme } from "./app/theme/antdTheme";
import { ThemeModeProvider, useThemeMode } from "./app/theme/ThemeMode";

const queryClient = new QueryClient({
	defaultOptions: {
		queries: {
			// Live updates invalidate what changed; no need to refetch on focus.
			staleTime: 30_000,
			refetchOnWindowFocus: false,
			retry: 1,
		},
	},
});

function Themed() {
	const { mode } = useThemeMode();
	return (
		<ConfigProvider theme={antdTheme(mode)}>
			<AntApp style={{ height: "100%" }}>
				<App />
			</AntApp>
		</ConfigProvider>
	);
}

createRoot(document.getElementById("root")!).render(
	<StrictMode>
		<QueryClientProvider client={queryClient}>
			<BrowserRouter>
				<ThemeModeProvider>
					<ScopeProvider>
						<Themed />
					</ScopeProvider>
				</ThemeModeProvider>
			</BrowserRouter>
		</QueryClientProvider>
	</StrictMode>,
);
