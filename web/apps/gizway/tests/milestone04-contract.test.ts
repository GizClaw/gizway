import { describe, expect, test } from "vitest";
import { gizPaySchema } from "@/data/powersync/gizpay/schema";
import { gizWaySchema } from "@/data/powersync/gizway/schema";

describe("Milestone 04 browser data contract", () => {
  test("uses the central GizPay PowerSync tables directly", () => {
    expect(gizPaySchema.tables.map((table) => table.name).sort()).toEqual([
      "available_products",
      "my_accounts",
      "my_balances",
      "my_charges",
      "my_commissions",
      "my_merchants",
      "my_products",
      "my_profile",
      "my_service_accounts",
      "my_subscription_keys",
      "my_subscriptions",
      "my_topups",
      "my_transactions",
      "product_listings",
    ].sort());
    const serialized = JSON.stringify(gizPaySchema.toJSON());
    for (const field of ["email", "display_name", "merchant_id", "name", "last_used_at"]) {
      expect(serialized).toContain(field);
    }
    for (const oldTable of ["user_profiles", "credit_balances", "products", "subscriptions", "subscription_keys", "ledger_items"]) {
      expect(gizPaySchema.tables.map((table) => table.name)).not.toContain(oldTable);
    }
  });

  test("uses the regional GizWay PowerSync tables directly", () => {
    expect(gizWaySchema.tables.map((table) => table.name).sort()).toEqual([
      "model_customer_prices",
      "model_listings",
      "models",
      "my_ai_orders",
      "my_ai_usage",
      "my_provider_keys",
      "providers",
    ].sort());
    const serialized = JSON.stringify(gizWaySchema.toJSON());
    for (const field of ["subscription_key_id", "name", "last_used_at", "earned_microcredits", "prices_json", "featured"]) {
      expect(serialized).toContain(field);
    }
    for (const oldTable of ["usage_points", "ai_orders", "provider_keys"]) {
      expect(gizWaySchema.tables.map((table) => table.name)).not.toContain(oldTable);
    }
  });
});
