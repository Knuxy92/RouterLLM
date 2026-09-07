import path from "node:path"
import { defineConfig } from "vite"
import tailwindcss from "@tailwindcss/vite"

export default defineConfig({
  plugins: [tailwindcss()],
  base: "/admin/",
  build: {
    outDir: path.resolve(import.meta.dirname, "../internal/admin/dist"),
    emptyOutDir: true,
  },
  server: {
    port: 5173,
    host: true,
    // quick-tunnel hostnames are random per run, so allow the whole domain
    allowedHosts: [".trycloudflare.com"],
    proxy: {
      "/admin/api": "http://127.0.0.1:17701",
      "/v1": "http://127.0.0.1:17701",
    },
  },
})
