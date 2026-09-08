import { defineConfig } from "vite"
import react from "@vitejs/plugin-react"
import tailwindcss from "@tailwindcss/vite"

export default defineConfig({
  plugins: [react(), tailwindcss()],
  build: {
    outDir: "../backend/internal/web/dist",
    emptyOutDir: true,
  },
  server: {
    port: 8501,
    proxy: {
      "/api": "http://127.0.0.1:8500",
      "/health": "http://127.0.0.1:8500",
    },
  },
})
