import { expect, test } from "@playwright/test";
import { execFileSync } from "node:child_process";
import { mkdtempSync, readFileSync, rmSync, writeFileSync } from "node:fs";
import { tmpdir } from "node:os";
import { join, resolve } from "node:path";

function composeContainer(service: string): string {
  const project = process.env.M04_E2E_COMPOSE_PROJECT;
  if (!project) throw new Error("M04_E2E_COMPOSE_PROJECT is required for real state tests");
  return `${project}-${service}-1`;
}

function docker(...arguments_: string[]) {
  execFileSync("docker", arguments_, { stdio: "pipe" });
}

function startDeniedPowerSync(service: "gizpay" | "cn" | "global"): () => void {
  const project = process.env.M04_E2E_COMPOSE_PROJECT;
  if (!project) throw new Error("M04_E2E_COMPOSE_PROJECT is required for real state tests");
  const original = composeContainer(`powersync-${service}`);
  const replacement = `${project}-powersync-${service}-denied`;
  const root = resolve(process.cwd(), "../../..");
  const configName = service === "gizpay" ? "gizpay" : `gizway-${service}`;
  const source = resolve(root, `tests/powersync/config/${configName}-service.yaml`);
  const sync = resolve(root, `tests/powersync/config/${configName}-sync-config.yaml`);
  const directory = mkdtempSync(join(tmpdir(), `gizway-${service}-denied-`));
  const config = join(directory, "service.yaml");
  writeFileSync(config, readFileSync(source, "utf8").replace('audience: ["386000000000000001"]', 'audience: ["denied-audience"]'));
  docker("stop", original);
  docker("run", "--detach", "--name", replacement, "--network", `${project}_default`, "--network-alias", `powersync-${service}`, "--env", "POWERSYNC_CONFIG_PATH=/config/service.yaml", "--volume", `${config}:/config/service.yaml:ro`, "--volume", `${sync}:/config/sync-config.yaml:ro`, "journeyapps/powersync-service:1.23.3");
  return () => {
    try { docker("rm", "--force", replacement); } catch { /* replacement may already have exited */ }
    docker("start", original);
    rmSync(directory, { recursive: true, force: true });
  };
}

async function login(page: import("@playwright/test").Page, username = process.env.M04_E2E_USERNAME ?? "", passwordValue = process.env.M04_E2E_PASSWORD ?? "") {
  await page.goto("/");
  await page.getByRole("button", { name: "Sign in", exact: true }).click({ timeout: 90_000 });
  await expect(page).toHaveURL(/identity\.e2e\.gizway\.test/i);
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
  await expect(page.getByTestId("regional-price-count")).toHaveText("4");
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
    const response = await fetch("/_api/gizway/v1/chat/completions", {
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
  const topUpAPI = "**/_api/gizpay/account/v1/accounts/*/topups";
  const retrying = async (route: import("@playwright/test").Route) => {
    await route.fulfill({ status: 503, contentType: "application/json", body: JSON.stringify({ error: { code: "temporarily_unavailable", message: "retry this command" } }) });
  };
  await page.route(topUpAPI, retrying);
  await page.getByRole("button", { name: "Add credit" }).first().click();
  await page.getByRole("button", { name: "50,000", exact: true }).click();
  await page.getByRole("button", { name: "Confirm top-up" }).click();
  await expect(page.getByTestId("runtime-state")).toHaveAttribute("data-state", "command_pending", { timeout: 30_000 });
  await expect(page.getByTestId("runtime-state")).toBeVisible();
  await page.unroute(topUpAPI, retrying);
  await expect(page.getByRole("dialog")).toHaveCount(0, { timeout: 60_000 });
  await expect(page.getByTestId("runtime-state")).toHaveCount(0);

  await page.route(topUpAPI, async (route) => {
    await route.fulfill({ status: 409, contentType: "application/json", body: JSON.stringify({ error: { code: "test_command_rejected", message: "deterministic rejection" } }) });
  });
  await page.getByRole("button", { name: "Add credit" }).first().click();
  await page.getByRole("button", { name: "50,000", exact: true }).click();
  await page.getByRole("button", { name: "Confirm top-up" }).click();
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
  const postgres = composeContainer("postgres-global");
  try {
    docker("exec", postgres, "psql", "-U", "postgres", "-d", "gizway", "-c", "UPDATE gizway.model_listings SET availability='unavailable'");
    await expect(page.getByTestId("public-model-catalog-state")).toHaveAttribute("data-state", "empty", { timeout: 60_000 });
    await expect(page.getByTestId("public-model-catalog-state")).toHaveText("0");
    await expect(page.getByTestId("public-gizway-catalog-notice")).toHaveAttribute("data-state", "empty");
    await expect(page.getByTestId("public-gizway-catalog-notice")).toBeVisible();
  } finally {
    docker("exec", postgres, "psql", "-U", "postgres", "-d", "gizway", "-c", "UPDATE gizway.model_listings SET availability='available'");
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
  const pay = composeContainer("powersync-gizpay");
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
  let restorePay: (() => void) | undefined;
  let restoreWay: (() => void) | undefined;
  try {
    restorePay = startDeniedPowerSync("gizpay");
    restoreWay = startDeniedPowerSync("cn");
    await page.goto("/");
    await expect(page.getByTestId("public-gizway-sync-state")).toHaveAttribute("data-state", "denied", { timeout: 60_000 });
    await expect(page.getByTestId("public-gizpay-sync-state")).toHaveAttribute("data-state", "denied", { timeout: 60_000 });
    await expect(page.getByTestId("public-catalog-state")).toHaveAttribute("data-state", "denied");
    await expect(page.getByTestId("runtime-state")).toBeVisible();
    await expect(page.getByRole("heading", { name: "denied" })).toBeVisible();
  } finally {
    restoreWay?.();
    restorePay?.();
  }
});
