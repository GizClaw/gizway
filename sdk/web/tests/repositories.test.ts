import { expect, it } from "vitest";
import { PowerSyncGizPayRepository } from "../src/powersync/gizpay/repository";
import { PowerSyncGizWayRepository } from "../src/powersync/gizway/repository";
import { gizPaySchema } from "../src/powersync/gizpay/schema";
import { gizWaySchema } from "../src/powersync/gizway/schema";

it("keeps fake seeding and raw databases out of repository/public methods", async () => {
  expect(Object.getOwnPropertyNames(PowerSyncGizPayRepository.prototype)).toEqual(expect.arrayContaining(["getSnapshot", "createTopUp", "createSubscription", "createSubscriptionKey", "revokeSubscriptionKey"]));
  expect(Object.getOwnPropertyNames(PowerSyncGizPayRepository.prototype)).not.toContain("seed");
  expect(Object.getOwnPropertyNames(PowerSyncGizWayRepository.prototype)).not.toContain("seed");
  const root = await import("../src/index");
  expect(root).not.toHaveProperty("PowerSyncDatabase");
  expect(root).not.toHaveProperty("gizPaySchema");
});

it("retains the supported PowerSync schema families", () => {
  expect(String(gizPaySchema)).toBeTruthy();
  expect(String(gizWaySchema)).toBeTruthy();
});
