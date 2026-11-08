import { defineConfig } from 'vite'
import vue from '@vitejs/plugin-vue'
import { resolve } from 'node:path'

// Local component review without a backend connection or public settings fetch.
export default defineConfig({
  plugins: [vue()],
  resolve: { alias: { '@': resolve(__dirname, '../../src') } },
  server: { host: '127.0.0.1', port: 5187, strictPort: true },
})
