import { readFileSync } from 'node:fs'
import react from '@vitejs/plugin-react'
import { defineConfig } from 'vite'

// Panelin sürümü server/panel/package.json'dan gelir (server'la birlikte yayınlanır; scripts/release.sh server ikisinin eşit olduğunu doğrular).
const panelVersion = (JSON.parse(readFileSync(new URL('./package.json', import.meta.url), 'utf-8')) as { version: string }).version

// https://vite.dev/config/
export default defineConfig({
  plugins: [react()],
  define: { __PANEL_VERSION__: JSON.stringify(panelVersion) },
  server: {
    proxy: {
      // API çağrılarını Go backend'ine yönlendirir; böylece tarayıcı kendinden imzalı sertifikasıyla
      // asla doğrudan konuşmaz (elle "bu sertifikaya güven" adımından ve server'da herhangi bir
      // CORS ayarından kaçınır — geliştirmede CORS_ALLOWED_ORIGINS gerekmez).
      '/api/v1': {
        target: 'https://localhost:8443',
        changeOrigin: true,
        secure: false,
      },
    },
  },
})
