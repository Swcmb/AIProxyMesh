import react from "@vitejs/plugin-react";
import { defineConfig } from "vite";

// The management server serves the API and this UI from the same origin in
// production. During development Vite proxies /api to the WebUI listener.
export default defineConfig({
  base: "/",
  plugins: [react()],
  build: {
    outDir: "../internal/webui/dist",
    emptyOutDir: true,
    chunkSizeWarningLimit: 1200,
  },
  server: {
    host: "127.0.0.1",
    port: 5173,
    proxy: {
      "/api": {
        target: "http://127.0.0.1:8081",
        changeOrigin: false,
      },
    },
  },
});
