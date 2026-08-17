import { expect, test } from "@playwright/test";

test("mobile navigation is keyboard and touch accessible", async ({ page }, testInfo) => {
  test.skip(!testInfo.project.name.endsWith("mobile"), "mobile-only contract");
  await page.goto("/?mode=fake&scenario=active-payg&authenticated=1");
  const menu = page.getByRole("button", { name: "Open navigation" });
  await expect(menu).toBeVisible();
  await menu.focus();
  await page.keyboard.press("Enter");
  await expect(page.getByRole("button", { name: "Subscription Keys", exact: true })).toBeVisible();
  await page.getByRole("button", { name: "Close navigation" }).click();
  await expect(page.getByRole("button", { name: "Close navigation" })).toHaveCount(0);
});

test("dialogs trap focus, close with Escape and restore the trigger", async ({ page }, testInfo) => {
  test.skip(testInfo.project.name.endsWith("mobile"), "desktop keyboard contract");
  await page.goto("/?mode=fake&scenario=active-payg&authenticated=1");

  await page.getByRole("button", { name: "Subscription Keys", exact: true }).click();
  const createKey = page.getByRole("button", { name: "Create Key", exact: true }).first();
  await createKey.click();
  const keyDialog = page.getByRole("dialog", { name: "Create Subscription Key" });
  const keyName = keyDialog.getByLabel("Key name");
  await expect(keyName).toBeFocused();
  await keyName.fill("Keyboard test");
  await keyName.press("Shift+Tab");
  await expect(keyDialog.getByRole("button", { name: "Close Subscription Key dialog" })).toBeFocused();
  await page.keyboard.press("Shift+Tab");
  await expect(keyDialog.getByRole("button", { name: "Create Key" })).toBeFocused();
  await page.keyboard.press("Tab");
  await expect(keyDialog.getByRole("button", { name: "Close Subscription Key dialog" })).toBeFocused();
  await page.keyboard.press("Escape");
  await expect(keyDialog).toHaveCount(0);
  await expect(createKey).toBeFocused();

  await page.getByRole("button", { name: "Provider Keys", exact: true }).click();
  const addProvider = page.getByRole("button", { name: "Add Provider Key", exact: true }).first();
  await addProvider.click();
  const providerDialog = page.getByRole("dialog", { name: "Add Provider Key" });
  await expect(providerDialog.locator("select").first()).toBeFocused();
  await page.keyboard.press("Escape");
  await expect(providerDialog).toHaveCount(0);
  await expect(addProvider).toBeFocused();

  await page.getByRole("button", { name: "Credits", exact: true }).click();
  const topUp = page.getByRole("button", { name: "Add credit", exact: true }).first();
  await topUp.click();
  const topUpDialog = page.getByRole("dialog", { name: "Add GizWay credit" });
  await expect(topUpDialog.getByLabel("Credit amount")).toBeFocused();
  await page.keyboard.press("Escape");
  await expect(topUpDialog).toHaveCount(0);
  await expect(topUp).toBeFocused();
});
