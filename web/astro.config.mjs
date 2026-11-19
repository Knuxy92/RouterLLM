import { fileURLToPath } from "node:url"
import { defineConfig } from "astro/config"
import react from "@astrojs/react"
import tailwindcss from "@tailwindcss/vite"

export default defineConfig({
  base: "/admin/",
  outDir: fileURLToPath(new URL("../internal/admin/dist", import.meta.url)),
  publicDir: "public",
  output: "static",
  integrations: [react()],
  vite: {
    plugins: [tailwindcss()],
    server: {
      proxy: {
        "/admin/api": "http://127.0.0.1:17701",
        "/v1": "http://127.0.0.1:17701",
      },
    },
  },
})
