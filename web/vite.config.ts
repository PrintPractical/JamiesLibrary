/// <reference types="vitest/config" />
import { defineConfig } from "vite";
import react from "@vitejs/plugin-react";

// The build goes into the Go package that embeds it. scripts/clean-dist.mjs
// empties that folder first (keeping .gitkeep), so Vite doesn't.
export default defineConfig({
  plugins: [react()],
  // assetsInlineLimit 0: the CSP allows fonts only from the server itself ('self'), not data: URLs.
  build: { outDir: "../internal/webui/dist", emptyOutDir: false, assetsInlineLimit: 0 },
  server: { proxy: { "/api": "http://localhost:8080", "/media": "http://localhost:8080" } },
  test: {
    environment: "jsdom",
    globals: true,
    setupFiles: ["./src/test-setup.ts"],
  },
});
