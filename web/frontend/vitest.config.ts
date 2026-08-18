import { defineConfig } from 'vitest/config'
import react from '@vitejs/plugin-react'
import { fileURLToPath, URL } from 'node:url'

// Component tests for the shared page-template primitives (PageShell, SaveBar,
// FormGroup, DataTable, InfoPopover). These assert behaviour the Playwright
// journeys cannot reach cheaply — dirty tracking, the unsaved-changes guard,
// and a table's loading/empty/paged states — and they run without a browser,
// so they are the gate that can be checked before pushing.
export default defineConfig({
  plugins: [react()],
  resolve: {
    alias: { '@': fileURLToPath(new URL('./src', import.meta.url)) },
  },
  test: {
    environment: 'jsdom',
    setupFiles: ['./src/test/setup.ts'],
    include: ['src/**/*.test.{ts,tsx}'],
    restoreMocks: true,
  },
})
