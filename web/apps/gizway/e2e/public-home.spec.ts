import { expect, test } from "@playwright/test";

test("anonymous Catalog is public PowerSync data and contains no private workspace data", async ({ page }, testInfo) => {
  await page.goto("/");
  await expect(page.getByRole("heading", { name: /multi-model ai gateway/i })).toBeVisible();
  await expect(page.getByRole("link", { name: /sign in/i }).first()).toBeVisible();
  await expect(page.getByText(/model catalog/i).first()).toBeVisible();
  await expect(page.getByText(/available credit/i)).toHaveCount(0);
  await expect(page.getByText(/global site|cn site/i)).toHaveCount(0);
  await expect(page.getByRole("link", { name: /playground/i })).toHaveCount(0);
  await expect(page.getByText(testInfo.project.name.startsWith("cn-") ? "DeepSeek V3.1" : "GPT-5", { exact: true })).toBeVisible();
  await expect(page.getByText(/credits \/ 1M/i).first()).toBeVisible();
});

test("public homepage keeps the reviewed visual baseline", async ({ page }) => {
  await page.goto("/");
  await expect(page).toHaveScreenshot("public-home.png", { fullPage: true });
});
