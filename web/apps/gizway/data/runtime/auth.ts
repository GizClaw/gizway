import type { PublicRuntimeConfig } from "./config";

type TokenSet = {
  access_token: string;
  refresh_token?: string;
  id_token?: string;
  expires_at: number;
};

const tokenKey = "gizway.oidc.tokens";
const transactionKey = "gizway.oidc.transaction";
let refreshPromise: Promise<string | undefined> | undefined;

function base64url(bytes: Uint8Array): string {
  let binary = "";
  for (const byte of bytes) binary += String.fromCharCode(byte);
  return btoa(binary).replaceAll("+", "-").replaceAll("/", "_").replaceAll("=", "");
}

function randomValue(size = 32): string {
  return base64url(crypto.getRandomValues(new Uint8Array(size)));
}

async function challenge(verifier: string): Promise<string> {
  return base64url(new Uint8Array(await crypto.subtle.digest("SHA-256", new TextEncoder().encode(verifier))));
}

async function redirectToLogin(config: PublicRuntimeConfig): Promise<void> {
  const verifier = randomValue(48);
  const state = randomValue(24);
  sessionStorage.setItem(transactionKey, JSON.stringify({ verifier, state }));
  const query = new URLSearchParams({
    client_id: config.identity.client_id,
    redirect_uri: config.identity.redirect_uri,
    response_type: "code",
    scope: `openid profile email offline_access urn:zitadel:iam:org:projects:roles urn:zitadel:iam:org:project:id:${config.identity.audience}:aud`,
    code_challenge: await challenge(verifier),
    code_challenge_method: "S256",
    state,
  });
  location.assign(`${config.identity.issuer.replace(/\/$/, "")}/oauth/v2/authorize?${query}`);
}

export async function beginLogin(config: PublicRuntimeConfig): Promise<never> {
  await redirectToLogin(config);
  return new Promise<never>(() => undefined);
}

export async function completeLogin(config: PublicRuntimeConfig, callbackURL: URL): Promise<void> {
  const saved = sessionStorage.getItem(transactionKey);
  sessionStorage.removeItem(transactionKey);
  if (!saved) throw new Error("OIDC transaction is missing");
  const transaction = JSON.parse(saved) as { verifier?: unknown; state?: unknown };
  if (callbackURL.searchParams.get("state") !== transaction.state || typeof transaction.verifier !== "string") {
    throw new Error("OIDC state mismatch");
  }
  const code = callbackURL.searchParams.get("code");
  if (!code) throw new Error(callbackURL.searchParams.get("error") ?? "OIDC callback has no code");
  const body = new URLSearchParams({
    grant_type: "authorization_code",
    client_id: config.identity.client_id,
    redirect_uri: config.identity.redirect_uri,
    code,
    code_verifier: transaction.verifier,
  });
  await saveTokenResponse(await fetch(`${config.identity.issuer.replace(/\/$/, "")}/oauth/v2/token`, {
    method: "POST",
    headers: { "Content-Type": "application/x-www-form-urlencoded" },
    body,
  }));
}

async function saveTokenResponse(response: Response): Promise<TokenSet> {
  if (!response.ok) throw new Error(`OIDC token exchange failed: ${response.status}`);
  const value = await response.json() as { access_token?: unknown; refresh_token?: unknown; id_token?: unknown; expires_in?: unknown };
  if (typeof value.access_token !== "string" || value.access_token === "") throw new Error("OIDC response has no access token");
  const tokens: TokenSet = {
    access_token: value.access_token,
    refresh_token: typeof value.refresh_token === "string" ? value.refresh_token : undefined,
    id_token: typeof value.id_token === "string" ? value.id_token : undefined,
    expires_at: Date.now() + Number(value.expires_in ?? 300) * 1000,
  };
  sessionStorage.setItem(tokenKey, JSON.stringify(tokens));
  return tokens;
}

function storedTokens(): TokenSet | undefined {
  const raw = sessionStorage.getItem(tokenKey);
  if (!raw) return undefined;
  try { return JSON.parse(raw) as TokenSet; } catch { return undefined; }
}

export async function humanToken(config: PublicRuntimeConfig): Promise<string | undefined> {
  const tokens = storedTokens();
  if (!tokens) return undefined;
  if (tokens.expires_at > Date.now() + 60_000) return tokens.access_token;
  if (!tokens.refresh_token) {
    clearSession();
    await redirectToLogin(config);
    return undefined;
  }
  refreshPromise ??= refreshHumanToken(config, tokens.refresh_token).finally(() => {
    refreshPromise = undefined;
  });
  return refreshPromise;
}

async function refreshHumanToken(config: PublicRuntimeConfig, refreshToken: string): Promise<string | undefined> {
  try {
    const response = await fetch(`${config.identity.issuer.replace(/\/$/, "")}/oauth/v2/token`, {
      method: "POST",
      headers: { "Content-Type": "application/x-www-form-urlencoded" },
      body: new URLSearchParams({ grant_type: "refresh_token", client_id: config.identity.client_id, refresh_token: refreshToken }),
    });
    if (storedTokens()?.refresh_token !== refreshToken) return undefined;
    return (await saveTokenResponse(response)).access_token;
  } catch {
    if (storedTokens()?.refresh_token !== refreshToken) return undefined;
    clearSession();
    await redirectToLogin(config);
    return undefined;
  }
}

export function subjectFromToken(raw: string): string {
  const part = raw.split(".")[1];
  if (!part) throw new Error("Human access token is not a JWT");
  const padded = part.replaceAll("-", "+").replaceAll("_", "/").padEnd(Math.ceil(part.length / 4) * 4, "=");
  const payload = JSON.parse(atob(padded)) as { sub?: unknown };
  if (typeof payload.sub !== "string" || payload.sub === "") throw new Error("Human access token has no subject");
  return payload.sub;
}

export function clearSession(): void {
  sessionStorage.removeItem(tokenKey);
  sessionStorage.removeItem(transactionKey);
}

export function logoutURL(config: PublicRuntimeConfig): string {
  const query = new URLSearchParams({
    client_id: config.identity.client_id,
    post_logout_redirect_uri: config.identity.post_logout_redirect_uri,
  });
  const idToken = storedTokens()?.id_token;
  if (idToken) query.set("id_token_hint", idToken);
  return `${config.identity.issuer.replace(/\/$/, "")}/oidc/v1/end_session?${query}`;
}
