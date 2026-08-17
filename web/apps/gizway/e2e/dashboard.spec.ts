import { expect, test } from "@playwright/test";

async function openMobileNavigation(page: import("@playwright/test").Page) {
  const menu = page.getByRole("button", { name: "Open navigation" });
  if (await menu.isVisible()) await menu.click();
}

test.beforeEach(async ({ page }) => {
  await page.goto("/?mode=fake&scenario=active-payg&authenticated=1");
  await expect(page.getByRole("heading", { name: /good afternoon/i })).toBeVisible();
});

test("keeps the reviewed navigation and regional user flows", async ({ page }) => {
  await openMobileNavigation(page);
  for (const name of ["Overview", "Models", "Usage", "Subscription Keys", "Provider Keys", "Credits", "Settings"]) {
    await expect(page.getByRole("button", { name, exact: true })).toBeVisible();
  }
  await page.getByRole("button", { name: "Models", exact: true }).click();
  await expect(page.getByRole("heading", { name: "Models", exact: true })).toBeVisible();
  await expect(page.getByText(/input_tokens/i).first()).toBeVisible();
  await expect(page.getByText(/output_tokens/i).first()).toBeVisible();
  await expect(page.getByText(/rpm/i)).toHaveCount(0);
});

test("renders private data and command feedback from PowerSync state", async ({ page }) => {
  await openMobileNavigation(page);
  await page.getByRole("button", { name: "Subscription Keys", exact: true }).click();
  await expect(page.getByText(/last used/i)).toBeVisible();
  await openMobileNavigation(page);
  await page.getByRole("button", { name: "Provider Keys", exact: true }).click();
  await expect(page.getByText(/earnings settle to your default merchant/i)).toBeVisible();
  await openMobileNavigation(page);
  await page.getByRole("button", { name: "Credits", exact: true }).click();
  await expect(page.getByRole("button", { name: /add credit/i }).first()).toBeVisible();
});

test("overview shows PAYG, dual sync and recent AI Orders without placeholder facts", async ({ page }) => {
  await expect(page.getByTestId("payg-status")).toHaveAttribute("data-state", "active");
  await expect(page.getByTestId("overview-gizpay-sync")).toHaveAttribute("data-state", "ready");
  await expect(page.getByTestId("overview-gizway-sync")).toHaveAttribute("data-state", "ready");
  await expect(page.getByText("Recent AI Orders", { exact: true })).toBeVisible();
  await expect(page.getByText("ord_01", { exact: true })).toBeVisible();
  await expect(page.getByText("12.8%", { exact: true })).toHaveCount(0);
  await expect(page.getByText("Updated just now", { exact: true })).toHaveCount(0);
});

test("usage filters AI Orders and expands normalized token metrics", async ({ page }) => {
  await openMobileNavigation(page);
  await page.getByRole("button", { name: "Usage", exact: true }).click();
  await page.getByLabel("Time range").selectOption("all");
  const modelFilter = page.getByLabel("Model filter");
  const selectedModel = await modelFilter.locator("option").nth(1).getAttribute("value");
  expect(selectedModel).toBeTruthy();
  await modelFilter.selectOption(selectedModel ?? "");
  const order = page.getByRole("row").filter({ has: page.getByRole("button", { name: "Show metrics" }) }).first();
  await expect(order).toBeVisible();
  await order.getByRole("button", { name: "Show metrics" }).click();
  await expect(page.getByText("input_tokens", { exact: true })).toBeVisible();
  await expect(page.getByText("output_tokens", { exact: true })).toBeVisible();
});

test("usage renders pending and failed Order states from PowerSync", async ({ page }) => {
  await openMobileNavigation(page);
  await page.getByRole("button", { name: "Usage", exact: true }).click();
  await expect(page.getByText("pending", { exact: true })).toBeVisible();
  await expect(page.getByText("failed", { exact: true })).toBeVisible();
  await expect(page.getByText("streaming", { exact: true })).toHaveCount(0);
});

test("credits and settings expose their complete synchronized read models", async ({ page }) => {
  await openMobileNavigation(page);
  await page.getByRole("button", { name: "Credits", exact: true }).click();
  for (const heading of ["Ledger transactions", "Top-ups", "Charges", "Commissions"]) {
    await expect(page.getByText(heading, { exact: true })).toBeVisible();
  }
  await expect(page.getByText("ord_01", { exact: false })).toBeVisible();
  await expect(page.getByText("charge_01", { exact: false })).toBeVisible();
  await openMobileNavigation(page);
  await page.getByRole("button", { name: "Settings", exact: true }).click();
  await expect(page.getByText("User identity", { exact: true })).toBeVisible();
  await expect(page.getByText("Default Merchant", { exact: true })).toBeVisible();
  await expect(page.getByText("Authenticated", { exact: true })).toBeVisible();
  await expect(page.getByText("mrc_idy_default", { exact: true })).toBeVisible();
});

test("provider key last use comes from synchronized data", async ({ page }) => {
  await openMobileNavigation(page);
  await page.getByRole("button", { name: "Provider Keys", exact: true }).click();
  await expect(page.getByText("Recently", { exact: true })).toHaveCount(0);
  await expect(page.getByText(/8\/(16|17)\/2026|(16|17)\/08\/2026/).first()).toBeVisible();
});

test("dashboard keeps the reviewed visual baseline", async ({ page }) => {
  await expect(page).toHaveScreenshot("dashboard.png", { fullPage: true });
});
