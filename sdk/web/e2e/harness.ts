import { createGizWayClient, type BrowserOAuthClient, type GizWayClient, type PublicCatalogConnection, type Region } from "../src";

let client: GizWayClient | undefined;
let connection: PublicCatalogConnection | undefined;
let authenticated: Awaited<ReturnType<GizWayClient["connectAuthenticated"]>> | undefined;

globalThis.sdkTest = {
  async connectPublic(entryOrigin: string, region: Region) {
    connection = undefined;
    client = await createGizWayClient({ entryOrigin, region });
    connection = await client.connectPublicCatalog();
    const catalog = await connection.getCatalog();
    return { states: connection.getStates(), products: catalog.products.length, models: catalog.models.length };
  },
  async state() { return connection?.getStates(); },
  async beginLogin(entryOrigin: string, region: Region, oauth: BrowserOAuthClient) {
    client = await createGizWayClient({ entryOrigin, region, oauth });
    return client.auth.beginLogin();
  },
  async completeLogin(entryOrigin: string, region: Region, oauth: BrowserOAuthClient, callbackURL: string) {
    client = await createGizWayClient({ entryOrigin, region, oauth });
    await client.auth.completeLogin(callbackURL);
    authenticated = await client.connectAuthenticated();
    const [pay, way] = await Promise.all([authenticated.gizpay.getSnapshot(), authenticated.gizway.getSnapshot()]);
    return { states: authenticated.getStates(), products: pay.products.length, models: way.models.length };
  },
  async mutate() {
    if (!authenticated) throw new Error("authenticated connection is unavailable");
    let pay = await authenticated.gizpay.getSnapshot();
    const product = pay.products[0];
    if (!product) throw new Error("no GizPay product is available");
    let subscription = pay.subscriptions.find((item) => item.productId === product.id && item.status === "active");
    if (!subscription) {
      pay = await authenticated.gizpay.createSubscription(product.id);
      subscription = pay.subscriptions.find((item) => item.productId === product.id && item.status === "active");
    }
    if (!subscription) throw new Error("active GizPay subscription was not synchronized");
    await authenticated.gizpay.createSubscriptionKey(subscription.id, `SDK E2E ${crypto.randomUUID()}`);
    await authenticated.gizpay.createTopUp(50_000, "fake", `sdk-${crypto.randomUUID()}`);
    let way = await authenticated.gizway.getSnapshot();
    const model = way.models[0];
    if (!model) throw new Error("no GizWay model is available");
    way = await authenticated.gizway.createProviderKey({ providerId: model.providerId, name: `SDK E2E ${crypto.randomUUID()}`, key: `provider-${crypto.randomUUID()}`, prices: [{ modelId: model.id, metric: "input_tokens", unitSize: 1_000_000, microcreditsPerUnit: 25 }] });
    const providerKey = way.providerKeys.at(-1);
    if (!providerKey) throw new Error("Provider Key was not synchronized");
    await authenticated.gizway.updateProviderKeyPrices(providerKey.id, [{ modelId: model.id, metric: "input_tokens", unitSize: 1_000_000, microcreditsPerUnit: 30 }]);
    way = await authenticated.gizway.disableProviderKey(providerKey.id);
    return { keys: (await authenticated.gizpay.getSnapshot()).keys.length, providerStatus: way.providerKeys.find((item) => item.id === providerKey.id)?.status };
  },
  async logoutURL() {
    if (!client) throw new Error("SDK client is unavailable");
    return client.auth.getLogoutURL();
  },
  async close(clear = false) { if (clear) await client?.clearLocalData(); else await client?.close(); },
};

declare global {
  var sdkTest: {
    connectPublic(entryOrigin: string, region: Region): Promise<{ states: unknown; products: number; models: number }>;
    state(): Promise<unknown>;
    beginLogin(entryOrigin: string, region: Region, oauth: BrowserOAuthClient): Promise<string>;
    completeLogin(entryOrigin: string, region: Region, oauth: BrowserOAuthClient, callbackURL: string): Promise<{ states: unknown; products: number; models: number }>;
    mutate(): Promise<{ keys: number; providerStatus?: string }>;
    logoutURL(): Promise<string>;
    close(clear?: boolean): Promise<void>;
  };
}
