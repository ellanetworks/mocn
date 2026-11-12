import { defineConfig, devices } from "@playwright/test";

const E2E_PORT = process.env.ELLA_E2E_PORT ?? "5030";
const DEFAULT_URL = `http://localhost:${E2E_PORT}`;
const baseURL = process.env.ELLA_E2E_BASE_URL ?? DEFAULT_URL;
const SESSION_FILE = "e2e/.auth/admin.json";

export default defineConfig({
  testDir: "./e2e",
  testIgnore: "**/.build/**",
  globalSetup: "./e2e/global-setup.ts",
  fullyParallel: true,
  forbidOnly: !!process.env.CI,
  retries: process.env.CI ? 2 : 0,
  workers: process.env.CI ? 1 : undefined,
  reporter: process.env.CI ? [["github"], ["html", { open: "never" }]] : "list",
  timeout: 30_000,
  expect: { timeout: 10_000 },
  use: {
    ...devices["Desktop Chrome"],
    baseURL,
    trace: "on-first-retry",
    screenshot: "only-on-failure",
    video: "retain-on-failure",
  },
  projects: [
    {
      name: "initialize",
      testDir: "./e2e/initialize",
    },
    {
      name: "unauthenticated",
      testDir: "./e2e/unauthenticated",
      dependencies: ["initialize"],
    },
    {
      name: "setup",
      testMatch: /auth\.setup\.ts/,
      dependencies: ["unauthenticated"],
    },
    {
      name: "authenticated",
      testDir: "./e2e/authenticated",
      dependencies: ["setup"],
      use: { storageState: SESSION_FILE },
    },
  ],
  ...(!process.env.ELLA_E2E_BASE_URL && {
    webServer: {
      command: "e2e/serve.sh",
      env: { ELLA_E2E_PORT: E2E_PORT },
      url: `${DEFAULT_URL}/api/v1/status`,
      reuseExistingServer: false,
      timeout: 180_000,
      stdout: "pipe",
      stderr: "pipe",
    },
  }),
});
