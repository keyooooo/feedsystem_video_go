import { defineConfig, devices } from "@playwright/test";
const external = process.env.E2E_BASE_URL;
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
    : {
        command: "npm run dev -- --port 4173",
        url: "http://127.0.0.1:4173",
        reuseExistingServer: !process.env.CI,
      },
  projects: [
    { name: "chromium", use: { ...devices["Desktop Chrome"] } },
    { name: "webkit", use: { ...devices["Desktop Safari"] } },
  ],
});
