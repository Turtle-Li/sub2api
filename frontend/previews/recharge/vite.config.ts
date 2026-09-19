import { defineConfig } from 'vite'
import vue from '@vitejs/plugin-vue'
import { resolve } from 'node:path'

// Local UI review only: use the real PaymentView, with a neutral layout and no API traffic.
export default defineConfig({
  plugins: [{ name: 'preview-layout', enforce: 'pre', load(id) {
    if (id.endsWith('/components/layout/AppLayout.vue')) return '<template><main class="mx-auto max-w-7xl px-4 py-8 sm:px-8"><slot /></main></template>'
  } }, vue()],
  resolve: { alias: { '@': resolve(__dirname, '../../src') } },
  server: { host: '127.0.0.1', port: 5199, strictPort: true },
})
