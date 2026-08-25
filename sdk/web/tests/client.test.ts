import { describe, expect, it, vi } from "vitest";
import { AuthenticationConfigurationError, createGizWayClient } from "../src";
import { authenticatedDatabaseNames, openDatabasePair } from "../src/powersync/database";
import { createPairCloser, MutationCoordinator } from "../src/powersync/lifecycle";

describe("paired database lifecycle", () => {
  it("rolls back the first database when the second open fails", async () => {
    const close = vi.fn(async () => undefined);
    let calls = 0;
    const factory = vi.fn(async () => {
      calls++;
      if (calls === 2) throw new Error("second open failed");
      return { close };
    });
    await expect(openDatabasePair({ gizpay: "pay", gizway: "way" }, factory as never)).rejects.toThrow("second open failed");
    expect(close).toHaveBeenCalledOnce();
  });
  it("attempts both disconnects and closes, aggregates failures, and is idempotent", async () => {
    const pay = { disconnect: vi.fn(async () => { throw new Error("pay disconnect"); }), disconnectAndClear: vi.fn(), close: vi.fn(async () => undefined) };
    const way = { disconnect: vi.fn(async () => undefined), disconnectAndClear: vi.fn(), close: vi.fn(async () => { throw new Error("way close"); }) };
    const close = createPairCloser(pay as never, way as never);
    await expect(close()).rejects.toBeInstanceOf(AggregateError);
    expect(pay.disconnect).toHaveBeenCalledOnce();
    expect(way.disconnect).toHaveBeenCalledOnce();
    expect(pay.close).toHaveBeenCalledOnce();
    expect(way.close).toHaveBeenCalledOnce();
    await expect(close()).resolves.toBeUndefined();
  });
  it("cancels every pending mutation waiter", async () => {
    const coordinator = new MutationCoordinator();
    const first = coordinator.run("a", "1", async () => undefined);
    const second = coordinator.run("b", "2", async () => undefined);
    coordinator.cancelAll(new Error("closed"));
    await expect(first).rejects.toThrow("closed");
    await expect(second).rejects.toThrow("closed");
  });
});

describe("client OAuth ownership", () => {
  const runtime = {
    site: { hostname: "global.example.test" },
    identity: { issuer: "https://identity.example.test", audience: "project" },
    services: { public_catalog_token_url: "https://global.example.test/auth/catalog-token", gizpay_powersync_url: "https://global.example.test/_sync/gizpay", gizpay_api_url: "https://global.example.test", gizway_powersync_url: "https://global.example.test/_sync/gizway", gizway_api_url: "https://global.example.test" },
  };

  it("creates a public-only client without OAuth or session storage", async () => {
    const client = await createGizWayClient({
      entryOrigin: "https://global.example.test",
      region: "global",
      fetch: async () => Response.json(runtime),
    });
    expect(client.config.identity).toEqual(runtime.identity);
    await expect(client.auth.beginLogin()).rejects.toBeInstanceOf(AuthenticationConfigurationError);
    await expect(client.close()).resolves.toBeUndefined();
  });

  it("includes the consumer client ID in authenticated database namespaces", async () => {
    const first = await authenticatedDatabaseNames("global", runtime.identity.issuer, "consumer-a", "subject");
    const second = await authenticatedDatabaseNames("global", runtime.identity.issuer, "consumer-b", "subject");
    expect(first).not.toEqual(second);
  });
});
