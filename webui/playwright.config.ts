import { defineConfig, devices } from "@playwright/test";

const webPort = process.env.E2E_WEB_PORT ?? "18081";
const baseURL = process.env.E2E_BASE_URL ?? `http://127.0.0.1:${webPort}`;
const password = process.env.E2E_PASSWORD ?? "e2e-only-Password-2026";

const inheritedEnv = Object.fromEntries(
  Object.entries(process.env).filter((entry): entry is [string, string] => entry[1] !== undefined),
);

export default defineConfig({
  testDir: "./e2e",
  fullyParallel: false,
  workers: 1,
  retries: process.env.CI ? 1 : 0,
  timeout: 60_000,
  expect: { timeout: 15_000 },
  reporter: process.env.CI ? [["list"], ["html", { open: "never" }]] : "list",
  outputDir: "test-results",
  use: {
    baseURL,
    trace: "retain-on-failure",
    screenshot: "only-on-failure",
  },
  projects: [
    {
      name: "setup",
      testMatch: /auth\.setup\.ts/,
    },
    {
      name: "chromium",
      use: {
        ...devices["Desktop Chrome"],
        viewport: { width: 1440, height: 900 },
        storageState: "e2e/.auth/state.json",
      },
      dependencies: ["setup"],
    },
    {
      name: "firefox",
      use: {
        ...devices["Desktop Firefox"],
        viewport: { width: 1440, height: 900 },
        storageState: "e2e/.auth/state.json",
      },
      dependencies: ["setup"],
    },
  ],
  webServer: process.env.E2E_BASE_URL
    ? undefined
    : {
        command: "node e2e/serve.mjs",
        url: `${baseURL}/`,
        reuseExistingServer: !process.env.CI,
        timeout: 120_000,
        env: { ...inheritedEnv, E2E_PASSWORD: password, E2E_WEB_PORT: webPort },
      },
});
