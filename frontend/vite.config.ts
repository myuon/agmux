import { defineConfig } from 'vite'
import react from '@vitejs/plugin-react'
import tailwindcss from '@tailwindcss/vite'

// Backend port for the dev proxy; override to point at a non-default
// backend (e.g. AGMUX_BACKEND_PORT=4322 npm run dev)
const backendPort = process.env.AGMUX_BACKEND_PORT || '4321'

export default defineConfig({
  plugins: [react(), tailwindcss()],
  server: {
    proxy: {
      '/api': `http://localhost:${backendPort}`,
      '/ws': {
        target: `ws://localhost:${backendPort}`,
        ws: true,
      },
    },
  },
})
