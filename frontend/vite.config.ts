import { defineConfig } from "vite";
import vue from "@vitejs/plugin-vue";
import fs from "node:fs";
import path from "node:path";

// The build is embedded into the Go server (internal/server/web, go:embed).
const webRoot = path.resolve(__dirname, "../internal/server/web");

export default defineConfig({
  plugins: [
    vue(),
    {
      name: "embed-admin",
      buildStart() {
        // Drop stale hashed bundles from earlier builds so the embed stays small.
        fs.rmSync(path.join(webRoot, "assets"), { recursive: true, force: true });
      },
      closeBundle() {
        fs.copyFileSync(path.join(webRoot, "index.html"), path.join(webRoot, "login.html"));
      },
    },
  ],
  resolve: { alias: { "@": path.resolve(__dirname, "src") } },
  build: {
    outDir: webRoot,
    emptyOutDir: false,
    assetsDir: "assets",
    chunkSizeWarningLimit: 2000,
  },
  server: {
    proxy: {
      "/api": "http://127.0.0.1:8080",
      "/ws": { target: "ws://127.0.0.1:8080", ws: true },
      "/broadcast": "http://127.0.0.1:8080",
    },
  },
});
