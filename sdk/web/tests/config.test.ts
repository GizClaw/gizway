import { describe, expect, it } from "vitest";
import { loadRuntimeConfig, validateCatalogJWT } from "../src/config";

const runtime = {
  site: { hostname: "global.example.test" },
  identity: { issuer: "https://identity.example.test", client_id: "browser", redirect_uri: "https://www.example.test/callback", post_logout_redirect_uri: "https://www.example.test/", audience: "project" },
  services: { public_catalog_token_url: "https://global.example.test/auth/catalog-token", gizpay_powersync_url: "https://global.example.test/_sync/gizpay", gizpay_api_url: "https://global.example.test", gizway_powersync_url: "https://global.example.test/_sync/gizway", gizway_api_url: "https://global.example.test" },
};

describe("runtime configuration", () => {
  it("loads from the explicit Entry and omits credentials", async () => {
    let request: RequestInfo | URL | undefined;
    const config = await loadRuntimeConfig("https://global.example.test", "global", async (input, init) => {
      request = input;
      expect(init?.credentials).toBe("omit");
      return Response.json(runtime);
    });
    expect(String(request)).toBe("https://global.example.test/auth/runtime-config");
    expect(config.site.hostname).toBe("global.example.test");
  });
  it.each([
    ["region", runtime, "cn", "region mismatch"],
    ["site", { ...runtime, site: { hostname: "other.example.test" } }, "global", "does not match Entry"],
    ["unsafe service", { ...runtime, services: { ...runtime.services, gizway_api_url: "http://global.example.test" } }, "global", "must use HTTPS"],
    ["credential", { ...runtime, identity: { ...runtime.identity, issuer: "https://user:pass@identity.example.test" } }, "global", "must not contain credentials"],
  ] as const)("rejects %s mismatch", async (_name, body, region, message) => {
    await expect(loadRuntimeConfig("https://global.example.test", region, async () => Response.json(body))).rejects.toThrow(message);
  });
  it("accepts loopback HTTP for browser tests", async () => {
    const local = JSON.parse(JSON.stringify(runtime));
    local.site.hostname = "127.0.0.1";
    for (const key of Object.keys(local.services)) local.services[key] = `http://127.0.0.1:8080/${key}`;
    local.services.gizpay_api_url = local.services.gizway_api_url = "http://127.0.0.1:8080";
    local.identity.issuer = "http://localhost:8081";
    local.identity.redirect_uri = "http://localhost:4173/callback";
    local.identity.post_logout_redirect_uri = "http://localhost:4173/";
    await expect(loadRuntimeConfig("http://127.0.0.1:8080", "global", async () => Response.json(local))).resolves.toBeTruthy();
  });
});

it("validates regional Catalog claims", () => {
  const payload = btoa(JSON.stringify({ iss: runtime.identity.issuer, aud: ["project"], exp: Math.floor(Date.now() / 1000) + 60, "urn:zitadel:iam:org:project:project:roles": { public_catalog: {}, public_catalog_global: {} } })).replaceAll("+", "-").replaceAll("/", "_").replaceAll("=", "");
  expect(() => validateCatalogJWT(`x.${payload}.x`, runtime.identity.issuer, "project", "global")).not.toThrow();
  expect(() => validateCatalogJWT(`x.${payload}.x`, runtime.identity.issuer, "project", "cn")).toThrow("regional Public Catalog roles are missing");
});
