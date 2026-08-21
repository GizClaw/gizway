import { expect, test } from "@playwright/test";
import { execFileSync } from "node:child_process";

function composeContainer(service: string): string {
  const project = process.env.M04_E2E_COMPOSE_PROJECT;
  if (!project) throw new Error("M04_E2E_COMPOSE_PROJECT is required for real state tests");
  return `${project}-${service}-1`;
}

function docker(...arguments_: string[]) {
  execFileSync("docker", arguments_, { stdio: "pipe" });
}

function setModelListingAvailability(availability: "available" | "unavailable") {
  const project = process.env.M04_E2E_COMPOSE_PROJECT;
  if (!project) throw new Error("M04_E2E_COMPOSE_PROJECT is required for real state tests");
  const body = JSON.stringify({ availability });
  docker("run", "--rm", "--network", `${project}_default`, "--volume", `${project}_fixtures:/fixtures:ro`, "gizway-e2e-helper:dev", "sh", "-ec", `curl --fail --silent --show-error -X PATCH http://gizway-global:8080/admin/v1/model-listings/listing_story_text_global -H "Content-Type: application/json" -H "X-GizWay-Admin-Key: $(cat /fixtures/admin-key)" --data '${body}' >/dev/null && curl --fail --silent --show-error -X PATCH http://gizway-global:8080/admin/v1/model-listings/listing_story_text_zero_global -H "Content-Type: application/json" -H "X-GizWay-Admin-Key: $(cat /fixtures/admin-key)" --data '${body}' >/dev/null`);
}

async function login(page: import("@playwright/test").Page, username = process.env.M04_E2E_USERNAME ?? "", passwordValue = process.env.M04_E2E_PASSWORD ?? "") {
  await page.goto("/");
  await page.getByRole("button", { name: "Sign in", exact: true }).click({ timeout: 90_000 });
  await expect(page).toHaveURL(/identity\.e2e\.gizclaw\.test/i);
  const loginname = page.getByRole("textbox", { name: /loginname/i });
  const continueButton = page.getByRole("button", { name: "Continue", exact: true });
  await expect(loginname).toBeEditable();
  await page.waitForTimeout(500);
  await loginname.fill(username);
  await expect(loginname).toHaveValue(username);
  await expect(continueButton).toBeEnabled();
  await continueButton.click();
  const password = page.getByLabel(/password/i);
  await expect(password).toBeEditable();
  await page.waitForTimeout(500);
  await password.fill(passwordValue);
  await expect(password).toHaveValue(passwordValue);
  await expect(continueButton).toBeEnabled();
  await continueButton.click();
  await expect(page.getByRole("heading", { name: /good afternoon/i })).toBeVisible({ timeout: 60_000 });
}

test("a genuinely new Human is initialized by the first real login", async ({ page }, testInfo) => {
  test.skip(process.env.M04_REAL_E2E !== "1", "requires the M04 Compose stack");
  test.skip(testInfo.project.name !== "global-desktop", "the new Human is initialized once");
  test.setTimeout(180_000);
  await login(page, process.env.M04_E2E_NEW_USERNAME ?? "", process.env.M04_E2E_NEW_PASSWORD ?? "");
  await expect(page.getByTestId("gizpay-sync-state")).toHaveAttribute("data-state", "ready");
  await page.getByRole("button", { name: "Subscription Keys", exact: true }).click();
  await expect(page.getByRole("button", { name: "Create Key", exact: true }).first()).toBeVisible();
});

test("real login initializes the Human and reaches both PowerSync services", async ({ page }) => {
  test.skip(process.env.M04_REAL_E2E !== "1", "requires the M04 Compose stack");
  test.setTimeout(180_000);
  await login(page);
  await expect(page.getByTestId("gizpay-sync-state")).toHaveAttribute("data-state", "ready");
  await expect(page.getByTestId("gizway-sync-state")).toHaveAttribute("data-state", "ready");
  await expect(page.getByTestId("regional-model-count")).toHaveText("2");
  await expect(page.getByTestId("regional-price-count")).not.toHaveText("0");
});

test("background Usage and Order changes refresh the open UI", async ({ page }, testInfo) => {
  test.skip(process.env.M04_REAL_E2E !== "1", "requires the M04 Compose stack");
  test.skip(testInfo.project.name !== "global-desktop", "the live-query path is exercised once");
  test.setTimeout(180_000);
  await login(page);
  await page.getByRole("button", { name: "Subscription Keys", exact: true }).click();
  const keyRow = page.getByRole("row").filter({ hasText: "Bootstrap" });
  await keyRow.getByRole("button", { name: /^Show / }).click();
  const subscriptionKey = (await keyRow.locator("code").textContent())?.trim() ?? "";
  expect(subscriptionKey).not.toBe("");
  await page.getByRole("button", { name: "Usage", exact: true }).click();
  const rows = page.getByRole("table").getByRole("row");
  const before = await rows.count();
  const status = await page.evaluate(async (key) => {
    const response = await fetch("/openai/v1/chat/completions", {
      method: "POST",
      headers: { Authorization: `Bearer ${key}`, "Content-Type": "application/json" },
      body: JSON.stringify({ model: "story-text", messages: [{ role: "user", content: "live-query acceptance" }] }),
    });
    return response.status;
  }, subscriptionKey);
  expect(status).toBe(200);
  await expect(rows).toHaveCount(before + 1, { timeout: 60_000 });
});

test("real CRUD queues close Subscription Key, Provider Key, Top-up and logout loops", async ({ page }, testInfo) => {
  test.skip(process.env.M04_REAL_E2E !== "1", "requires the M04 Compose stack");
  test.skip(testInfo.project.name.endsWith("mobile"), "real write loop runs once per regional desktop site");
  test.setTimeout(180_000);
  await login(page);
  await expect(page.getByTestId("regional-model-count")).not.toHaveText("0");
  await expect(page.getByTestId("payg-product-count")).not.toHaveText("0");
  await expect(page.getByTestId("active-payg-subscription-count")).not.toHaveText("0");

  await page.getByRole("button", { name: "Subscription Keys", exact: true }).click();
  await page.getByRole("button", { name: "Create Key", exact: true }).click();
  const runID = crypto.randomUUID();
  const keyName = `Real browser ${testInfo.project.name} ${runID}`;
  await page.getByLabel("Key name").fill(keyName);
  await page.getByRole("button", { name: "Create Key", exact: true }).last().click();
  const keyRow = page.getByRole("row").filter({ hasText: keyName });
  await expect(keyRow).toBeVisible({ timeout: 60_000 });
  await keyRow.getByRole("button", { name: "Revoke" }).click();
  await expect(keyRow).toContainText("revoked", { timeout: 60_000 });

  await page.getByRole("button", { name: "Provider Keys", exact: true }).click();
  await page.getByRole("button", { name: "Add Provider Key", exact: true }).first().click();
  const createProviderDialog = page.getByRole("dialog", { name: "Add Provider Key" });
  const providerName = `Real provider ${testInfo.project.name} ${runID}`;
  await createProviderDialog.getByLabel("Key name", { exact: true }).fill(providerName);
  await createProviderDialog.getByLabel("Provider Key", { exact: true }).fill(`provider-secret-${testInfo.project.name}-${runID}`);
  await createProviderDialog.getByLabel("Input price / 1M", { exact: true }).fill("20");
  await createProviderDialog.getByLabel("Output price / 1M", { exact: true }).fill("40");
  await createProviderDialog.getByRole("button", { name: "Add Provider Key", exact: true }).click();
  const providerCard = page.getByText(providerName, { exact: true }).locator("xpath=ancestor::*[contains(@class,'rounded-2xl')][1]");
  await expect(providerCard).toBeVisible({ timeout: 60_000 });
  await providerCard.getByRole("button", { name: "Configure prices" }).click();
  await page.getByLabel("Input price / 1M").fill("25");
  await page.getByRole("button", { name: "Save prices" }).click();
  await providerCard.getByRole("button", { name: "Disable Key" }).click();
  await expect(providerCard).toContainText("disabled", { timeout: 60_000 });

  await page.getByRole("button", { name: "Credits", exact: true }).click();
  await page.getByRole("button", { name: "Add credit" }).first().click();
  await page.getByRole("button", { name: "50,000", exact: true }).click();
  await page.getByRole("button", { name: "Confirm top-up" }).click();
  await expect(page.getByRole("dialog")).toHaveCount(0, { timeout: 60_000 });

  await page.getByRole("button", { name: "Sign out" }).click();
  await expect(page.getByRole("heading", { name: /multi-model ai gateway/i })).toBeVisible({ timeout: 60_000 });
});

test("one real PowerSync outage degrades independently and recovers", async ({ page }, testInfo) => {
  test.skip(process.env.M04_REAL_E2E !== "1", "requires the M04 Compose stack");
  test.skip(testInfo.project.name !== "global-desktop", "the real outage path is exercised once");
  test.setTimeout(180_000);
  await login(page);
  await expect(page.getByTestId("gizpay-sync-state")).toHaveAttribute("data-state", "ready");
  await expect(page.getByTestId("gizway-sync-state")).toHaveAttribute("data-state", "ready");
  const regional = composeContainer("powersync-global");
  try {
    docker("stop", regional);
    await expect(page.getByTestId("gizway-sync-state")).toHaveAttribute("data-state", "offline", { timeout: 60_000 });
    await expect(page.getByTestId("gizpay-sync-state")).toHaveAttribute("data-state", "ready");
    await expect(page.getByTestId("regional-model-count")).toHaveText("2");
  } finally {
    docker("start", regional);
  }
  await expect(page.getByTestId("gizway-sync-state")).toHaveAttribute("data-state", "ready", { timeout: 60_000 });
});

test("real CRUD Queue renders retrying commands as pending and deterministic rejection as failed", async ({ page }, testInfo) => {
  test.skip(process.env.M04_REAL_E2E !== "1", "requires the M04 Compose stack");
  test.skip(testInfo.project.name !== "global-desktop", "the real Connector command-state path is exercised once");
  test.setTimeout(180_000);
  await login(page);
  await page.getByRole("button", { name: "Credits", exact: true }).click();
  const topUpAPI = "**/topups";
  let retryingRequests = 0;
  const retrying = async (route: import("@playwright/test").Route) => {
    retryingRequests++;
    await route.fulfill({ status: 503, contentType: "application/json", body: JSON.stringify({ error: { code: "temporarily_unavailable", message: "retry this command" } }) });
  };
  await page.route(topUpAPI, retrying);
  await page.getByRole("button", { name: "Add credit" }).first().click();
  await page.getByRole("button", { name: "50,000", exact: true }).click();
  await page.getByRole("button", { name: "Confirm top-up" }).click();
  await expect(page.getByTestId("runtime-state")).toHaveAttribute("data-state", "command_pending", { timeout: 30_000 });
  await expect.poll(() => retryingRequests).toBeGreaterThan(0);
  await expect(page.getByTestId("runtime-state")).toBeVisible();
  await page.unroute(topUpAPI, retrying);
  await expect(page.getByRole("dialog")).toHaveCount(0, { timeout: 60_000 });
  await expect(page.getByTestId("runtime-state")).toHaveCount(0);

  let rejectedRequests = 0;
  await page.route(topUpAPI, async (route) => {
    rejectedRequests++;
    await route.fulfill({ status: 409, contentType: "application/json", body: JSON.stringify({ error: { code: "test_command_rejected", message: "deterministic rejection" } }) });
  });
  await page.getByRole("button", { name: "Add credit" }).first().click();
  await page.getByRole("button", { name: "50,000", exact: true }).click();
  await page.getByRole("button", { name: "Confirm top-up" }).click();
  await expect.poll(() => rejectedRequests).toBeGreaterThan(0);
  await expect(page.getByTestId("runtime-state")).toHaveAttribute("data-state", "command_failed", { timeout: 30_000 });
  await expect(page.getByTestId("runtime-state")).toContainText("test_command_rejected");
  await expect(page.getByTestId("runtime-state")).toBeVisible();
});

test("public Catalog renders real prices, empty data and an offline service, then recovers", async ({ page }, testInfo) => {
  test.skip(process.env.M04_REAL_E2E !== "1", "requires the M04 Compose stack");
  test.skip(testInfo.project.name !== "global-desktop", "the real Catalog update path is exercised once");
  test.setTimeout(180_000);
  await page.goto("/");
  await expect(page.getByTestId("public-model-catalog-state")).toHaveText("2", { timeout: 60_000 });
  await expect(page.getByText("input_tokens: 1,000 GIZ Credit / 1M · output_tokens: 2,000 GIZ Credit / 1M", { exact: true })).toBeVisible();
  try {
    setModelListingAvailability("unavailable");
    await expect(page.getByTestId("public-model-catalog-state")).toHaveAttribute("data-state", "empty", { timeout: 60_000 });
    await expect(page.getByTestId("public-model-catalog-state")).toHaveText("0");
    await expect(page.getByTestId("public-gizway-catalog-notice")).toHaveAttribute("data-state", "empty");
    await expect(page.getByTestId("public-gizway-catalog-notice")).toBeVisible();
  } finally {
    setModelListingAvailability("available");
  }
  await expect(page.getByTestId("public-model-catalog-state")).toHaveText("2", { timeout: 60_000 });
  await expect(page.getByTestId("public-gizway-catalog-notice")).toHaveCount(0);
  const regional = composeContainer("powersync-global");
  try {
    docker("stop", regional);
    await expect(page.getByTestId("public-gizway-catalog-notice")).toHaveAttribute("data-state", "offline", { timeout: 60_000 });
    await expect(page.getByTestId("public-gizway-catalog-notice")).toBeVisible();
    await expect(page.getByTestId("public-gizpay-catalog-notice")).toHaveCount(0);
  } finally {
    docker("start", regional);
  }
  await expect(page.getByTestId("public-gizway-catalog-notice")).toHaveCount(0, { timeout: 60_000 });
});

test("real Public Catalog renders loading and sync-error states from PowerSync", async ({ page }, testInfo) => {
  test.skip(process.env.M04_REAL_E2E !== "1", "requires the M04 Compose stack");
  test.skip(testInfo.project.name !== "global-desktop", "the real initial failure path is exercised once");
  test.setTimeout(180_000);
  const pay = composeContainer("powersync-pay");
  const way = composeContainer("powersync-global");
  try {
    docker("stop", pay, way);
    await page.goto("/");
    await expect(page.getByTestId("runtime-state")).toHaveAttribute("data-state", /opening_local_db|first_sync|sync_error/, { timeout: 30_000 });
    await expect(page.getByTestId("runtime-state")).toHaveAttribute("data-state", "sync_error", { timeout: 90_000 });
    await expect(page.getByRole("heading", { name: "sync error" })).toBeVisible();
    await expect(page.getByTestId("runtime-state")).toBeVisible();
  } finally {
    docker("start", pay, way);
  }
});

test("a rejected Catalog credential produces a real denied PowerSync state", async ({ page }, testInfo) => {
  test.skip(process.env.M04_REAL_E2E !== "1", "requires the M04 Compose stack");
  test.skip(testInfo.project.name !== "cn-mobile", "the destructive authorization path runs after all other real E2E coverage");
  test.setTimeout(180_000);
  const encode = (value: object) => Buffer.from(JSON.stringify(value)).toString("base64url");
  const token = `${encode({ alg: "none", typ: "JWT" })}.${encode({ iss: "https://identity.e2e.gizclaw.test:18080", aud: "rejected-audience", exp: Math.floor(Date.now() / 1000) + 300 })}.signature`;
  await page.route("**/auth/catalog-token", async (route) => {
    await route.fulfill({ status: 200, contentType: "application/json", body: JSON.stringify({ access_token: token, token_type: "Bearer" }) });
  });
  await page.goto("/");
  await expect(page.getByTestId("public-gizway-sync-state")).toHaveAttribute("data-state", "denied", { timeout: 60_000 });
  await expect(page.getByTestId("public-gizpay-sync-state")).toHaveAttribute("data-state", "denied", { timeout: 60_000 });
  await expect(page.getByTestId("public-catalog-state")).toHaveAttribute("data-state", "denied");
  await expect(page.getByTestId("runtime-state")).toBeVisible();
  await expect(page.getByRole("heading", { name: "denied" })).toBeVisible();
});
