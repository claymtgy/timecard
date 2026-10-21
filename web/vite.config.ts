import { defineConfig } from 'vite'
import vue from '@vitejs/plugin-vue'

// In development the browser talks only to Vite (port 5173); API and auth routes
// are proxied to the Go app so cookies and redirects share one origin.
export default defineConfig({
  plugins: [vue()],
  server: {
    port: 5173,
    strictPort: true,
    proxy: {
      '/api': 'http://localhost:8080',
      '/auth': 'http://localhost:8080',
    },
  },
})
