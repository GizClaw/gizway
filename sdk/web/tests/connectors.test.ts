import { describe, expect, it, vi } from "vitest";
import type { CommonPowerSyncDatabase } from "@powersync/web";
import { APIConnector, retryAfterMilliseconds } from "../src/powersync/connectors/base";
import { GizPayConnector } from "../src/powersync/connectors/gizpay";
import { GizWayConnector } from "../src/powersync/connectors/gizway";

class Connector extends APIConnector {
  protected mapEntry(table: string, id: string) { return { method: "POST", path: `/${table}/${id}`, body: { id } }; }
}
function database(complete = vi.fn()) {
  return { getCrudBatch: async () => ({ crud: [{ table: "items", id: "1", op: "PUT", opData: {} }], complete }) } as unknown as CommonPowerSyncDatabase;
}

describe("upload failure rules", () => {
  it("honors Retry-After and leaves the batch pending", async () => {
    const sleep = vi.fn(async () => undefined), complete = vi.fn();
    const connector = new Connector({ endpoint: "https://sync", apiBaseURL: "https://api", token: async () => "t", sleep, fetcher: async () => new Response("", { status: 429, headers: { "Retry-After": "2" } }) });
    await expect(connector.uploadData(database(complete))).rejects.toThrow("temporary upload failure");
    expect(sleep).toHaveBeenCalledWith(2_000);
    expect(complete).not.toHaveBeenCalled();
  });
  it("completes deterministic API rejection and reports it", async () => {
    const complete = vi.fn(), rejected = vi.fn();
    const connector = new Connector({ endpoint: "https://sync", apiBaseURL: "https://api", token: async () => "t", onMutationError: rejected, fetcher: async () => Response.json({ error: { code: "invalid_item", message: "bad" } }, { status: 422 }) });
    await connector.uploadData(database(complete));
    expect(rejected).toHaveBeenCalledWith(expect.objectContaining({ code: "invalid_item", status: 422 }));
    expect(complete).toHaveBeenCalledOnce();
  });
  it("completes a deterministic rejection before notifying a throwing observer", async () => {
    const complete = vi.fn();
    const connector = new Connector({ endpoint: "https://sync", apiBaseURL: "https://api", token: async () => "t", onMutationError: () => { throw new Error("observer failed"); }, fetcher: async () => Response.json({ error: { code: "invalid_item", message: "bad" } }, { status: 422 }) });
    await expect(connector.uploadData(database(complete))).rejects.toThrow("observer failed");
    expect(complete).toHaveBeenCalledOnce();
  });
  it("completes a successful upload before notifying a throwing observer", async () => {
    const complete = vi.fn();
    const connector = new Connector({ endpoint: "https://sync", apiBaseURL: "https://api", token: async () => "t", onMutationSuccess: () => { throw new Error("observer failed"); }, fetcher: async () => new Response(null, { status: 204 }) });
    await expect(connector.uploadData(database(complete))).rejects.toThrow("observer failed");
    expect(complete).toHaveBeenCalledOnce();
  });
  it("leaves malformed errors and network/5xx failures pending", async () => {
    for (const fetcher of [async () => new Response("bad", { status: 400 }), async () => new Response("", { status: 503 }), async () => { throw new Error("offline"); }]) {
      const complete = vi.fn();
      const connector = new Connector({ endpoint: "https://sync", apiBaseURL: "https://api", token: async () => "t", fetcher });
      await expect(connector.uploadData(database(complete))).rejects.toThrow();
      expect(complete).not.toHaveBeenCalled();
    }
  });
});

it("parses delta and date Retry-After values", () => {
  expect(retryAfterMilliseconds("1.5", 0)).toBe(1_500);
  expect(retryAfterMilliseconds("Thu, 01 Jan 1970 00:00:02 GMT", 1_000)).toBe(1_000);
});

describe("supported mutation mappings", () => {
  const cases = [
    [GizPayConnector, "my_merchants", "PATCH", { public_name: "Merchant" }, "/account/v1/merchants/id"],
    [GizPayConnector, "my_subscriptions", "PUT", { product_id: "product", account_id: "account", terms_version: "v1" }, "/account/v1/products/product/subscriptions"],
    [GizPayConnector, "my_subscriptions", "PATCH", { status: "paused" }, "/account/v1/subscriptions/id"],
    [GizPayConnector, "my_subscription_keys", "PUT", { subscription_id: "subscription", name: "key" }, "/account/v1/subscriptions/subscription/keys"],
    [GizPayConnector, "my_subscription_keys", "PATCH", { subscription_id: "subscription", status: "revoked" }, "/account/v1/subscriptions/subscription/keys/id/revoke"],
    [GizPayConnector, "my_topups", "PUT", { account_id: "account", channel: "fake", external_reference: "ref", amount_microcredits: 1 }, "/account/v1/accounts/account/topups"],
    [GizWayConnector, "my_provider_keys", "PUT", { provider_id: "provider", name: "key", key: "secret", status: "active", prices_json: "[]" }, "/user/v1/providers/provider/keys"],
    [GizWayConnector, "my_provider_keys", "PATCH", { status: "disabled" }, "/user/v1/provider-keys/id/disable"],
    [GizWayConnector, "my_provider_keys", "PATCH", { prices_json: "[]" }, "/user/v1/provider-keys/id/prices"],
  ] as const;
  it.each(cases)("maps %s %s %s", async (ConnectorType, table, op, opData, expectedPath) => {
    let requestURL = "";
    const complete = vi.fn(async () => undefined);
    const connector = new ConnectorType({ endpoint: "https://sync", apiBaseURL: "https://api", token: async () => "token", fetcher: async (input) => {
      requestURL = String(input);
      return new Response(null, { status: 204 });
    } });
    const db = { getCrudBatch: async () => ({ crud: [{ table, id: "id", op, opData }], complete }), getAll: async () => [] } as unknown as CommonPowerSyncDatabase;
    await connector.uploadData(db);
    expect(requestURL).toBe(`https://api${expectedPath}`);
    expect(complete).toHaveBeenCalledOnce();
  });
  it("rejects unsupported mutations without completing the batch", async () => {
    const complete = vi.fn(async () => undefined);
    const connector = new GizWayConnector({ endpoint: "https://sync", apiBaseURL: "https://api", token: async () => "token" });
    const db = { getCrudBatch: async () => ({ crud: [{ table: "models", id: "id", op: "PUT", opData: {} }], complete }) } as unknown as CommonPowerSyncDatabase;
    await expect(connector.uploadData(db)).rejects.toThrow("unsupported GizWay local mutation");
    expect(complete).not.toHaveBeenCalled();
  });
});
