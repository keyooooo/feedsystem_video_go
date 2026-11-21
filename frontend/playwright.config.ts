import { defineConfig, devices } from "@playwright/test";
const external = process.env.E2E_BASE_URL;
const direct = process.env.E2E_OSS_UPLOAD_ORIGIN;
const playback = process.env.E2E_OSS_PLAYBACK_ORIGIN;
const secure = process.env.E2E_SECURE_SESSIONS === "1";
export default defineConfig({
  testDir: "./e2e",
  fullyParallel: true,
  retries: 0,
  use: {
    baseURL: external || "http://127.0.0.1:4173",
    trace: "retain-on-failure",
  },
  outputDir: external ? "test-results/production" : "test-results/dev",
  webServer: external
    ? undefined
    : [
        {
          command: "npm exec vite -- --mode test --port 4173",
          url: "http://127.0.0.1:4173",
          reuseExistingServer: false,
          env: {
            ...(direct
              ? {
                  VITE_OSS_DIRECT_UPLOAD: "true",
                  VITE_OSS_UPLOAD_ORIGIN: direct,
                }
              : {}),
            ...(secure ? { VITE_SECURE_SESSIONS: "true" } : {}),
            ...(playback ? { VITE_OSS_PLAYBACK_ORIGIN: playback } : {}),
          },
        },
        {
          command: "npm run dev -- --port 4176 --strictPort",
          url: "http://127.0.0.1:4176",
        },
      ],
  projects: [
    { name: "chromium", use: { ...devices["Desktop Chrome"] } },
    { name: "webkit", use: { ...devices["Desktop Safari"] } },
  ],
});
