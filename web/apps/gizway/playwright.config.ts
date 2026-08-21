import { defineConfig, devices } from "@playwright/test";

const port = Number(process.env.M04_WEB_PORT ?? 44173);
const cnPort = Number(process.env.M04_WEB_CN_PORT ?? port);
const external = process.env.M04_WEB_EXTERNAL === "1";
const tlsSPKI = process.env.M04_TLS_SPKI ?? "";
if (external && tlsSPKI === "") throw new Error("M04_TLS_SPKI is required for scoped external E2E certificate trust");
const globalBaseURL = external ? `https://global.e2e.gizclaw.test:${port}` : `http://global.localhost:${port}`;
const cnBaseURL = external ? `https://cn.e2e.gizclaw.test:${cnPort}` : `http://cn.localhost:${port}`;

export default defineConfig({
  testDir: "./e2e",
  fullyParallel: false,
  workers: external ? 1 : undefined,
  forbidOnly: true,
  retries: process.env.CI ? 2 : 0,
  reporter: [["list"], ["html", { outputFolder: "playwright-report", open: "never" }]],
  use: {
    trace: "retain-on-failure",
    screenshot: "only-on-failure",
    video: "retain-on-failure",
    launchOptions: {
      args: [
        "--host-resolver-rules=MAP *.e2e.gizclaw.test 127.0.0.1",
        ...(external ? [`--ignore-certificate-errors-spki-list=${tlsSPKI}`] : []),
      ],
    },
  },
  expect: { toHaveScreenshot: { animations: "disabled" } },
  webServer: external ? undefined : {
    command: `VITE_GIZWAY_WEB_MODE=fake npm run build && npm run preview -- --host 0.0.0.0 --port ${port}`,
    url: globalBaseURL,
    reuseExistingServer: false,
    timeout: 120_000,
  },
  projects: [
    { name: "global-desktop", use: { ...devices["Desktop Chrome"], baseURL: globalBaseURL } },
    { name: "cn-desktop", use: { ...devices["Desktop Chrome"], baseURL: cnBaseURL } },
    { name: "global-mobile", use: { ...devices["Pixel 7"], baseURL: globalBaseURL } },
    { name: "cn-mobile", use: { ...devices["Pixel 7"], baseURL: cnBaseURL } },
  ],
});
