import type { Region } from "./contracts/gizway";

export type PublicRuntimeConfig = {
  site: { hostname: string };
  identity: {
    issuer: string;
    audience: string;
  };
  services: {
    public_catalog_token_url: string;
    gizpay_powersync_url: string;
    gizpay_api_url: string;
    gizway_powersync_url: string;
    gizway_api_url: string;
  };
};

export type Fetch = typeof globalThis.fetch;

export async function loadRuntimeConfig(entryOrigin: string, region: Region, fetcher: Fetch = globalThis.fetch): Promise<PublicRuntimeConfig> {
  const origin = validateOrigin(entryOrigin, "entryOrigin");
  const response = await fetcher(new URL("/auth/runtime-config", origin), { cache: "no-store", credentials: "omit" });
  if (!response.ok) throw new Error(`runtime configuration unavailable: ${response.status}`);
  return validateRuntimeConfig(await response.json(), origin, region);
}

export function validateRuntimeConfig(value: unknown, entryOrigin: URL, region: Region): PublicRuntimeConfig {
  if (!isRecord(value) || !isRecord(value.site) || !isRecord(value.identity) || !isRecord(value.services)) {
    throw new Error("runtime configuration is incomplete");
  }
  const config = value as PublicRuntimeConfig;
  const strings = [
    config.site.hostname,
    config.identity.issuer,
    config.identity.audience,
    config.services.public_catalog_token_url,
    config.services.gizpay_powersync_url,
    config.services.gizpay_api_url,
    config.services.gizway_powersync_url,
    config.services.gizway_api_url,
  ];
  if (strings.some((item) => typeof item !== "string" || item.length === 0)) throw new Error("runtime configuration is incomplete");
  if (config.site.hostname !== entryOrigin.hostname) throw new Error("runtime configuration site does not match Entry");
  const expectedRegion: Region = config.site.hostname.startsWith("cn.") ? "cn" : "global";
  if (expectedRegion !== region) throw new Error(`runtime configuration region mismatch: expected ${region}`);
  validateOrigin(config.identity.issuer, "identity issuer");
  validateURL(config.services.public_catalog_token_url, "Catalog token URL");
  validateURL(config.services.gizpay_powersync_url, "GizPay PowerSync URL");
  validateOrigin(config.services.gizpay_api_url, "GizPay API origin");
  validateURL(config.services.gizway_powersync_url, "GizWay PowerSync URL");
  validateOrigin(config.services.gizway_api_url, "GizWay API origin");
  return config;
}

export async function publicCatalogToken(config: PublicRuntimeConfig, region: Region, fetcher: Fetch = globalThis.fetch, now = Date.now()): Promise<string> {
  const response = await fetcher(config.services.public_catalog_token_url, { cache: "no-store", credentials: "omit" });
  if (!response.ok) throw new Error(`public Catalog token unavailable: ${response.status}`);
  const value = await response.json() as { access_token?: unknown; token_type?: unknown };
  if (typeof value.access_token !== "string" || value.access_token === "" || value.token_type !== "Bearer") {
    throw new Error("public Catalog token response is invalid");
  }
  validateCatalogJWT(value.access_token, config.identity.issuer, config.identity.audience, region, now);
  return value.access_token;
}

export function validateCatalogJWT(token: string, issuer: string, audience: string, region: Region, now = Date.now()): void {
  try {
    const parts = token.split(".");
    if (parts.length !== 3 || !parts[1]) throw new Error("JWT structure is invalid");
    const payload = JSON.parse(decodeBase64URL(parts[1])) as Record<string, unknown>;
    const audiences = typeof payload.aud === "string" ? [payload.aud] : Array.isArray(payload.aud) ? payload.aud : [];
    if (payload.iss !== issuer) throw new Error("issuer is invalid");
    if (!audiences.includes(audience)) throw new Error("audience is invalid");
    if (typeof payload.exp !== "number" || payload.exp * 1000 <= now) throw new Error("token is expired");
    const claim = payload[`urn:zitadel:iam:org:project:${audience}:roles`];
    const roles = isRecord(claim) ? claim : {};
    if (!("public_catalog" in roles) || !(`public_catalog_${region}` in roles)) throw new Error("regional Public Catalog roles are missing");
  } catch (error) {
    const reason = error instanceof Error ? error.message : "claims are invalid";
    throw new Error(`public Catalog token denied: ${reason}`, { cause: error });
  }
}

export function validateOrigin(raw: string, name: string): URL {
  const url = validateURL(raw, name);
  if (url.pathname !== "/" || url.search || url.hash) throw new Error(`${name} must be an origin without path, query, or fragment`);
  return url;
}

function validateURL(raw: string, name: string): URL {
  let url: URL;
  try { url = new URL(raw); } catch { throw new Error(`${name} must be an absolute URL`); }
  if (url.username || url.password || url.hash) throw new Error(`${name} must not contain credentials or a fragment`);
  const loopback = url.hostname === "localhost" || url.hostname === "127.0.0.1" || url.hostname === "[::1]";
  if (url.protocol !== "https:" && !(url.protocol === "http:" && loopback)) throw new Error(`${name} must use HTTPS (HTTP is loopback-only)`);
  return url;
}

function decodeBase64URL(value: string): string {
  const encoded = value.replaceAll("-", "+").replaceAll("_", "/").padEnd(Math.ceil(value.length / 4) * 4, "=");
  return new TextDecoder().decode(Uint8Array.from(atob(encoded), (character) => character.charCodeAt(0)));
}

function isRecord(value: unknown): value is Record<string, unknown> {
  return value != null && typeof value === "object" && !Array.isArray(value);
}
