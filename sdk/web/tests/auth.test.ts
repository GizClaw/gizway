import { describe, expect, it, vi } from "vitest";
import { AuthenticationRequiredError, createBrowserAuth, subjectFromToken } from "../src/auth";
import type { PublicRuntimeConfig } from "../src/config";

const config: PublicRuntimeConfig = {
  site: { hostname: "global.example.test" },
  identity: { issuer: "https://identity.example.test", client_id: "browser", redirect_uri: "https://www.example.test/callback", post_logout_redirect_uri: "https://www.example.test/", audience: "project" },
  services: { public_catalog_token_url: "https://global.example.test/auth/catalog-token", gizpay_powersync_url: "https://global.example.test/_sync/gizpay", gizpay_api_url: "https://global.example.test", gizway_powersync_url: "https://global.example.test/_sync/gizway", gizway_api_url: "https://global.example.test" },
};
function storage() {
  const values = new Map<string, string>();
  return { values, getItem: (key: string) => values.get(key) ?? null, setItem: (key: string, value: string) => values.set(key, value), removeItem: (key: string) => { values.delete(key); } };
}
const crypto = { getRandomValues<T extends ArrayBufferView | null>(value: T): T { if (value instanceof Uint8Array) value.fill(7); return value; }, subtle: globalThis.crypto.subtle } as Crypto;

describe("browser auth", () => {
  it("returns an authorization URL without navigation and enforces state", async () => {
    const store = storage();
    const auth = createBrowserAuth({ config, region: "global", storage: store, crypto, fetcher: async () => Response.json({ access_token: "token", expires_in: 300 }) });
    const url = new URL(await auth.beginLogin());
    expect(url.pathname).toBe("/oauth/v2/authorize");
    const transaction = JSON.parse([...store.values.values()][0]!) as { state: string };
    await expect(auth.completeLogin(`https://www.example.test/callback?code=c&state=wrong`)).rejects.toThrow("state mismatch");
    await auth.beginLogin();
    const next = JSON.parse([...store.values.values()][0]!) as { state: string };
    await auth.completeLogin(`https://www.example.test/callback?code=c&state=${next.state}`);
    await expect(auth.getAccessToken()).resolves.toBe("token");
    expect(transaction.state).toBe(next.state);
  });
  it("single-flights refresh, retains a non-rotated refresh token, and clears failed sessions", async () => {
    const store = storage();
    let now = 1_000;
    let calls = 0;
    const fetcher = vi.fn(async (_input: RequestInfo | URL, init?: RequestInit) => {
      calls++;
      if (String(init?.body).includes("authorization_code")) return Response.json({ access_token: "old", refresh_token: "refresh", expires_in: 1 });
      return Response.json({ access_token: "new", expires_in: 300 });
    });
    const auth = createBrowserAuth({ config, region: "global", storage: store, crypto, fetcher, clock: () => now });
    await auth.beginLogin();
    const transaction = JSON.parse([...store.values.values()][0]!) as { state: string };
    await auth.completeLogin(`https://www.example.test/callback?code=c&state=${transaction.state}`);
    now += 2_000;
    expect(await Promise.all([auth.getAccessToken(), auth.getAccessToken()])).toEqual(["new", "new"]);
    expect(calls).toBe(2);
    now += 301_000;
    fetcher.mockResolvedValueOnce(new Response("", { status: 401 }));
    await expect(auth.getAccessToken()).rejects.toBeInstanceOf(AuthenticationRequiredError);
    await expect(auth.getAccessToken()).rejects.toBeInstanceOf(AuthenticationRequiredError);
  });
  it("does not restore a cleared session from an in-flight refresh response", async () => {
    const store = storage();
    let now = 1_000;
    let releaseRefresh!: (value: Record<string, unknown>) => void;
    const refreshBody = new Promise<Record<string, unknown>>((resolve) => { releaseRefresh = resolve; });
    const fetcher = vi.fn(async (_input: RequestInfo | URL, init?: RequestInit) => {
      if (String(init?.body).includes("authorization_code")) return Response.json({ access_token: "old", refresh_token: "refresh", expires_in: 1 });
      return { ok: true, status: 200, json: () => refreshBody } as Response;
    });
    const auth = createBrowserAuth({ config, region: "global", storage: store, crypto, fetcher, clock: () => now });
    await auth.beginLogin();
    const transaction = JSON.parse([...store.values.values()][0]!) as { state: string };
    await auth.completeLogin(`https://www.example.test/callback?code=c&state=${transaction.state}`);
    now += 2_000;
    const refresh = auth.getAccessToken();
    await vi.waitFor(() => expect(fetcher).toHaveBeenCalledTimes(2));
    auth.clearSession();
    releaseRefresh({ access_token: "resurrected", refresh_token: "next", expires_in: 300 });
    await expect(refresh).rejects.toThrow("session changed");
    expect(store.values.size).toBe(0);
  });
  it("isolates region namespaces", async () => {
    const store = storage();
    const globalAuth = createBrowserAuth({ config, region: "global", storage: store, crypto });
    const cnAuth = createBrowserAuth({ config, region: "cn", storage: store, crypto });
    await globalAuth.beginLogin();
    await cnAuth.beginLogin();
    expect(store.values.size).toBe(2);
    globalAuth.clearSession();
    expect(store.values.size).toBe(1);
  });
});

it("reads the exact JWT subject", () => {
  const payload = btoa(JSON.stringify({ sub: "user/a:b" })).replaceAll("=", "");
  expect(subjectFromToken(`x.${payload}.x`)).toBe("user/a:b");
});
