import { defineConfig } from "@playwright/test";

export default defineConfig({
  testDir: "e2e",
  use: {
    baseURL: "http://127.0.0.1:4173",
    ignoreHTTPSErrors: true,
    launchOptions: {
      args: [
        "--host-resolver-rules=MAP *.e2e.gizclaw.test 127.0.0.1",
        ...(process.env.M04_TLS_SPKI ? [`--ignore-certificate-errors-spki-list=${process.env.M04_TLS_SPKI}`] : []),
      ],
    },
  },
  webServer: { command: "vite --host 127.0.0.1 --port 4173", url: "http://127.0.0.1:4173/harness.html", reuseExistingServer: false },
});
