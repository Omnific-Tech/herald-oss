import { defineConfig, devices } from '@playwright/test'

// The UI and the mock API run on different ports: cross-origin but same-site,
// the same relationship admin.herald.example.com has with herald.example.com.
const uiOrigin = 'http://localhost:4173'
const mockPort = '4012'

export default defineConfig({
  testDir: 'e2e',
  // Tests share one stateful mock process.
  workers: 1,
  forbidOnly: !!process.env.CI,
  reporter: [['list'], ['html', { open: 'never' }]],
  use: {
    baseURL: uiOrigin,
    screenshot: 'on',
    trace: 'retain-on-failure',
    permissions: ['clipboard-read', 'clipboard-write'],
  },
  // Chromium treats http://localhost as secure, so the mock's Secure,
  // __Host- prefixed cookie behaves as it will in production.
  projects: [{ name: 'chromium', use: { ...devices['Desktop Chrome'] } }],
  webServer: [
    {
      command: 'tsx mock/server.ts',
      url: `http://localhost:${mockPort}/__mock/health`,
      env: { MOCK_PORT: mockPort, ADMIN_ORIGIN: uiOrigin },
      reuseExistingServer: false,
    },
    {
      command:
        'vite build --mode e2e --outDir dist-e2e && vite preview --mode e2e --outDir dist-e2e --port 4173 --strictPort',
      url: uiOrigin,
      reuseExistingServer: false,
      timeout: 120_000,
    },
  ],
})
