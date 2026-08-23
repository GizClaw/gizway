import { expect, test } from "@playwright/test";
import { execFileSync } from "node:child_process";

function composeContainer(service: string): string {
  const project = process.env.M04_E2E_COMPOSE_PROJECT;
  if (!project) throw new Error("M04_E2E_COMPOSE_PROJECT is required");
  return `${project}-${service}-1`;
}

for (const regional of [
  { region: "global" as const, port: process.env.M04_ENTRY_PORT ?? "3000" },
  { region: "cn" as const, port: process.env.M04_ENTRY_CN_PORT ?? "3001" },
]) {
  test(`${regional.region} public Catalog works cross-origin through the packaged surface`, async ({ page }) => {
    test.skip(process.env.M04_REAL_E2E !== "1", "requires the Compose stack");
    test.setTimeout(120_000);
    await page.goto("/harness.html");
    const result = await page.evaluate(({ region, port }) => globalThis.sdkTest.connectPublic(`https://${region}.e2e.gizclaw.test:${port}`, region), regional);
    expect(result.products).toBeGreaterThan(0);
    expect(result.models).toBeGreaterThan(0);
    await expect.poll(async () => page.evaluate(() => globalThis.sdkTest.state()), { timeout: 60_000 }).toEqual({ gizpay: "ready", gizway: "ready" });
    await page.evaluate(() => globalThis.sdkTest.close(true));
  });
}

test("one PowerSync outage preserves the other service and cached Catalog, then recovers", async ({ page }) => {
  test.skip(process.env.M04_REAL_E2E !== "1", "requires the Compose stack");
  test.setTimeout(180_000);
  await page.goto("/harness.html");
  const entry = `https://global.e2e.gizclaw.test:${process.env.M04_ENTRY_PORT ?? "3000"}`;
  const before = await page.evaluate((origin) => globalThis.sdkTest.connectPublic(origin, "global"), entry);
  expect(before.models).toBeGreaterThan(0);
  const regional = composeContainer("powersync-global");
  try {
    execFileSync("docker", ["stop", regional]);
    await expect.poll(async () => page.evaluate(() => globalThis.sdkTest.state()), { timeout: 60_000 }).toEqual({ gizpay: "ready", gizway: "offline" });
  } finally { execFileSync("docker", ["start", regional]); }
  await expect.poll(async () => page.evaluate(() => globalThis.sdkTest.state()), { timeout: 60_000 }).toEqual({ gizpay: "ready", gizway: "ready" });
  await page.evaluate(() => globalThis.sdkTest.close(true));
});

test("PKCE login reaches both authenticated services through the UI-neutral SDK", async ({ page }) => {
  test.skip(process.env.M04_REAL_E2E !== "1", "requires the Compose stack");
  test.setTimeout(180_000);
  const entry = `https://global.e2e.gizclaw.test:${process.env.M04_ENTRY_PORT ?? "3000"}`;
  await page.goto("/harness.html");
  const loginURL = await page.evaluate((origin) => globalThis.sdkTest.beginLogin(origin, "global"), entry);
  await page.goto(loginURL);
  const loginname = page.getByRole("textbox", { name: /loginname/i });
  const continueButton = page.getByRole("button", { name: "Continue", exact: true });
  await loginname.fill(process.env.M04_E2E_USERNAME ?? "");
  await continueButton.click();
  await page.getByLabel(/password/i).fill(process.env.M04_E2E_PASSWORD ?? "");
  await continueButton.click();
  await expect(page).toHaveURL(/127\.0\.0\.1:4173\/harness\.html\?code=/, { timeout: 60_000 });
  const result = await page.evaluate((origin) => globalThis.sdkTest.completeLogin(origin, "global", location.href), entry);
  expect(result.products).toBeGreaterThan(0);
  expect(result.models).toBeGreaterThan(0);
  expect(result.states).toEqual({ gizpay: "ready", gizway: "ready" });
  const mutations = await page.evaluate(() => globalThis.sdkTest.mutate());
  expect(mutations.keys).toBeGreaterThan(0);
  expect(mutations.providerStatus).toBe("disabled");
  await page.evaluate(() => globalThis.sdkTest.close(true));
});
