import path from "node:path";
import react from "@vitejs/plugin-react";
import { defineConfig } from "vite";

// `make dev` runs `thefactory serve --dev` on :7420; the dev server proxies
// the API to it. `make build` writes the bundle into the Go embed directory.
export default defineConfig({
	plugins: [react()],
	resolve: {
		alias: {
			"@": path.resolve(import.meta.dirname, "src"),
		},
	},
	server: {
		port: 5174,
		strictPort: true,
		proxy: {
			"/api": {
				target: "http://127.0.0.1:7420",
				changeOrigin: true,
			},
		},
	},
	build: {
		outDir: "../internal/web/dist",
		emptyOutDir: true,
		chunkSizeWarningLimit: 1500,
		rollupOptions: {
			output: {
				manualChunks(id) {
					if (!id.includes("node_modules")) return;
					if (id.includes("/antd/") || id.includes("/@ant-design/") || id.includes("/rc-")) return "antd";
					if (
						id.includes("/react-markdown/") ||
						id.includes("/remark-") ||
						id.includes("/micromark") ||
						id.includes("/mdast-") ||
						id.includes("/unified/")
					)
						return "markdown";
					if (id.includes("/react-dom/") || id.includes("/react/") || id.includes("/scheduler/")) return "react";
				},
			},
		},
	},
});
