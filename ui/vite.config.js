import { defineConfig } from 'vite'
import vue from '@vitejs/plugin-vue'

// In local dev, /api is proxied to the Go API on :8080.
// On OpenShift, nginx does the same proxying (see nginx.conf).
export default defineConfig({
  plugins: [vue()],
  server: { proxy: { '/api': 'http://localhost:8080' } }
})
