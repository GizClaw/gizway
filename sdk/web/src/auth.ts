import type { Fetch, PublicRuntimeConfig } from "./config";
import type { Region } from "./contracts/gizway";

type TokenSet = { access_token: string; refresh_token?: string; id_token?: string; expires_at: number };
type StorageLike = Pick<Storage, "getItem" | "setItem" | "removeItem">;
type CryptoLike = Pick<Crypto, "getRandomValues" | "subtle">;

export type BrowserOAuthClient = {
  clientId: string;
  redirectUri: string;
  postLogoutRedirectUri: string;
};

export class AuthenticationConfigurationError extends Error {
  constructor(message = "browser OAuth configuration is required") {
    super(message);
    this.name = "AuthenticationConfigurationError";
  }
}

export class AuthenticationRequiredError extends Error {
  constructor(message = "authentication is required", options?: ErrorOptions) {
    super(message, options);
    this.name = "AuthenticationRequiredError";
  }
}

export type GizWayAuth = {
  beginLogin(): Promise<string>;
  completeLogin(callbackURL: string | URL): Promise<void>;
  getAccessToken(): Promise<string>;
  getLogoutURL(): string;
  clearSession(): void;
};

export function createBrowserAuth(options: {
  config: PublicRuntimeConfig;
  region: Region;
  oauth?: BrowserOAuthClient;
  storage?: StorageLike;
  fetcher?: Fetch;
  crypto?: CryptoLike;
  clock?: () => number;
}): GizWayAuth {
  const { config, region } = options;
  if (!options.oauth) return unconfiguredBrowserAuth();
  const oauth = validateOAuthClient(options.oauth);
  if (!options.storage) throw new AuthenticationConfigurationError("browser session storage is unavailable");
  const storage = options.storage;
  const fetcher = options.fetcher ?? globalThis.fetch;
  const crypto = options.crypto ?? globalThis.crypto;
  const clock = options.clock ?? Date.now;
  const prefix = `gizway.oidc.${region}.${encodeURIComponent(config.identity.issuer)}.${encodeURIComponent(oauth.clientId)}`;
  const tokenKey = `${prefix}.tokens`;
  const transactionKey = `${prefix}.transaction`;
  let refreshPromise: Promise<string> | undefined;
  let sessionGeneration = 0;

  const storedTokens = (): TokenSet | undefined => {
    const raw = storage.getItem(tokenKey);
    if (!raw) return undefined;
    try {
      const value = JSON.parse(raw) as TokenSet;
      return typeof value.access_token === "string" && typeof value.expires_at === "number" ? value : undefined;
    } catch { return undefined; }
  };
  const clearSession = () => {
    sessionGeneration++;
    storage.removeItem(tokenKey);
    storage.removeItem(transactionKey);
  };
  const saveTokenResponse = async (response: Response, previous?: TokenSet, mayPersist: () => boolean = () => true): Promise<TokenSet> => {
    if (!response.ok) throw new AuthenticationRequiredError(`OIDC token exchange failed: ${response.status}`);
    const value = await response.json() as Record<string, unknown>;
    if (typeof value.access_token !== "string" || value.access_token === "") throw new AuthenticationRequiredError("OIDC response has no access token");
    const expires = Number(value.expires_in ?? 300);
    if (!Number.isFinite(expires) || expires <= 0) throw new AuthenticationRequiredError("OIDC response has invalid expiry");
    const tokens: TokenSet = {
      access_token: value.access_token,
      refresh_token: typeof value.refresh_token === "string" ? value.refresh_token : previous?.refresh_token,
      id_token: typeof value.id_token === "string" ? value.id_token : previous?.id_token,
      expires_at: clock() + expires * 1000,
    };
    if (!mayPersist()) throw new AuthenticationRequiredError("OIDC session changed during token exchange");
    storage.setItem(tokenKey, JSON.stringify(tokens));
    return tokens;
  };
  const tokenEndpoint = `${config.identity.issuer.replace(/\/$/, "")}/oauth/v2/token`;

  return {
    async beginLogin() {
      const verifier = randomValue(crypto, 48);
      const state = randomValue(crypto, 24);
      storage.setItem(transactionKey, JSON.stringify({ verifier, state }));
      const digest = await crypto.subtle.digest("SHA-256", new TextEncoder().encode(verifier));
      const query = new URLSearchParams({
        client_id: oauth.clientId,
        redirect_uri: oauth.redirectUri,
        response_type: "code",
        scope: `openid profile email offline_access urn:zitadel:iam:org:projects:roles urn:zitadel:iam:org:project:id:${config.identity.audience}:aud`,
        code_challenge: base64url(new Uint8Array(digest)),
        code_challenge_method: "S256",
        state,
      });
      return `${config.identity.issuer.replace(/\/$/, "")}/oauth/v2/authorize?${query}`;
    },
    async completeLogin(rawCallbackURL) {
      const callbackURL = new URL(rawCallbackURL);
      const expectedCallback = new URL(oauth.redirectUri);
      if (callbackURL.origin !== expectedCallback.origin || callbackURL.pathname !== expectedCallback.pathname) {
        throw new AuthenticationRequiredError("OIDC callback URL does not match the configured redirect URI");
      }
      const saved = storage.getItem(transactionKey);
      storage.removeItem(transactionKey);
      if (!saved) throw new AuthenticationRequiredError("OIDC transaction is missing");
      let transaction: { verifier?: unknown; state?: unknown };
      try { transaction = JSON.parse(saved) as typeof transaction; } catch { throw new AuthenticationRequiredError("OIDC transaction is invalid"); }
      if (callbackURL.searchParams.get("state") !== transaction.state || typeof transaction.verifier !== "string") {
        throw new AuthenticationRequiredError("OIDC state mismatch");
      }
      const error = callbackURL.searchParams.get("error");
      const code = callbackURL.searchParams.get("code");
      if (error || !code) throw new AuthenticationRequiredError(error ?? "OIDC callback has no code");
      await saveTokenResponse(await fetcher(tokenEndpoint, {
        method: "POST",
        credentials: "omit",
        headers: { "Content-Type": "application/x-www-form-urlencoded" },
        body: new URLSearchParams({ grant_type: "authorization_code", client_id: oauth.clientId, redirect_uri: oauth.redirectUri, code, code_verifier: transaction.verifier }),
      }));
    },
    async getAccessToken() {
      const tokens = storedTokens();
      if (!tokens) throw new AuthenticationRequiredError();
      if (tokens.expires_at > clock() + 60_000) return tokens.access_token;
      if (!tokens.refresh_token) {
        clearSession();
        throw new AuthenticationRequiredError("access token expired and no refresh token is available");
      }
      refreshPromise ??= (async () => {
        const generation = sessionGeneration;
        try {
          const activeRefresh = tokens.refresh_token!;
          const response = await fetcher(tokenEndpoint, {
            method: "POST",
            credentials: "omit",
            headers: { "Content-Type": "application/x-www-form-urlencoded" },
            body: new URLSearchParams({ grant_type: "refresh_token", client_id: oauth.clientId, refresh_token: activeRefresh }),
          });
          return (await saveTokenResponse(response, tokens, () => sessionGeneration === generation && storedTokens()?.refresh_token === activeRefresh)).access_token;
        } catch (error) {
          if (sessionGeneration === generation && storedTokens()?.refresh_token === tokens.refresh_token) clearSession();
          throw error instanceof AuthenticationRequiredError ? error : new AuthenticationRequiredError("OIDC token refresh failed", { cause: error });
        } finally { refreshPromise = undefined; }
      })();
      return refreshPromise;
    },
    getLogoutURL() {
      const query = new URLSearchParams({ client_id: oauth.clientId, post_logout_redirect_uri: oauth.postLogoutRedirectUri });
      const idToken = storedTokens()?.id_token;
      if (idToken) query.set("id_token_hint", idToken);
      return `${config.identity.issuer.replace(/\/$/, "")}/oidc/v1/end_session?${query}`;
    },
    clearSession,
  };
}

function unconfiguredBrowserAuth(): GizWayAuth {
  const unavailable = () => new AuthenticationConfigurationError();
  return {
    async beginLogin() { throw unavailable(); },
    async completeLogin() { throw unavailable(); },
    async getAccessToken() { throw unavailable(); },
    getLogoutURL() { throw unavailable(); },
    clearSession() { /* no OAuth namespace exists */ },
  };
}

function validateOAuthClient(value: BrowserOAuthClient): BrowserOAuthClient {
  if (typeof value.clientId !== "string" || value.clientId.trim() === "" || value.clientId !== value.clientId.trim()) {
    throw new AuthenticationConfigurationError("browser OAuth clientId must be a non-empty value without surrounding whitespace");
  }
  validateOAuthRedirect(value.redirectUri, "redirectUri");
  validateOAuthRedirect(value.postLogoutRedirectUri, "postLogoutRedirectUri");
  return value;
}

function validateOAuthRedirect(raw: string, name: string): URL {
  let url: URL;
  try { url = new URL(raw); } catch { throw new AuthenticationConfigurationError(`browser OAuth ${name} must be an absolute URL`); }
  if (url.username || url.password || url.hash) throw new AuthenticationConfigurationError(`browser OAuth ${name} must not contain credentials or a fragment`);
  if (url.hostname.includes("*")) throw new AuthenticationConfigurationError(`browser OAuth ${name} must not contain a wildcard host`);
  const loopback = url.hostname === "localhost" || url.hostname === "127.0.0.1" || url.hostname === "[::1]";
  if (url.protocol !== "https:" && !(url.protocol === "http:" && loopback)) {
    throw new AuthenticationConfigurationError(`browser OAuth ${name} must use HTTPS (HTTP is loopback-only)`);
  }
  return url;
}

export function subjectFromToken(raw: string): string {
  const part = raw.split(".")[1];
  if (!part) throw new AuthenticationRequiredError("access token is not a JWT");
  try {
    const encoded = part.replaceAll("-", "+").replaceAll("_", "/").padEnd(Math.ceil(part.length / 4) * 4, "=");
    const payload = JSON.parse(new TextDecoder().decode(Uint8Array.from(atob(encoded), (character) => character.charCodeAt(0)))) as { sub?: unknown };
    if (typeof payload.sub !== "string" || payload.sub === "") throw new Error("missing subject");
    return payload.sub;
  } catch (error) { throw new AuthenticationRequiredError("access token has no valid subject", { cause: error }); }
}

function randomValue(crypto: CryptoLike, size: number): string {
  return base64url(crypto.getRandomValues(new Uint8Array(size)));
}

function base64url(bytes: Uint8Array): string {
  let binary = "";
  for (const byte of bytes) binary += String.fromCharCode(byte);
  return btoa(binary).replaceAll("+", "-").replaceAll("/", "_").replaceAll("=", "");
}
