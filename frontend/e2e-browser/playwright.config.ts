import { defineConfig, devices } from "@playwright/test";

// Real-browser sign-in e2e (regression guard for the #286 redirect loop).
// Specs are named *.e2e.ts (not *.spec.ts) because vitest's default include
// glob would otherwise pick them up; testMatch below is the counterpart.
export default defineConfig({
  testDir: __dirname,
  testMatch: "**/*.e2e.ts",
  timeout: 120_000,
  expect: { timeout: 30_000 },
  retries: 0,
  workers: 1,
  reporter: [["list"]],
  use: {
    baseURL: process.env.E2E_BASE_URL ?? "http://localhost:3000",
    trace: "retain-on-failure",
  },
  projects: [{ name: "chromium", use: { ...devices["Desktop Chrome"] } }],
});
