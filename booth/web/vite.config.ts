import { defineConfig } from "vite";
import react from "@vitejs/plugin-react";

// SPA built to static assets embedded into the Booth Go binary (embed.FS, §2).
// `base: "./"` keeps asset URLs relative so Booth can serve index.html from any
// mount point. In dev, /v1 is proxied to a locally-running Booth (BOOTH_ADDR).
export default defineConfig({
  plugins: [react()],
  base: "./",
  build: {
    outDir: "dist",
    emptyOutDir: true,
    sourcemap: false,
  },
  server: {
    port: 5173,
    proxy: {
      "/v1": {
        target: process.env.BOOTH_DEV_TARGET || "http://localhost:8080",
        changeOrigin: true,
      },
    },
  },
});
