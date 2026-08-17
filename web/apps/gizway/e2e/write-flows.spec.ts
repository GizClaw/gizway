import { expect, test } from "@playwright/test";

test.beforeEach(async ({ page }, testInfo) => {
  test.skip(process.env.M04_REAL_E2E === "1", "fake write coverage is separate from the real Compose closed loop");
  test.skip(testInfo.project.name.endsWith("mobile"), "desktop interaction contract; mobile navigation is covered separately");
  await page.goto("/?mode=fake&scenario=active-payg&authenticated=1");
  await expect(page.getByRole("heading", { name: /good afternoon/i })).toBeVisible();
});

test("creates and revokes a named Subscription Key", async ({ page }) => {
  await page.getByRole("button", { name: "Subscription Keys", exact: true }).click();
  await page.getByRole("button", { name: "Create Key", exact: true }).click();
  await page.getByLabel("Key name").fill("Browser test key");
  await page.getByRole("button", { name: "Create Key", exact: true }).last().click();
  const row = page.getByRole("row").filter({ hasText: "Browser test key" });
  await expect(row).toBeVisible();
  await row.getByRole("button", { name: "Revoke" }).click();
  await expect(row).toContainText("revoked");
});

test("creates, reprices and disables a Provider Key", async ({ page }) => {
  await page.getByRole("button", { name: "Provider Keys", exact: true }).click();
  await page.getByRole("button", { name: "Add Provider Key", exact: true }).first().click();
  const createDialog = page.getByRole("dialog", { name: "Add Provider Key" });
  await createDialog.locator("select").first().selectOption({ index: 1 });
  await createDialog.getByLabel("Key name", { exact: true }).fill("Browser provider");
  await createDialog.getByLabel("Provider Key", { exact: true }).fill("provider-secret-browser-test");
  await createDialog.getByLabel("Input price / 1M", { exact: true }).fill("20");
  await createDialog.getByLabel("Output price / 1M", { exact: true }).fill("40");
  await createDialog.getByRole("button", { name: "Add Provider Key", exact: true }).click();
  await expect(page.getByText("Browser provider")).toBeVisible();
  await page.getByRole("button", { name: "Configure prices" }).last().click();
  const priceDialog = page.getByRole("dialog", { name: "Configure purchase prices" });
  await priceDialog.getByLabel("Input price / 1M", { exact: true }).fill("30");
  await priceDialog.getByRole("button", { name: "Save prices" }).click();
  await page.getByRole("button", { name: "Configure prices" }).last().click();
  const savedDialog = page.getByRole("dialog", { name: "Configure purchase prices" });
  await expect(savedDialog.getByLabel("Input price / 1M", { exact: true })).toHaveValue("30");
  await expect(savedDialog.getByLabel("Output price / 1M", { exact: true })).toHaveValue("40");
  await savedDialog.getByLabel("Close Provider Key dialog").click();
  await page.getByRole("button", { name: "Disable Key" }).last().click();
  await expect(page.getByText("Browser provider").locator("xpath=ancestor::*[contains(@class,'rounded-2xl')][1]")).toContainText("disabled");
});

test("submits a Fake Channel top-up", async ({ page }) => {
  await page.getByRole("button", { name: "Credits", exact: true }).click();
  await page.getByRole("button", { name: "Add credit" }).first().click();
  await page.getByRole("button", { name: "50,000", exact: true }).click();
  await page.getByRole("button", { name: "Confirm top-up" }).click();
  await expect(page.getByText("334,650")).toBeVisible();
});
