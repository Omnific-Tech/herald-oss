import path from 'node:path'
import tailwindcss from '@tailwindcss/vite'
import react from '@vitejs/plugin-react'
import { defineConfig, loadEnv } from 'vite'

export default defineConfig(({ command, mode }) => {
  // The API base URL is public configuration. A build without it would ship a
  // bundle that silently calls the wrong host.
  const env = loadEnv(mode, import.meta.dirname, 'VITE_')
  if (command === 'build' && !env.VITE_ADMIN_API_URL) {
    throw new Error(`VITE_ADMIN_API_URL is not set for mode "${mode}"`)
  }

  return {
    plugins: [react(), tailwindcss()],
    resolve: {
      alias: { '@': path.resolve(import.meta.dirname, 'src') },
    },
  }
})
