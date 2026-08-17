import { afterEach, describe, expect, test, vi } from "vitest";
import { readFile } from "node:fs/promises";
import { clearSession, humanToken, logoutURL } from "@/data/runtime/auth";
import { connectPowerSyncService, createDatabaseCloser, ensurePAYGSubscription, maintainPAYGSubscription, maintainPowerSyncService, MutationCoordinator, openDatabasePair, ReadOnlyCatalogConnector, waitForInitialSync, watchCatalogDatabases, watchDatabases } from "@/data/runtime/real-provider";
import { validateCatalogJWT } from "@/data/runtime/config";
import { PowerSyncGizPayRepository } from "@/data/powersync/gizpay/repository";
import { PowerSyncGizWayRepository } from "@/data/powersync/gizway/repository";
import { GizPayConnector } from "@/data/powersync/connectors/gizpay";
import { readDashboardSnapshots } from "@/data/dashboard-snapshot";
import type { PublicRuntimeConfig } from "@/data/runtime/config";

function installSessionStorage() {
  const values = new Map<string, string>();
  const storage = {
    getItem: (key: string) => values.get(key) ?? null,
    setItem: (key: string, value: string) => values.set(key, value),
    removeItem: (key: string) => values.delete(key),
    clear: () => values.clear(),
    key: (index: number) => Array.from(values.keys())[index] ?? null,
    get length() { return values.size; },
  } satisfies Storage;
  vi.stubGlobal("sessionStorage", storage);
  return storage;
}

afterEach(() => vi.unstubAllGlobals());

describe("M04 runtime authentication", () => {
  test("builds an RP-initiated logout URL before clearing all local tokens", () => {
    const storage = installSessionStorage();
    storage.setItem("gizway.oidc.tokens", JSON.stringify({ access_token: "access", id_token: "id-token", expires_at: Date.now() + 60_000 }));
    storage.setItem("gizway.oidc.transaction", "pending");
    const config = {
      identity: {
        issuer: "https://identity.example.test",
        client_id: "browser-client",
        post_logout_redirect_uri: "https://global.example.test/",
      },
    } as PublicRuntimeConfig;
    const url = new URL(logoutURL(config));
    expect(url.pathname).toBe("/oidc/v1/end_session");
    expect(url.searchParams.get("client_id")).toBe("browser-client");
    expect(url.searchParams.get("id_token_hint")).toBe("id-token");
    expect(url.searchParams.get("post_logout_redirect_uri")).toBe("https://global.example.test/");
    clearSession();
    expect(storage.length).toBe(0);
  });

  test("redirects to a fresh login when token refresh fails", async () => {
    const storage = installSessionStorage();
    storage.setItem("gizway.oidc.tokens", JSON.stringify({ access_token: "expired", refresh_token: "refresh", expires_at: 0 }));
    const assign = vi.fn();
    vi.stubGlobal("location", { assign });
    vi.stubGlobal("fetch", vi.fn(async () => new Response("", { status: 401 })));
    const config = {
      identity: {
        issuer: "https://identity.example.test",
        client_id: "browser-client",
        redirect_uri: "https://global.example.test/auth/callback",
        audience: "project-id",
      },
    } as PublicRuntimeConfig;
    await expect(humanToken(config)).resolves.toBeUndefined();
    expect(storage.getItem("gizway.oidc.tokens")).toBeNull();
    expect(assign).toHaveBeenCalledOnce();
    const login = new URL(String(assign.mock.calls[0]?.[0]));
    expect(login.pathname).toBe("/oauth/v2/authorize");
    expect(login.searchParams.get("client_id")).toBe("browser-client");
    expect(storage.getItem("gizway.oidc.transaction")).not.toBeNull();
  });

  test("redirects to login when an expired session has no refresh token", async () => {
    const storage = installSessionStorage();
    storage.setItem("gizway.oidc.tokens", JSON.stringify({ access_token: "expired", expires_at: 0 }));
    const assign = vi.fn();
    vi.stubGlobal("location", { assign });
    const config = {
      identity: {
        issuer: "https://identity.example.test", client_id: "browser-client",
        redirect_uri: "https://global.example.test/auth/callback", audience: "project-id",
      },
    } as PublicRuntimeConfig;
    await expect(humanToken(config)).resolves.toBeUndefined();
    expect(storage.getItem("gizway.oidc.tokens")).toBeNull();
    expect(assign).toHaveBeenCalledOnce();
  });

  test("coalesces concurrent failed refreshes into one login redirect", async () => {
    const storage = installSessionStorage();
    storage.setItem("gizway.oidc.tokens", JSON.stringify({ access_token: "expired", refresh_token: "refresh", expires_at: 0 }));
    const assign = vi.fn();
    vi.stubGlobal("location", { assign });
    const fetchMock = vi.fn(async () => new Response("", { status: 503 }));
    vi.stubGlobal("fetch", fetchMock);
    const config = {
      identity: {
        issuer: "https://identity.example.test", client_id: "browser-client",
        redirect_uri: "https://global.example.test/auth/callback", audience: "project-id",
      },
    } as PublicRuntimeConfig;
    await Promise.all([humanToken(config), humanToken(config)]);
    expect(fetchMock).toHaveBeenCalledOnce();
    expect(assign).toHaveBeenCalledOnce();
  });

  test("redirects to login when the refresh request has a transport failure", async () => {
    const storage = installSessionStorage();
    storage.setItem("gizway.oidc.tokens", JSON.stringify({ access_token: "expired", refresh_token: "refresh", expires_at: 0 }));
    const assign = vi.fn();
    vi.stubGlobal("location", { assign });
    vi.stubGlobal("fetch", vi.fn(async () => { throw new TypeError("network unavailable"); }));
    const config = {
      identity: {
        issuer: "https://identity.example.test", client_id: "browser-client",
        redirect_uri: "https://global.example.test/auth/callback", audience: "project-id",
      },
    } as PublicRuntimeConfig;
    await expect(humanToken(config)).resolves.toBeUndefined();
    expect(storage.getItem("gizway.oidc.tokens")).toBeNull();
    expect(assign).toHaveBeenCalledOnce();
  });

  test("does not restore a refresh response after the user clears the session", async () => {
    const storage = installSessionStorage();
    storage.setItem("gizway.oidc.tokens", JSON.stringify({ access_token: "expired", refresh_token: "refresh", expires_at: 0 }));
    let release!: (response: Response) => void;
    vi.stubGlobal("fetch", vi.fn(() => new Promise<Response>((resolve) => { release = resolve; })));
    const config = { identity: { issuer: "https://identity.example.test", client_id: "browser-client" } } as PublicRuntimeConfig;
    const pending = humanToken(config);
    clearSession();
    release(new Response(JSON.stringify({ access_token: "late", refresh_token: "late-refresh", expires_in: 300 }), { status: 200 }));
    await expect(pending).resolves.toBeUndefined();
    expect(storage.getItem("gizway.oidc.tokens")).toBeNull();
  });
});

describe("M04 local command confirmation", () => {
  test("resolves only after the matching Connector upload succeeds", async () => {
    const mutations = new MutationCoordinator();
    const pending = mutations.run("my_topups", "topup-1", async () => undefined);
    let settled = false;
    void pending.finally(() => { settled = true; });
    await Promise.resolve();
    expect(settled).toBe(false);
    mutations.succeed({ table: "my_topups", id: "topup-1", status: 201 });
    await expect(pending).resolves.toMatchObject({ status: 201 });
  });

  test("turns a typed deterministic upload failure into a rejected command", async () => {
    const mutations = new MutationCoordinator();
    const pending = mutations.run("my_subscription_keys", "key-1", async () => undefined);
    await Promise.resolve();
    mutations.reject("my_subscription_keys", "key-1", new Error("resource_id_conflict: conflict"));
    await expect(pending).rejects.toThrow("resource_id_conflict");
  });

  test("keeps a retrying mutation pending beyond the old 60-second UI deadline", async () => {
    vi.useFakeTimers();
    const mutations = new MutationCoordinator();
    const pending = mutations.run("my_topups", "topup-retrying", async () => undefined);
    let settled = false;
    void pending.finally(() => { settled = true; });
    await vi.advanceTimersByTimeAsync(61_000);
    expect(settled).toBe(false);
    mutations.succeed({ table: "my_topups", id: "topup-retrying", status: 200 });
    await expect(pending).resolves.toMatchObject({ status: 200 });
    vi.useRealTimers();
  });
});

describe("M04 synchronized billing read models", () => {
  test("returns each Dashboard repository independently when the other local query fails", async () => {
    const waySnapshot = { models: [], usage: [], orders: [], providerKeys: [] };
    const payFailure = {
      gizpay: { getSnapshot: vi.fn(async () => { throw new Error("GizPay query failed"); }) },
      gizway: { getSnapshot: vi.fn(async () => waySnapshot) },
    };
    await expect(readDashboardSnapshots(payFailure as never, "global")).resolves.toEqual({ way: waySnapshot, errors: { gizpay: "GizPay query failed" } });
    const paySnapshot = { keys: [] };
    const wayFailure = {
      gizpay: { getSnapshot: vi.fn(async () => paySnapshot) },
      gizway: { getSnapshot: vi.fn(async () => { throw new Error("GizWay query failed"); }) },
    };
    await expect(readDashboardSnapshots(wayFailure as never, "global")).resolves.toEqual({ pay: paySnapshot, errors: { gizway: "GizWay query failed" } });
  });

  test("derives Credit activity from AI Order gross Credit, not token quantity", async () => {
    const database = {
      getAll: vi.fn(async (query: string) => {
        if (query.includes("FROM models m")) return [{ id: "model-1", name: "Model One", provider_id: "provider-1", provider: "Provider", family: "chat", description: "", latency: "fast", context: "1K", accent: "#000" }];
        if (query.includes("FROM model_customer_prices")) return [];
        if (query.includes("FROM my_ai_usage")) return [
          { order_id: "order-1", metric: "input_tokens", quantity: 90_000 },
          { order_id: "order-1", metric: "output_tokens", quantity: 10_000 },
        ];
        if (query.includes("FROM my_ai_orders")) return [{ id: "order-1", model: "Model One", subscription_key_id: "key-1", gross_microcredits: 321, status: "charged", created_at: "2026-08-17T10:00:00Z" }];
        if (query.includes("FROM my_provider_keys")) return [];
        throw new Error(`unexpected query: ${query}`);
      }),
    };
    const snapshot = await new PowerSyncGizWayRepository(database as never).getSnapshot("global");
    expect(snapshot.usage).toEqual([{ day: "Mon", credits: 321 }]);
    expect(snapshot.orders[0]).toMatchObject({ credits: 321, tokens: 100_000 });
    expect(snapshot.orders[0]?.metrics).toEqual([
      { metric: "input_tokens", quantity: 90_000 },
      { metric: "output_tokens", quantity: 10_000 },
    ]);
  });

  test("preserves the canonical pay_as_you_go billing mode", async () => {
    const database = {
      get: vi.fn(async (query: string) => {
        if (query.includes("my_profile")) return { id: "user", display_name: "User", email: "user@example.test", merchant_id: "merchant" };
        if (query.includes("my_balances")) return { balance_microcredits: 10 };
        if (query.includes("my_merchants")) return { id: "merchant", public_name: "Merchant", status: "active" };
        throw new Error(`unexpected get: ${query}`);
      }),
      getAll: vi.fn(async (query: string) => query.includes("available_products") ? [{ id: "payg", name: "PAYG", description: "", billing_mode: "pay_as_you_go", price_text: "Usage", status: "active" }] : []),
    };
    const snapshot = await new PowerSyncGizPayRepository(database as never).getSnapshot();
    expect(snapshot.products[0]?.billing).toBe("pay_as_you_go");
  });

  test("maps pending and billing_failed Orders without calling them streaming", async () => {
    const database = {
      getAll: vi.fn(async (query: string) => {
        if (query.includes("FROM models m")) return [{ id: "model-1", name: "Model One", provider_id: "provider-1", provider: "Provider", family: "chat", description: "", latency: "fast", context: "1K", accent: "#000" }];
        if (query.includes("FROM my_ai_orders")) return [
          { id: "pending", model: "Model One", subscription_key_id: "key", gross_microcredits: 0, status: "pending", created_at: "2026-08-17T10:00:00Z" },
          { id: "failed", model: "Model One", subscription_key_id: "key", gross_microcredits: 12, status: "billing_failed", created_at: "2026-08-17T10:01:00Z" },
        ];
        return [];
      }),
    };
    const snapshot = await new PowerSyncGizWayRepository(database as never).getSnapshot("global");
    expect(snapshot.orders.map((order) => order.status)).toEqual(["pending", "failed"]);
  });

  test("seeds 1M tokens rates as one million units", async () => {
    const batches: Array<{ query: string; rows: unknown[][] }> = [];
    const database = {
      disconnectAndClear: vi.fn(async () => undefined),
      writeTransaction: vi.fn(async (write: (transaction: { executeBatch: (query: string, rows: unknown[][]) => Promise<void> }) => Promise<void>) => write({ executeBatch: async (query, rows) => { batches.push({ query, rows }); } })),
    };
    await new PowerSyncGizWayRepository(database as never).seed("global", "active-payg");
    const prices = batches.find((batch) => batch.query.includes("model_customer_prices"));
    expect(prices?.rows.length).toBeGreaterThan(0);
    expect(prices?.rows.every((row) => row[3] === 1_000_000)).toBe(true);
  });
});

describe("M04 live PowerSync behavior", () => {
  test("retries a temporary Public Catalog token failure and connects without a reload", async () => {
    vi.useFakeTimers();
    let attempts = 0;
    const connector = new ReadOnlyCatalogConnector("https://sync.example.test", async () => {
      attempts++;
      if (attempts === 1) throw new Error("public Catalog token unavailable: 503");
      return "token";
    });
    const synced = { connected: true, hasSynced: true, downloadError: undefined };
    const database = {
      currentStatus: { connected: false, hasSynced: false, downloadError: undefined as Error | undefined },
      connect: vi.fn(async () => undefined),
      waitForStatus: vi.fn(async (predicate: (status: typeof synced) => boolean) => { expect(predicate(synced)).toBe(true); }),
    };
    const states: string[] = [];
    const maintained = maintainPowerSyncService(database as never, connector, (state) => { states.push(state); }, undefined, 100);
    await vi.advanceTimersByTimeAsync(0);
    expect(states).toEqual(["sync_error"]);
    await vi.advanceTimersByTimeAsync(100);
    expect(states).toEqual(["sync_error", "ready"]);
    expect(database.connect).toHaveBeenCalledOnce();
    expect(attempts).toBe(2);
    maintained.stop();
    vi.useRealTimers();
  });

  test("updates a maintained Public Catalog connection when PowerSync rejects its audience", async () => {
    let statusChanged: ((status: { connected: boolean; hasSynced: boolean; downloadError?: Error }) => void) | undefined;
    const connector = new ReadOnlyCatalogConnector("https://sync.example.test", async () => "token");
    const database = {
      currentStatus: { connected: false, hasSynced: false, downloadError: undefined },
      connect: vi.fn(async () => undefined),
      waitForStatus: vi.fn(() => new Promise<void>(() => undefined)),
      registerListener: vi.fn((listener: { statusChanged?: typeof statusChanged }) => {
        statusChanged = listener.statusChanged;
        return () => undefined;
      }),
    };
    const states: string[] = [];
    const maintained = maintainPowerSyncService(database as never, connector, (state) => { states.push(state); });
    connector.invalidateCredentials();
    statusChanged?.({ connected: false, hasSynced: false, downloadError: new Error("HTTP Unauthorized 401") });
    expect(states.at(-1)).toBe("denied");
    maintained.stop();
  });

  test("activates PAYG after GizPay recovers without recreating the page", async () => {
    vi.useFakeTimers();
    let syncState: "sync_error" | "ready" = "sync_error";
    let notify: () => void = () => undefined;
    const selectPAYGProduct = vi.fn();
    const database = {
      getOptional: vi.fn(async (query: string) => {
        if (query.includes("my_accounts")) return { id: "account" };
        if (query.includes("product_listings")) return { product_id: "product-payg" };
        if (query.includes("my_subscriptions")) return { id: "subscription-payg", status: "active" };
        return undefined;
      }),
    };
    const provider = {
      syncStates: () => ({ gizpay: syncState, gizway: "ready" as const }),
      gizPayDatabase: database,
      mutations: new MutationCoordinator(),
      selectPAYGProduct,
      subscribe: (listener: () => void) => { notify = listener; return () => undefined; },
    };
    const states: string[] = [];
    const stop = maintainPAYGSubscription(provider as never, "global.example.test", (state) => { states.push(state); }, 100);
    await vi.advanceTimersByTimeAsync(0);
    expect(states).toContain("waiting");
    syncState = "ready";
    notify();
    await vi.advanceTimersByTimeAsync(100);
    expect(selectPAYGProduct).toHaveBeenCalledWith("product-payg");
    expect(states.at(-1)).toBe("ready");
    stop();
    vi.useRealTimers();
  });

  test("rejects a Catalog JWT for another PowerSync audience before synchronization", () => {
    const encode = (value: unknown) => Buffer.from(JSON.stringify(value)).toString("base64url");
    const token = `${encode({ alg: "RS256" })}.${encode({ iss: "https://identity.example.test", aud: ["other-project"], exp: 4_102_444_800 })}.signature`;
    expect(() => validateCatalogJWT(token, "https://identity.example.test", "gizpay-project", "global", 1_786_320_000_000)).toThrow("public Catalog token denied: audience is invalid");
  });

  test("rejects a Catalog JWT without the required regional roles", () => {
    const encode = (value: unknown) => Buffer.from(JSON.stringify(value)).toString("base64url");
    const token = `${encode({ alg: "RS256" })}.${encode({ iss: "https://identity.example.test", aud: ["gizpay-project"], exp: 4_102_444_800 })}.signature`;
    expect(() => validateCatalogJWT(token, "https://identity.example.test", "gizpay-project", "global", 1_786_320_000_000)).toThrow("public Catalog token denied: regional Public Catalog roles are missing");
  });

  test("classifies a Public Catalog credential invalidated by PowerSync as denied", async () => {
    const connector = new ReadOnlyCatalogConnector("https://sync.example.test", async () => "catalog-token");
    await connector.fetchCredentials();
    expect(connector.hasInvalidCredentials()).toBe(false);
    connector.invalidateCredentials();
    expect(connector.hasInvalidCredentials()).toBe(true);
  });

  test("uses real mode unless Fake mode is explicitly configured", async () => {
    const source = await readFile(new URL("../app/page.tsx", import.meta.url), "utf8");
    expect(source).toContain('process.env.GIZWAY_WEB_MODE !== "fake"');
    expect(source).not.toContain('process.env.GIZWAY_WEB_MODE === "real"');
  });

  test("settles GizPay and GizWay connections independently", async () => {
    const status = (connected: boolean, hasSynced: boolean) => ({ connected, hasSynced, downloadError: undefined });
    const pay = {
      currentStatus: status(false, false),
      connect: vi.fn(async () => { throw new Error("GizPay unavailable"); }),
      waitForStatus: vi.fn(),
    };
    const way = {
      currentStatus: status(true, true),
      connect: vi.fn(async () => undefined),
      waitForStatus: vi.fn(async () => undefined),
    };
    const connector = { fetchCredentials: vi.fn(async () => ({ endpoint: "https://sync.example.test", token: "token" })) } as never;
    await expect(Promise.all([connectPowerSyncService(pay as never, connector), connectPowerSyncService(way as never, connector)])).resolves.toEqual(["sync_error", "ready"]);
    expect(way.connect).toHaveBeenCalledOnce();
  });

  test("classifies the PowerSync authentication error family as denied", async () => {
    const denied = { connected: false, hasSynced: false, downloadError: new Error("sync failed", { cause: new Error("PSYNC_S2102: authentication failed") }) };
    const database = {
      currentStatus: { connected: false, hasSynced: false, downloadError: undefined as Error | undefined },
      connect: vi.fn(async () => undefined),
      waitForStatus: vi.fn(async (predicate: (status: typeof denied) => boolean) => {
        expect(predicate(denied)).toBe(true);
      }),
    };
    await expect(connectPowerSyncService(database as never, { fetchCredentials: vi.fn(async () => ({ endpoint: "https://sync.example.test", token: "token" })) } as never)).resolves.toBe("denied");
  });

  test("uses the SDK credential invalidation signal when the browser hides the authorization error", async () => {
    const hiddenAuthorizationFailure = { connected: false, hasSynced: false, downloadError: new Error("WebSocket connection failed") };
    const connector = {
      fetchCredentials: vi.fn(async () => ({ endpoint: "https://sync.example.test", token: "token" })),
      hasInvalidCredentials: vi.fn(() => true),
    };
    const database = {
      currentStatus: hiddenAuthorizationFailure,
      connect: vi.fn(async () => undefined),
      waitForStatus: vi.fn(async (predicate: (status: typeof hiddenAuthorizationFailure) => boolean) => {
        expect(predicate(hiddenAuthorizationFailure)).toBe(true);
      }),
    };
    await expect(connectPowerSyncService(database as never, connector as never)).resolves.toBe("denied");
  });

  test("classifies wrapped JWT validation errors without relying on an HTTP status", async () => {
    const denied = { connected: false, hasSynced: false, downloadError: new Error("stream rejected: JWT audience validation failed") };
    const database = {
      currentStatus: denied,
      connect: vi.fn(async () => undefined),
      waitForStatus: vi.fn(async (predicate: (status: typeof denied) => boolean) => {
        expect(predicate(denied)).toBe(true);
      }),
    };
    await expect(connectPowerSyncService(database as never, { fetchCredentials: vi.fn(async () => ({ endpoint: "https://sync.example.test", token: "token" })) } as never)).resolves.toBe("denied");
  });

  test("rejects invalid credentials before entering the PowerSync retry loop", async () => {
    const database = {
      currentStatus: { connected: false, hasSynced: false, downloadError: undefined },
      connect: vi.fn(async () => undefined),
      waitForStatus: vi.fn(async () => undefined),
    };
    const connector = { fetchCredentials: vi.fn(async () => { throw new Error("public Catalog token denied: audience is invalid"); }) };
    await expect(connectPowerSyncService(database as never, connector as never)).resolves.toBe("denied");
    expect(database.connect).not.toHaveBeenCalled();
  });

  test("keeps a temporary Catalog Token endpoint failure retryable", async () => {
    const database = {
      currentStatus: { connected: false, hasSynced: false, downloadError: undefined },
      connect: vi.fn(async () => undefined),
      waitForStatus: vi.fn(async () => undefined),
    };
    const connector = { fetchCredentials: vi.fn(async () => { throw new Error("public Catalog token unavailable: 503"); }) };
    await expect(connectPowerSyncService(database as never, connector as never)).resolves.toBe("sync_error");
    expect(database.connect).not.toHaveBeenCalled();
  });

  test("accepts an empty regional Catalog immediately after first sync", async () => {
    const pay = { waitForFirstSync: vi.fn(async () => undefined), getOptional: vi.fn(async () => { throw new Error("Catalog rows must not be queried"); }) };
    const way = { waitForFirstSync: vi.fn(async () => undefined), getOptional: vi.fn(async () => { throw new Error("Catalog rows must not be queried"); }) };
    await expect(waitForInitialSync(pay as never, way as never)).resolves.toBeUndefined();
    expect(pay.waitForFirstSync).toHaveBeenCalledOnce();
    expect(way.waitForFirstSync).toHaveBeenCalledOnce();
    expect(pay.getOptional).not.toHaveBeenCalled();
    expect(way.getOptional).not.toHaveBeenCalled();
    const source = await readFile(new URL("../data/runtime/real-provider.ts", import.meta.url), "utf8");
    expect(source).not.toContain("Regional Catalog was not ready");
  });

  test("selects and keys the current PAYG Product instead of an arbitrary Subscription", async () => {
    const execute = vi.fn(async (...arguments_: unknown[]) => { void arguments_; });
    const database = {
      getOptional: vi.fn(async (sql: string, parameters?: unknown[]) => {
        if (sql.includes("product_listings")) return { product_id: "product-current" };
        if (sql.includes("my_subscriptions") && parameters?.[0] === "product-current") return { id: "subscription-current", status: "active" };
        if (sql.includes("my_accounts")) return { id: "account" };
        return undefined;
      }),
      get: vi.fn(async (sql: string) => {
        if (sql.includes("my_profile")) return { id: "user", display_name: "User", email: "user@example.test", merchant_id: "merchant" };
        if (sql.includes("my_balances")) return { balance_microcredits: 0 };
        if (sql.includes("my_merchants")) return { id: "merchant" };
        throw new Error(`unexpected get: ${sql}`);
      }),
      getAll: vi.fn(async () => []),
      execute,
    };
    const selection = await ensurePAYGSubscription(database as never, "global.example.test", new MutationCoordinator());
    expect(selection).toEqual({ productID: "product-current", subscriptionID: "subscription-current" });
    const repository = new PowerSyncGizPayRepository(database as never);
    repository.selectPAYGProduct(selection.productID);
    await repository.createSubscriptionKey("Current product key");
    const calls = execute.mock.calls as unknown as unknown[][];
    const insert = calls.find(([sql]) => String(sql).includes("INSERT INTO my_subscription_keys"));
    expect((insert?.[1] as unknown[] | undefined)?.[1]).toBe("subscription-current");
  });

  test("re-reads the winning PAYG Subscription after a concurrent create conflict", async () => {
    const mutations = new MutationCoordinator();
    const database = {
      getOptional: vi.fn(async (sql: string) => {
        if (sql.includes("my_accounts")) return { id: "account" };
        if (sql.includes("product_listings")) return { product_id: "product-current" };
        if (sql.includes("my_subscriptions")) return undefined;
        if (sql.includes("available_products")) return { terms_version: "v1" };
        throw new Error(`unexpected query: ${sql}`);
      }),
      execute: vi.fn(async (_sql: string, parameters: unknown[]) => {
        const rejectedID = String(parameters[0]);
        queueMicrotask(() => mutations.reject("my_subscriptions", rejectedID, new Error("subscription_already_exists: Subscription already exists")));
      }),
      getAll: vi.fn(async () => [{ id: "subscription-winner", status: "active" }]),
    };
    await expect(ensurePAYGSubscription(database as never, "global.example.test", mutations)).resolves.toEqual({ productID: "product-current", subscriptionID: "subscription-winner" });
    expect(database.getAll).toHaveBeenCalledWith(expect.stringContaining("my_subscriptions"), ["product-current"]);
  });

  test("does not accept a non-active concurrent PAYG Subscription winner", async () => {
    const mutations = new MutationCoordinator();
    const database = {
      getOptional: vi.fn(async (sql: string) => {
        if (sql.includes("my_accounts")) return { id: "account" };
        if (sql.includes("product_listings")) return { product_id: "product-current" };
        if (sql.includes("my_subscriptions")) return undefined;
        if (sql.includes("available_products")) return { terms_version: "v1" };
        throw new Error(`unexpected query: ${sql}`);
      }),
      execute: vi.fn(async (_sql: string, parameters: unknown[]) => {
        const rejectedID = String(parameters[0]);
        queueMicrotask(() => mutations.reject("my_subscriptions", rejectedID, new Error("subscription_already_exists: Subscription already exists")));
      }),
      getAll: vi.fn(async () => [{ id: "subscription-paused", status: "paused" }]),
    };
    await expect(ensurePAYGSubscription(database as never, "global.example.test", mutations)).rejects.toThrow("current PAYG Subscription is paused");
  });

  test.each(["paused", "inactive"])("does not treat a %s PAYG Subscription as ready", async (status) => {
    const mutations = new MutationCoordinator();
    const database = {
      getOptional: vi.fn(async (sql: string) => {
        if (sql.includes("my_accounts")) return { id: "account" };
        if (sql.includes("product_listings")) return { product_id: "product-current" };
        if (sql.includes("my_subscriptions")) return { id: "subscription-current", status };
        throw new Error(`unexpected query: ${sql}`);
      }),
      execute: vi.fn(),
    };
    await expect(ensurePAYGSubscription(database as never, "global.example.test", mutations)).rejects.toThrow(`current PAYG Subscription is ${status}`);
    expect(database.execute).not.toHaveBeenCalled();
  });

  test("requires an active current PAYG Subscription when creating a Key", async () => {
    const database = { getOptional: vi.fn(async () => undefined) };
    const repository = new PowerSyncGizPayRepository(database as never);
    repository.selectPAYGProduct("product-current");
    await expect(repository.createSubscriptionKey("Blocked key")).rejects.toThrow("current PAYG Subscription is not ready");
    expect(database.getOptional).toHaveBeenCalledWith(expect.stringContaining("status='active'"), ["product-current"]);
  });

  test("does not fall back to the first Subscription before the PAYG Product is selected", async () => {
    const database = {
      getOptional: vi.fn(async () => ({ id: "subscription-wrong" })),
    };
    const repository = new PowerSyncGizPayRepository(database as never);
    await expect(repository.createSubscriptionKey("Must not use arbitrary Subscription")).rejects.toThrow("current PAYG Product is not selected");
    expect(database.getOptional).not.toHaveBeenCalled();
  });

  test("subscribes to PowerSync changes and stops every live query", async () => {
    vi.useFakeTimers();
    const controllers: AbortSignal[] = [];
    const database = {
      watch: vi.fn((_sql: string, _parameters: unknown[], options: { signal: AbortSignal }) => {
        controllers.push(options.signal);
        return { async *[Symbol.asyncIterator]() { yield { rowsAffected: 0 }; } };
      }),
      registerListener: vi.fn(() => vi.fn()),
    };
    const listener = vi.fn();
    const stop = watchDatabases(database as never, database as never, listener, vi.fn());
    await Promise.resolve();
    await vi.advanceTimersByTimeAsync(30);
    expect(listener).toHaveBeenCalled();
    stop();
    expect(controllers.length).toBeGreaterThan(2);
    expect(controllers.every((signal) => signal.aborted)).toBe(true);
    vi.useRealTimers();
  });

  test("refreshes the public Catalog from live PowerSync queries", async () => {
    vi.useFakeTimers();
    const database = {
      watch: vi.fn((query: string) => {
        void query;
        return { async *[Symbol.asyncIterator]() { yield { rowsAffected: 1 }; } };
      }),
      registerListener: vi.fn(() => vi.fn()),
    };
    const listener = vi.fn();
    const stop = watchCatalogDatabases(database as never, database as never, listener, vi.fn());
    await Promise.resolve();
    await vi.advanceTimersByTimeAsync(30);
    expect(database.watch).toHaveBeenCalledTimes(3);
    expect(database.watch.mock.calls.some(([query]) => String(query).includes("model_customer_prices"))).toBe(true);
    expect(listener).toHaveBeenCalled();
    stop();
    vi.useRealTimers();
  });

  test("closes both PowerSync databases once and cancels pending mutations", async () => {
    const calls: string[] = [];
    const database = (name: string) => ({
      disconnect: vi.fn(async () => { calls.push(`${name}:disconnect`); }),
      disconnectAndClear: vi.fn(async () => { calls.push(`${name}:clear`); }),
      close: vi.fn(async () => { calls.push(`${name}:close`); }),
    });
    const pay = database("pay"), way = database("way");
    const beforeClose = vi.fn();
    const close = createDatabaseCloser(pay as never, way as never, beforeClose);
    await Promise.all([close(), close()]);
    expect(beforeClose).toHaveBeenCalledOnce();
    expect(calls).toEqual(["pay:disconnect", "way:disconnect", "pay:close", "way:close"]);
  });

  test("clears both local databases on explicit sign-out shutdown", async () => {
    const database = () => ({ disconnect: vi.fn(), disconnectAndClear: vi.fn(async () => undefined), close: vi.fn(async () => undefined) });
    const pay = database(), way = database();
    await createDatabaseCloser(pay as never, way as never)(true);
    expect(pay.disconnectAndClear).toHaveBeenCalledOnce();
    expect(way.disconnectAndClear).toHaveBeenCalledOnce();
    expect(pay.disconnect).not.toHaveBeenCalled();
    expect(way.disconnect).not.toHaveBeenCalled();
    expect(pay.close).toHaveBeenCalledOnce();
    expect(way.close).toHaveBeenCalledOnce();
  });

  test("aborts first-sync waiting immediately when the component unmounts", async () => {
    const waits: AbortSignal[] = [];
    const database = { waitForFirstSync: vi.fn((signal: AbortSignal) => {
      waits.push(signal);
      return new Promise<void>((_resolve, reject) => signal.addEventListener("abort", () => reject(signal.reason), { once: true }));
    }) };
    const controller = new AbortController();
    const pending = waitForInitialSync(database as never, database as never, controller.signal);
    controller.abort(new Error("component unmounted"));
    await expect(pending).rejects.toThrow("component unmounted");
    expect(waits).toHaveLength(2);
    expect(waits.every((signal) => signal.aborted)).toBe(true);
  });

  test("closes the first database when opening the second database fails", async () => {
    const first = { close: vi.fn(async () => undefined) };
    await expect(openDatabasePair(async () => first as never, async () => { throw new Error("second database failed"); })).rejects.toThrow("second database failed");
    expect(first.close).toHaveBeenCalledOnce();
  });

  test("connection shutdown rejects pending mutation waiters", async () => {
    const mutations = new MutationCoordinator();
    const pending = mutations.run("my_topups", "topup-pending", async () => undefined);
    mutations.cancelAll();
    await expect(pending).rejects.toThrow("PowerSync connection closed");
  });

  test("honors Retry-After before retaining a rate-limited mutation", async () => {
    const sleep = vi.fn(async () => undefined);
    const complete = vi.fn(async () => undefined);
    const connector = new GizPayConnector({
      endpoint: "https://sync.example.test", apiBaseURL: "https://pay.example.test", token: async () => "token",
      sleep, now: () => Date.parse("2026-08-16T00:00:00Z"),
    });
    vi.stubGlobal("fetch", vi.fn(async () => new Response("", { status: 429, headers: { "Retry-After": "3" } })));
    const database = {
      getCrudBatch: vi.fn(async () => ({
        crud: [{ table: "my_merchants", id: "merchant", op: "PATCH", opData: { public_name: "Name" } }],
        complete,
      })),
    };
    await expect(connector.uploadData(database as never)).rejects.toThrow("429");
    expect(sleep).toHaveBeenCalledWith(3000);
    expect(complete).not.toHaveBeenCalled();
  });

  test("clears the SDK credential-invalidated marker after a successful refresh", async () => {
    const connector = new GizPayConnector({
      endpoint: "https://sync.example.test", apiBaseURL: "https://pay.example.test", token: async () => "fresh-token",
    });
    connector.invalidateCredentials();
    expect(connector.hasInvalidCredentials()).toBe(true);
    await expect(connector.fetchCredentials()).resolves.toMatchObject({ token: "fresh-token" });
    expect(connector.hasInvalidCredentials()).toBe(false);
  });
});
