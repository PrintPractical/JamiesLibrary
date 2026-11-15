/// <reference types="vitest/config" />
import { defineConfig } from "vite";
import react from "@vitejs/plugin-react";

// The build goes into the Go package that embeds it. scripts/clean-dist.mjs
// empties that folder first (keeping .gitkeep), so Vite doesn't.
export default defineConfig({
  plugins: [react()],
  build: { outDir: "../internal/webui/dist", emptyOutDir: false },
  server: { proxy: { "/api": "http://localhost:8080", "/media": "http://localhost:8080" } },
  test: {
    environment: "jsdom",
    globals: true,
    setupFiles: ["./src/test-setup.ts"],
  },
});
