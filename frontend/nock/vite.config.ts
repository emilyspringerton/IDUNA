import tailwindcss from '@tailwindcss/vite'
import react from '@vitejs/plugin-react'
import { defineConfig } from 'vite'

// NOCK is served by IDUNA (Go) at /admin/nock/ -- `base` must match that mount point so every
// built asset URL resolves correctly once index.html is served from there instead of the site
// root. The dev-server proxy lets `npm run dev` talk to a real local IDUNA instance on :8080
// without a CORS dance during development.
export default defineConfig({
  base: '/admin/nock/',
  plugins: [tailwindcss(), react()],
  server: {
    proxy: {
      '/admin/nock/api': 'http://localhost:8080',
    },
  },
})
