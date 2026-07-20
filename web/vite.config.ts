import { defineConfig } from 'vite'
import react from '@vitejs/plugin-react'

// The build lands in internal/webui/dist, which go:embed pulls into the
// takos binary — the tracker daemon then serves it from its own port.
// In dev, `pnpm dev` proxies the API to a running daemon on :7434 so the
// UI talks to real workspaces without a build step.
export default defineConfig({
  plugins: [react()],
  build: {
    outDir: '../internal/webui/dist',
    emptyOutDir: true,
  },
  server: {
    proxy: {
      '/api': {
        target: 'http://127.0.0.1:7434',
        changeOrigin: true,
      },
    },
  },
})
