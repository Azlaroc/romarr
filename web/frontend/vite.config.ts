import { defineConfig, type Plugin } from 'vite'
import react from '@vitejs/plugin-react'
import { fileURLToPath, URL } from 'node:url'
import { writeFileSync } from 'node:fs'

// emptyOutDir wipes web/dist on each build (including the committed .gitkeep
// placeholder that keeps `//go:embed all:dist` compiling on Node-free jobs).
// Rewrite it after the bundle closes so it never silently drops from a commit.
function keepDistPlaceholder(): Plugin {
  return {
    name: 'keep-dist-placeholder',
    closeBundle() {
      const p = fileURLToPath(new URL('../dist/.gitkeep', import.meta.url))
      writeFileSync(p, '# Keeps //go:embed all:dist compiling without a Node build. Do not delete.\n')
    },
  }
}

// The Go binary embeds ../dist and serves it under a strict CSP (script-src
// 'self'). Two rules keep the production output CSP-clean:
//   1. modulePreload.polyfill:false — removes the ONLY inline <script> Vite
//      would otherwise inject (the module-preload polyfill).
//   2. no @vitejs/plugin-legacy — it injects inline SystemJS bootstrap scripts.
// Everything else ships as external, content-hashed /assets/*.js|css.
export default defineConfig({
  plugins: [react(), keepDistPlaceholder()],
  base: '/',
  resolve: {
    alias: { '@': fileURLToPath(new URL('./src', import.meta.url)) },
  },
  build: {
    outDir: '../dist',
    emptyOutDir: true,
    assetsDir: 'assets',
    target: 'es2020',
    sourcemap: false,
    modulePreload: { polyfill: false },
  },
  server: {
    port: 5173,
    proxy: {
      // Dev: proxy the API to the Go backend. If the backend has an API key
      // set, export VITE_DEV_API_KEY and it is injected as X-Api-Key here.
      '/api': { target: 'http://localhost:5001', changeOrigin: false },
      '/torznab': { target: 'http://localhost:5001', changeOrigin: false },
      '/metrics': { target: 'http://localhost:5001', changeOrigin: false },
    },
  },
})
