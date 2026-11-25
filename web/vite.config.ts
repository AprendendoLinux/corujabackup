import { defineConfig } from 'vite'
import react from '@vitejs/plugin-react'
import tailwindcss from '@tailwindcss/vite'

// O build vai direto para o pacote Go que o embute no binário do Server.
export default defineConfig({
  plugins: [react(), tailwindcss()],
  build: { outDir: '../internal/webui/dist', emptyOutDir: true },
  server: { proxy: { '/api': 'http://localhost:8080' } },
})
