import { defineConfig } from "vite";
import react from "@vitejs/plugin-react";
import tailwindcss from "@tailwindcss/vite";
import { fileURLToPath, URL } from "node:url";

// KEIROUTER_BACKEND lets a second dev stack run side by side (e.g. against a
// throwaway backend on another port) without editing this file.
const BACKEND = process.env.KEIROUTER_BACKEND ?? "http://127.0.0.1:20180";

// During development the dashboard talks to the Go backend on :20180.
// In production the built assets are served by the backend itself.
export default defineConfig({
  plugins: [react(), tailwindcss()],
  resolve: {
    alias: { "@": fileURLToPath(new URL("./src", import.meta.url)) },
  },
  server: {
    host: "127.0.0.1",
    port: 5180,
    proxy: {
      "/api": {
        target: BACKEND,
        changeOrigin: true,
        // Enable WebSocket proxy for streaming endpoints (e.g. /api/usage/stream)
        ws: true,
        // Suppress noisy ECONNREFUSED errors while backend is still starting.
        // The browser will simply retry the request once the backend is up.
        configure: (proxy) => {
          proxy.on("error", (err, _req, res) => {
            if ("code" in err && (err as NodeJS.ErrnoException).code === "ECONNREFUSED") {
              // Backend not ready yet — return 503 so the client can retry.
              if (res && "writeHead" in res) {
                (res as import("http").ServerResponse).writeHead(503, {
                  "Content-Type": "application/json",
                  "Retry-After": "2",
                });
                (res as import("http").ServerResponse).end(
                  JSON.stringify({ error: "Backend starting, retry shortly…" })
                );
              }
              return;
            }
            console.error("[proxy]", err.message);
          });
        },
      },
      "/v1": {
        target: BACKEND,
        changeOrigin: true,
        ws: true,
        configure: (proxy) => {
          proxy.on("error", (_err) => {
            // silently ignore — same startup race
          });
        },
      },
      "/healthz": {
        target: BACKEND,
        changeOrigin: true,
        configure: (proxy) => {
          proxy.on("error", (_err) => {
            // silently ignore — same startup race
          });
        },
      },
    },
  },
  build: {
    outDir: "dist",
    rollupOptions: {
      output: {
        // Split heavy, rarely-changing vendor libs into their own chunks so the
        // browser caches them across deploys and they load only with the pages
        // that import them.
        manualChunks(id: string) {
          if (!id.includes("node_modules")) return undefined;
          if (/[\\/]node_modules[\\/](react|react-dom|react-router|react-router-dom|scheduler)[\\/]/.test(id)) return "react";
          if (id.includes("@tanstack/react-query")) return "query";
          if (/[\\/]node_modules[\\/](radix-ui|@radix-ui|@floating-ui)[\\/]/.test(id)) return "radix";
          return undefined;
        },
      },
    },
  },
});
