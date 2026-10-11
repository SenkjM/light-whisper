import { defineConfig } from "vite";
import react from "@vitejs/plugin-react";
import path from "path";

const host = process.env.TAURI_DEV_HOST;

export default defineConfig(async () => ({
  plugins: [react()],
  resolve: {
    alias: {
      "@": path.resolve(import.meta.dirname, "./src"),
    },
  },
  build: {
    target: ["es2020", "edge88", "firefox78", "chrome87", "safari14"],
    cssTarget: ["es2020", "edge88", "firefox78", "chrome87", "safari14"],
    rolldownOptions: {
      // Concurrent hook wall times are diagnostic noise, not build failures.
      checks: { pluginTimings: false },
    },
  },
  clearScreen: false,
  server: {
    port: 1422,
    strictPort: true,
    host: host || false,
    hmr: host
      ? {
          protocol: "ws",
          host,
          port: 1421,
        }
      : undefined,
    watch: {
      ignored: ["**/src-tauri/**"],
    },
    // PLAN 步骤 6: browser → Vite → lw-engine (strip Origin so Go does not need CORS).
    proxy: {
      "/lw-engine": {
        target: process.env.VITE_LW_ENGINE_TARGET || "http://127.0.0.1:8741",
        changeOrigin: true,
        rewrite: (p) => p.replace(/^\/lw-engine/, ""),
        ws: true,
        configure: (proxy) => {
          proxy.on("proxyReq", (proxyReq) => {
            proxyReq.removeHeader("origin");
          });
        },
      },
    },
  },
}));
