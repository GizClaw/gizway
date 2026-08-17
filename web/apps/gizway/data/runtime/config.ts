export type PublicRuntimeConfig = {
  site: { hostname: string };
  identity: {
    issuer: string;
    client_id: string;
    redirect_uri: string;
    post_logout_redirect_uri: string;
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

let configPromise: Promise<PublicRuntimeConfig> | undefined;

export function loadRuntimeConfig(): Promise<PublicRuntimeConfig> {
  configPromise ??= fetch("/auth/runtime-config", { cache: "no-store" }).then(async (response) => {
    if (!response.ok) throw new Error(`runtime configuration unavailable: ${response.status}`);
    const value = await response.json() as PublicRuntimeConfig;
    if (!value.site?.hostname || !value.identity?.issuer || !value.identity?.client_id
      || !value.services?.gizpay_powersync_url || !value.services?.gizway_powersync_url) {
      throw new Error("runtime configuration is incomplete");
    }
    return value;
  });
  return configPromise;
}

export async function publicCatalogToken(config: PublicRuntimeConfig): Promise<string> {
  const response = await fetch(config.services.public_catalog_token_url, { cache: "no-store" });
  if (!response.ok) throw new Error(`public Catalog token unavailable: ${response.status}`);
  const value = await response.json() as { access_token?: unknown; token_type?: unknown };
  if (typeof value.access_token !== "string" || value.access_token === "" || value.token_type !== "Bearer") {
    throw new Error("public Catalog token response is invalid");
  }
  validateCatalogJWT(value.access_token, config.identity.issuer, config.identity.audience, config.site.hostname.startsWith("cn.") ? "cn" : "global");
  return value.access_token;
}

export function validateCatalogJWT(token: string, expectedIssuer: string, expectedAudience: string, region: "cn" | "global", now = Date.now()): void {
  try {
    const parts = token.split(".");
    if (parts.length !== 3) throw new Error("JWT structure is invalid");
    const encoded = parts[1].replaceAll("-", "+").replaceAll("_", "/");
    const payload = JSON.parse(atob(encoded.padEnd(Math.ceil(encoded.length / 4) * 4, "="))) as Record<string, unknown>;
    const audiences = typeof payload.aud === "string" ? [payload.aud] : Array.isArray(payload.aud) ? payload.aud : [];
    if (payload.iss !== expectedIssuer) throw new Error("issuer is invalid");
    if (!audiences.includes(expectedAudience)) throw new Error("audience is invalid");
    if (typeof payload.exp !== "number" || payload.exp * 1000 <= now) throw new Error("token is expired");
    const claim = payload[`urn:zitadel:iam:org:project:${expectedAudience}:roles`];
    const roles = claim != null && typeof claim === "object" ? claim as Record<string, unknown> : {};
    if (!("public_catalog" in roles) || !(`public_catalog_${region}` in roles)) throw new Error("regional Public Catalog roles are missing");
  } catch (error) {
    const reason = error instanceof Error ? error.message : "claims are invalid";
    throw new Error(`public Catalog token denied: ${reason}`, { cause: error });
  }
}
