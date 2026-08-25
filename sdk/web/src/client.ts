import type { PowerSyncBackendConnector, PowerSyncDatabase } from "@powersync/web";
import { AuthenticationConfigurationError, createBrowserAuth, subjectFromToken, type BrowserOAuthClient, type GizWayAuth } from "./auth";
import { loadRuntimeConfig, publicCatalogToken, type Fetch, type PublicRuntimeConfig } from "./config";
import type { CatalogProduct } from "./contracts/gizpay";
import type { GizPayRepository } from "./contracts/gizpay";
import type { CatalogModel, GizWayRepository, Model, Region } from "./contracts/gizway";
import { authenticatedDatabaseNames, defaultDatabaseFactory, openDatabasePair, publicDatabaseNames, type DatabaseFactory } from "./powersync/database";
import { GizPayConnector } from "./powersync/connectors/gizpay";
import { GizWayConnector } from "./powersync/connectors/gizway";
import { PowerSyncGizPayRepository } from "./powersync/gizpay/repository";
import { PowerSyncGizWayRepository } from "./powersync/gizway/repository";
import { connectService, createPairCloser, currentStates, MutationCoordinator, watchDatabases } from "./powersync/lifecycle";

export type ServiceSyncState = "first_sync" | "ready" | "offline" | "denied" | "sync_error";
export type ServiceSyncStates = { gizpay: ServiceSyncState; gizway: ServiceSyncState };
export type ConnectionBase = {
  getStates(): ServiceSyncStates;
  subscribe(listener: () => void): () => void;
  close(): Promise<void>;
};
export type PublicCatalogConnection = ConnectionBase & {
  getCatalog(): Promise<{ products: CatalogProduct[]; models: CatalogModel[] }>;
};
export type AuthenticatedConnection = ConnectionBase & { gizpay: GizPayRepository; gizway: GizWayRepository };
export type GizWayClient = {
  readonly region: Region;
  readonly config: PublicRuntimeConfig;
  readonly auth: GizWayAuth;
  connectPublicCatalog(options?: { signal?: AbortSignal }): Promise<PublicCatalogConnection>;
  connectAuthenticated(options?: { signal?: AbortSignal; onMutationError?: (error: Error) => void }): Promise<AuthenticatedConnection>;
  clearLocalData(): Promise<void>;
  close(): Promise<void>;
};
export type CreateClientOptions = {
  entryOrigin: string;
  region: Region;
  oauth?: BrowserOAuthClient;
  storage?: Pick<Storage, "getItem" | "setItem" | "removeItem">;
  fetch?: Fetch;
  crypto?: Pick<Crypto, "getRandomValues" | "subtle">;
  clock?: () => number;
};

export async function createGizWayClient(options: CreateClientOptions): Promise<GizWayClient> {
  const fetcher = options.fetch ?? globalThis.fetch;
  const crypto = options.crypto ?? globalThis.crypto;
  const storage = options.oauth ? (options.storage ?? defaultSessionStorage()) : options.storage;
  const config = await loadRuntimeConfig(options.entryOrigin, options.region, fetcher);
  const auth = createBrowserAuth({ config, region: options.region, oauth: options.oauth, storage, fetcher, crypto, clock: options.clock });
  return createClientWithDatabaseFactory(options.region, config, auth, options.oauth?.clientId, fetcher, crypto, options.clock ?? Date.now, defaultDatabaseFactory);
}

function defaultSessionStorage(): Storage {
  try {
    if (globalThis.sessionStorage) return globalThis.sessionStorage;
  } catch { /* access can be denied by the browser security model */ }
  throw new AuthenticationConfigurationError("browser session storage is unavailable");
}

function createClientWithDatabaseFactory(region: Region, config: PublicRuntimeConfig, auth: GizWayAuth, oauthClientId: string | undefined, fetcher: Fetch, crypto: Pick<Crypto, "subtle">, clock: () => number, databaseFactory: DatabaseFactory): GizWayClient {
  const active = new Set<ConnectionBase>();
  let lastSubject: string | undefined;
  let closing: Promise<void> | undefined;
  let closed = false;
  const ensureOpen = () => { if (closed) throw new Error("GizWay SDK client is closed"); };
  const requireOAuthClientId = () => {
    if (!oauthClientId) throw new AuthenticationConfigurationError();
    return oauthClientId;
  };
  const track = <T extends ConnectionBase>(connection: T): T => {
    const close = connection.close;
    connection.close = async () => { try { await close(); } finally { active.delete(connection); } };
    active.add(connection);
    return connection;
  };
  const open = async (names: { gizpay: string; gizway: string }, payConnector: PowerSyncBackendConnector, wayConnector: PowerSyncBackendConnector, signal?: AbortSignal) => {
    const [pay, way] = await openDatabasePair(names, databaseFactory);
    const controller = new AbortController();
    const combined = signal ? AbortSignal.any([signal, controller.signal]) : controller.signal;
    const closer = createPairCloser(pay, way, () => controller.abort());
    try {
      const [payState, wayState] = await Promise.all([connectService(pay, payConnector, combined), connectService(way, wayConnector, combined)]);
      combined.throwIfAborted();
      const abort = () => { void closer().catch(() => undefined); };
      signal?.addEventListener("abort", abort, { once: true });
      if (signal?.aborted) {
        await closer().catch(() => undefined);
        signal.throwIfAborted();
      }
      const close = (clear = false) => { signal?.removeEventListener("abort", abort); return closer(clear); };
      return { pay, way, initial: { gizpay: payState, gizway: wayState } as ServiceSyncStates, closer: close };
    } catch (error) { await closer().catch(() => undefined); throw error; }
  };

  return {
    region,
    config,
    auth,
    async connectPublicCatalog({ signal } = {}) {
      ensureOpen();
      const token = () => publicCatalogToken(config, region, fetcher, clock());
      const pair = await open(publicDatabaseNames(region), new ReadOnlyCatalogConnector(config.services.gizpay_powersync_url, token), new ReadOnlyCatalogConnector(config.services.gizway_powersync_url, token), signal);
      return track(createPublicConnection(pair.pay, pair.way, pair.initial, pair.closer, region, config.site.hostname));
    },
    async connectAuthenticated({ signal, onMutationError } = {}) {
      ensureOpen();
      const initialToken = await auth.getAccessToken();
      lastSubject = subjectFromToken(initialToken);
      const names = await authenticatedDatabaseNames(region, config.identity.issuer, requireOAuthClientId(), lastSubject, crypto);
      const mutations = new MutationCoordinator();
      const mutationError = (error: { table: string; id: string; code: string; message: string }) => {
        const failure = new Error(`${error.code}: ${error.message}`);
        mutations.reject(error.table, error.id, failure);
        try { onMutationError?.(failure); } catch { /* observer failures must not replay an accepted API rejection */ }
      };
      const common = { token: () => auth.getAccessToken(), fetcher, onMutationError: mutationError, onMutationSuccess: (success: { table: string; id: string }) => mutations.succeed(success.table, success.id) };
      const pair = await open(names,
        new GizPayConnector({ ...common, endpoint: config.services.gizpay_powersync_url, apiBaseURL: config.services.gizpay_api_url }),
        new GizWayConnector({ ...common, endpoint: config.services.gizway_powersync_url, apiBaseURL: config.services.gizway_api_url }), signal);
      const connection = createAuthenticatedConnection(pair.pay, pair.way, pair.initial, pair.closer, region, mutations, onMutationError);
      return track(connection);
    },
    async clearLocalData() {
      ensureOpen();
      const failures: unknown[] = [];
      for (const connection of [...active]) await connection.close().catch((error) => failures.push(error));
      const namespaces = [publicDatabaseNames(region)];
      if (lastSubject) namespaces.push(await authenticatedDatabaseNames(region, config.identity.issuer, requireOAuthClientId(), lastSubject, crypto));
      for (const names of namespaces) {
        try {
          const [pay, way] = await openDatabasePair(names, databaseFactory);
          await createPairCloser(pay, way)(true);
        } catch (error) { failures.push(error); }
      }
      auth.clearSession();
      lastSubject = undefined;
      if (failures.length) throw new AggregateError(failures, "local data cleanup was incomplete");
    },
    close() {
      if (!closing) {
        closed = true;
        closing = Promise.allSettled([...active].map((connection) => connection.close())).then((results) => {
          const failures = results.filter((result): result is PromiseRejectedResult => result.status === "rejected");
          if (failures.length) throw new AggregateError(failures.map((item) => item.reason), "one or more SDK connections failed to close");
        });
      }
      return closing;
    },
  };
}

function createPublicConnection(pay: PowerSyncDatabase, way: PowerSyncDatabase, initial: ServiceSyncStates, closer: (clear?: boolean) => Promise<void>, region: Region, site: string): PublicCatalogConnection {
  const subscriptions = new Set<() => void>();
  return {
    getStates: () => currentStates(pay, way, initial),
    subscribe: (listener) => registerSubscription(subscriptions, watchDatabases(pay, way, listener, () => listener())),
    close: () => { for (const unsubscribe of subscriptions) unsubscribe(); subscriptions.clear(); return closer(); },
    async getCatalog() {
      const [productRows, modelRows, priceRows] = await Promise.all([
        pay.getAll<{ product_id: string; title: string; description: string; billing_mode: string; price_text: string; status: string }>("SELECT product_id,title,description,billing_mode,price_text,status FROM product_listings WHERE site=? AND status='active' ORDER BY display_order,id", [site]),
        way.getAll<{ model_id: string; title: string; family: string; description: string; context: string; latency: string; availability: string }>("SELECT model_id,title,family,description,context,latency,availability FROM model_listings WHERE availability='available' ORDER BY display_order,id"),
        way.getAll<{ model_id: string; metric: string; unit_size: number; price_microcredits: number }>("SELECT model_id,metric,unit_size,price_microcredits FROM model_customer_prices ORDER BY model_id,metric"),
      ]);
      const rates = new Map<string, Model["rates"]>();
      for (const row of priceRows) (rates.get(row.model_id) ?? (rates.set(row.model_id, []), rates.get(row.model_id)!)).push({ metric: row.metric, unitSize: Number(row.unit_size), microcreditsPerUnit: Number(row.price_microcredits) });
      return {
        products: productRows.map((row) => ({ id: row.product_id, title: row.title, description: row.description, billingMode: row.billing_mode, priceText: row.price_text, status: row.status })),
        models: modelRows.map((row) => ({ id: row.model_id, title: row.title, family: row.family, description: row.description, context: row.context, latency: row.latency, availability: row.availability, rates: rates.get(row.model_id) ?? [] })),
      };
    },
  };
}

function createAuthenticatedConnection(pay: PowerSyncDatabase, way: PowerSyncDatabase, initial: ServiceSyncStates, closer: (clear?: boolean) => Promise<void>, region: Region, mutations: MutationCoordinator, onError?: (error: Error) => void): AuthenticatedConnection {
  const subscriptions = new Set<() => void>();
  return {
    gizpay: new PowerSyncGizPayRepository(pay, mutations),
    gizway: new PowerSyncGizWayRepository(way, region, mutations),
    getStates: () => currentStates(pay, way, initial),
    subscribe: (listener) => registerSubscription(subscriptions, watchDatabases(pay, way, listener, onError ?? (() => undefined))),
    close: () => { for (const unsubscribe of subscriptions) unsubscribe(); subscriptions.clear(); mutations.cancelAll(); return closer(); },
  };
}

function registerSubscription(subscriptions: Set<() => void>, stop: () => void): () => void {
  const unsubscribe = () => { if (!subscriptions.delete(unsubscribe)) return; stop(); };
  subscriptions.add(unsubscribe);
  return unsubscribe;
}

class ReadOnlyCatalogConnector implements PowerSyncBackendConnector {
  constructor(private readonly endpoint: string, private readonly token: () => Promise<string>) {}
  async fetchCredentials() { return { endpoint: this.endpoint, token: await this.token() }; }
  async uploadData(database: PowerSyncDatabase) { if (await database.getCrudBatch(1)) throw new Error("public Catalog connection is read-only"); }
}
