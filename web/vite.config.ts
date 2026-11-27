import path from "node:path";
import tailwindcss from "@tailwindcss/vite";
import react from "@vitejs/plugin-react";
import { defineConfig } from "vitest/config";

// The Go server serves the build from core/internal/webui/dist (copied by
// scripts/sync-dist.mjs). In dev, API calls are proxied to `tolarix serve`.
const backend = process.env.TOLARIX_DEV_BACKEND ?? "http://127.0.0.1:8080";

export default defineConfig({
  plugins: [react(), tailwindcss()],
  resolve: {
    alias: { "@": path.resolve(import.meta.dirname, "./src") },
  },
  server: {
    proxy: {
      "/api": backend,
      "/healthz": backend,
    },
  },
  build: {
    outDir: "dist",
    emptyOutDir: true,
  },
  test: {
    globals: true,
    environment: "jsdom",
    setupFiles: ["./src/test/setup.ts"],
    css: false,
    include: ["src/**/*.test.{ts,tsx}"],
  },
});
