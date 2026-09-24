import { defineConfig } from "vite";
import react from "@vitejs/plugin-react";
import tailwindcss from "@tailwindcss/vite";
import fs from "node:fs";
import path from "node:path";

const webRoot = path.resolve(__dirname, "../internal/server/web");

export default defineConfig({
  plugins: [
    react(),
    tailwindcss(),
    {
      name: "embed-admin",
      closeBundle() {
        fs.rmSync(path.join(webRoot, "assets/js"), { recursive: true, force: true });
        fs.rmSync(path.join(webRoot, "assets/css"), { recursive: true, force: true });
        fs.copyFileSync(path.join(webRoot, "index.html"), path.join(webRoot, "login.html"));
      },
    },
  ],
  resolve: { alias: { "@": path.resolve(__dirname, "src") } },
  build: { outDir: webRoot, emptyOutDir: false, assetsDir: "assets" },
});
