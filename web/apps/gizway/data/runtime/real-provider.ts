import type { PowerSyncBackendConnector, PowerSyncDatabase } from "@powersync/web";
import type { Region } from "@/data/contracts/gizway";
import { PowerSyncGizPayRepository } from "@/data/powersync/gizpay/repository";
import { PowerSyncGizWayRepository } from "@/data/powersync/gizway/repository";
import { GizPayConnector } from "@/data/powersync/connectors/gizpay";
import { GizWayConnector } from "@/data/powersync/connectors/gizway";
import { openRuntimeGizPayDatabase, openRuntimeGizWayDatabase } from "@/data/powersync/database";
import type { PrototypeDataProvider, ServiceSyncState, ServiceSyncStates } from "@/data/provider";
import type { PublicRuntimeConfig } from "./config";
import type { MutationError, MutationSuccess } from "@/data/powersync/connectors/base";

type MutationWaiter = {
  resolve: (success: MutationSuccess) => void;
  reject: (error: Error) => void;
};

export class MutationCoordinator {
  private readonly waiters = new Map<string, MutationWaiter>();

  async run(table: string, id: string, write: () => Promise<unknown>): Promise<MutationSuccess> {
    const key = `${table}:${id}`;
    if (this.waiters.has(key)) throw new Error(`mutation ${key} is already pending`);
    const result = new Promise<MutationSuccess>((resolve, reject) => {
      this.waiters.set(key, { resolve, reject });
    });
    try {
      await write();
    } catch (error) {
      this.take(table, id);
      throw error;
    }
    return result;
  }

  succeed(success: MutationSuccess) {
    const waiter = this.take(success.table, success.id);
    waiter?.resolve(success);
  }

  reject(table: string, id: string, error: Error) {
    const waiter = this.take(table, id);
    waiter?.reject(error);
  }

  cancelAll(error = new Error("PowerSync connection closed")) {
    for (const [key, waiter] of this.waiters) {
      waiter.reject(error);
      this.waiters.delete(key);
    }
  }

  private take(table: string, id: string): MutationWaiter | undefined {
    const key = `${table}:${id}`;
    const waiter = this.waiters.get(key);
    if (!waiter) return undefined;
    this.waiters.delete(key);
    return waiter;
  }
}

export type ConnectedProvider = PrototypeDataProvider & {
  gizPayDatabase: PowerSyncDatabase;
  gizWayDatabase: PowerSyncDatabase;
  mutations: MutationCoordinator;
  syncStates: () => ServiceSyncStates;
  selectPAYGProduct: (productID: string) => void;
  close: (clear?: boolean) => Promise<void>;
};

export async function connectHumanDatabases(config: PublicRuntimeConfig, region: Region, subject: string, token: () => Promise<string>, onMutationError: (message: string) => void, signal?: AbortSignal): Promise<ConnectedProvider> {
  const namespace = `${region}-${subject.replaceAll(/[^A-Za-z0-9_-]/g, "_")}`;
  const [gizPayDatabase, gizWayDatabase] = await openDatabasePair(() => openRuntimeGizPayDatabase(namespace), () => openRuntimeGizWayDatabase(region, namespace));
  const mutations = new MutationCoordinator();
  const closeDatabases = createDatabaseCloser(gizPayDatabase, gizWayDatabase, () => mutations.cancelAll());
  const mutation = (error: MutationError) => {
    const failure = new Error(`${error.code}: ${error.message}`);
    mutations.reject(error.table, error.id, failure);
    onMutationError(failure.message);
  };
  const success = (value: MutationSuccess) => mutations.succeed(value);
  const closeOnAbort = () => { void closeDatabases().catch(() => undefined); };
  signal?.addEventListener("abort", closeOnAbort, { once: true });
  try {
    signal?.throwIfAborted();
    const connections = await Promise.all([
      connectPowerSyncService(gizPayDatabase, new GizPayConnector({ endpoint: config.services.gizpay_powersync_url, apiBaseURL: config.services.gizpay_api_url, token, onMutationError: mutation, onMutationSuccess: success }), signal),
      connectPowerSyncService(gizWayDatabase, new GizWayConnector({ endpoint: config.services.gizway_powersync_url, apiBaseURL: config.services.gizway_api_url, token, onMutationError: mutation, onMutationSuccess: success }), signal),
    ]);
    signal?.throwIfAborted();
    const gizpay = new PowerSyncGizPayRepository(gizPayDatabase, mutations);
    const subscribe = (listener: () => void) => watchDatabases(gizPayDatabase, gizWayDatabase, listener, onMutationError);
    return {
      gizpay,
      gizway: new PowerSyncGizWayRepository(gizWayDatabase, mutations),
      gizPayDatabase,
      gizWayDatabase,
      mutations,
      syncStates: () => ({ gizpay: serviceSyncState(gizPayDatabase, connections[0]), gizway: serviceSyncState(gizWayDatabase, connections[1]) }),
      selectPAYGProduct: (productID: string) => gizpay.selectPAYGProduct(productID),
      subscribe,
      close: closeDatabases,
    };
  } catch (error) {
    await closeDatabases().catch(() => undefined);
    throw error;
  } finally {
    signal?.removeEventListener("abort", closeOnAbort);
  }
}

type SyncablePowerSyncDatabase = Pick<PowerSyncDatabase, "connect" | "waitForStatus" | "currentStatus"> & Partial<Pick<PowerSyncDatabase, "registerListener">>;
type CredentialAwareConnector = PowerSyncBackendConnector & { hasInvalidCredentials?: () => boolean; hasFetchedCredentials?: () => boolean };

export function maintainPowerSyncService(database: SyncablePowerSyncDatabase, connector: CredentialAwareConnector, onState: (state: ServiceSyncState) => void, signal?: AbortSignal, retryDelayMs = 1_000): { state: () => ServiceSyncState; stop: () => void } {
  let current: ServiceSyncState = "first_sync";
  let stopped = false;
  let timer: ReturnType<typeof setTimeout> | undefined;
  const removeStatusListener = database.registerListener?.({ statusChanged: (status) => {
    if (stopped) return;
    const fallback = connector.hasInvalidCredentials?.() === true ? "denied" : current;
    current = syncStateFromStatus(status, fallback);
    onState(current);
  } });
  const stop = () => {
    stopped = true;
    clearTimeout(timer);
    signal?.removeEventListener("abort", stop);
    removeStatusListener?.();
  };
  signal?.addEventListener("abort", stop, { once: true });
  const attempt = async () => {
    if (stopped || signal?.aborted) return;
    const next = await connectPowerSyncService(database, connector, signal);
    if (stopped || signal?.aborted) return;
    current = next;
    onState(next);
    if (current === "sync_error" && connector.hasFetchedCredentials?.() === false && !stopped) {
      timer = setTimeout(() => { void attempt(); }, retryDelayMs);
    }
  };
  void attempt();
  return { state: () => current, stop };
}

export async function connectPowerSyncService(database: SyncablePowerSyncDatabase, connector: CredentialAwareConnector, signal?: AbortSignal): Promise<ServiceSyncState> {
  try {
    signal?.throwIfAborted();
    await connector.fetchCredentials();
    signal?.throwIfAborted();
    await database.connect(connector);
    const timeout = AbortSignal.timeout(60_000);
    const syncSignal = signal ? AbortSignal.any([signal, timeout]) : timeout;
    let terminalStatus: PowerSyncDatabase["currentStatus"] | undefined;
    await database.waitForStatus((status) => {
      if (status.hasSynced !== true && status.downloadError == null) return false;
      terminalStatus = status;
      return true;
    }, syncSignal);
    signal?.throwIfAborted();
    const fallback = connector.hasInvalidCredentials?.() === true ? "denied" : "first_sync";
    return terminalStatus == null ? serviceSyncState(database, fallback) : syncStateFromStatus(terminalStatus, fallback);
  } catch (error) {
    if (signal?.aborted) throw error;
    const fallback = connector.hasInvalidCredentials?.() === true ? "denied" : classifySyncError(error);
    return serviceSyncState(database, fallback);
  }
}

export function serviceSyncState(database: Pick<PowerSyncDatabase, "currentStatus">, fallback: ServiceSyncState = "first_sync"): ServiceSyncState {
  return syncStateFromStatus(database.currentStatus, fallback);
}

function syncStateFromStatus(status: PowerSyncDatabase["currentStatus"], fallback: ServiceSyncState): ServiceSyncState {
  if (status.connected && status.hasSynced === true) return "ready";
  if (status.downloadError) {
    const current = classifySyncError(status.downloadError);
    return fallback === "denied" && current === "sync_error" && status.hasSynced !== true ? "denied" : current;
  }
  if (status.hasSynced === true) return "offline";
  return fallback;
}

function classifySyncError(error: unknown): ServiceSyncState {
  const message = syncErrorText(error);
  return /(?:PSYNC_S21|\b401\b|\b403\b|authenticat|authori[sz]at|unauthori[sz]ed|forbidden|\bdenied\b|credential|\bJWT\b|audience|issuer|signature|expired)/i.test(message) ? "denied" : "sync_error";
}

function syncErrorText(error: unknown): string {
  if (!(error instanceof Error)) return String(error);
  return `${error.name}: ${error.message}${error.cause == null ? "" : `; caused by ${syncErrorText(error.cause)}`}`;
}

type ClosablePowerSyncDatabase = Pick<PowerSyncDatabase, "disconnect" | "disconnectAndClear" | "close">;

export async function openDatabasePair<T extends Pick<PowerSyncDatabase, "close">>(openPay: () => Promise<T>, openWay: () => Promise<T>): Promise<[T, T]> {
  const pay = await openPay();
  try {
    return [pay, await openWay()];
  } catch (error) {
    await pay.close().catch(() => undefined);
    throw error;
  }
}

export async function waitForInitialSync(pay: Pick<PowerSyncDatabase, "waitForFirstSync">, way: Pick<PowerSyncDatabase, "waitForFirstSync">, signal?: AbortSignal): Promise<void> {
  const timeout = AbortSignal.timeout(60_000);
  const syncSignal = signal ? AbortSignal.any([signal, timeout]) : timeout;
  await Promise.all([pay.waitForFirstSync(syncSignal), way.waitForFirstSync(syncSignal)]);
}

export function createDatabaseCloser(pay: ClosablePowerSyncDatabase, way: ClosablePowerSyncDatabase, beforeClose?: () => void): (clear?: boolean) => Promise<void> {
  let closing: Promise<void> | undefined;
  return (clear = false) => {
    closing ??= (async () => {
      beforeClose?.();
      const databases = [pay, way];
      const disconnects = await Promise.allSettled(databases.map((database) => clear ? database.disconnectAndClear() : database.disconnect()));
      const closes = await Promise.allSettled(databases.map((database) => database.close()));
      const rejected = [...disconnects, ...closes].find((result): result is PromiseRejectedResult => result.status === "rejected");
      if (rejected) throw rejected.reason;
    })();
    return closing;
  };
}

export function watchDatabases(pay: PowerSyncDatabase, way: PowerSyncDatabase, listener: () => void, onError: (message: string) => void): () => void {
  const watches: Array<[PowerSyncDatabase, string]> = [
    [pay, "SELECT id,email,display_name,merchant_id,status FROM my_profile"],
    [pay, "SELECT id,balance_microcredits FROM my_balances"],
    [pay, "SELECT id,public_name,is_default,status,updated_at FROM my_merchants"],
    [pay, "SELECT id,name,status,terms_version FROM available_products"],
    [pay, "SELECT id,product_id,site,title,description,price_text,display_order,status,updated_at FROM product_listings"],
    [pay, "SELECT id,product_id,status,terms_version,created_at FROM my_subscriptions"],
    [pay, "SELECT id,subscription_id,name,key,status,last_used_at,revoked_at FROM my_subscription_keys"],
    [pay, "SELECT id,transaction_type,amount_microcredits,created_at FROM my_transactions"],
    [way, "SELECT id,name,provider_id,status FROM models"],
    [way, "SELECT id,name,kind,status FROM providers"],
    [way, "SELECT id,model_id,title,description,family,context,latency,accent,featured,display_order,availability,updated_at FROM model_listings"],
    [way, "SELECT id,model_id,metric,unit_size,price_microcredits FROM model_customer_prices"],
    [way, "SELECT id,model_id,metric,quantity,status,created_at FROM my_ai_usage"],
    [way, "SELECT id,subscription_key_id,gross_microcredits,status,created_at,completed_at FROM my_ai_orders"],
    [way, "SELECT id,name,status,last_used_at,earned_microcredits,prices_json,updated_at FROM my_provider_keys"],
  ];
  return watchQueries(pay, way, watches, listener, onError);
}

export function watchCatalogDatabases(pay: PowerSyncDatabase, way: PowerSyncDatabase, listener: () => void, onError: (message: string) => void): () => void {
  return watchQueries(pay, way, [
    [pay, "SELECT id,product_id,site,title,description,price_text,display_order,status,updated_at FROM product_listings"],
    [way, "SELECT id,model_id,title,description,family,display_order,availability,updated_at FROM model_listings"],
    [way, "SELECT id,model_id,metric,unit_size,price_microcredits FROM model_customer_prices"],
  ], listener, onError);
}

function watchQueries(pay: PowerSyncDatabase, way: PowerSyncDatabase, watches: Array<[PowerSyncDatabase, string]>, listener: () => void, onError: (message: string) => void): () => void {
  const controller = new AbortController();
  let timer: ReturnType<typeof setTimeout> | undefined;
  const changed = () => {
    clearTimeout(timer);
    timer = setTimeout(listener, 25);
  };
  for (const [database, sql] of watches) {
    void (async () => {
      try {
        for await (const result of database.watch(sql, [], { signal: controller.signal, triggerImmediate: false })) {
          void result;
          changed();
        }
      } catch (error) {
        if (!controller.signal.aborted) onError(error instanceof Error ? error.message : "PowerSync live query failed");
      }
    })();
  }
  const statusListeners = [pay, way].map((database) => database.registerListener({ statusChanged: changed }));
  return () => {
    controller.abort();
    clearTimeout(timer);
    for (const dispose of statusListeners) dispose();
  };
}

export async function ensurePAYGSubscription(database: PowerSyncDatabase, site: string, mutations: MutationCoordinator): Promise<{ productID: string; subscriptionID: string }> {
  const account = await database.getOptional<{ id: string }>("SELECT id FROM my_accounts WHERE status='active' ORDER BY created_at,id LIMIT 1");
  const product = await database.getOptional<{ product_id: string }>("SELECT product_id FROM product_listings WHERE site=? AND status='active' ORDER BY display_order,id LIMIT 1", [site]);
  if (!account || !product) throw new Error("PAYG account or Product Listing is not ready");
  const existing = await database.getOptional<{ id: string; status: string }>("SELECT id,status FROM my_subscriptions WHERE product_id=? ORDER BY created_at,id LIMIT 1", [product.product_id]);
  if (existing?.status === "active") return { productID: product.product_id, subscriptionID: existing.id };
  if (existing) throw new Error(`current PAYG Subscription is ${existing.status}`);
  const terms = await database.getOptional<{ terms_version: string }>("SELECT terms_version FROM available_products WHERE id=?", [product.product_id]);
  if (!terms) throw new Error("PAYG Product terms are not ready");
  const id = crypto.randomUUID();
  try {
    await mutations.run("my_subscriptions", id, () => database.execute("INSERT INTO my_subscriptions(id,account_id,product_id,status,terms_version,created_at) VALUES(?,?,?,'active',?,?)", [id, account.id, product.product_id, terms.terms_version, new Date().toISOString()]));
  } catch (error) {
    if (!(error instanceof Error) || !/^(?:resource_id_conflict|subscription_already_exists):/.test(error.message)) throw error;
    const winner = await waitForSubscription(database, product.product_id, id);
    return { productID: product.product_id, subscriptionID: winner };
  }
  return { productID: product.product_id, subscriptionID: id };
}

export function maintainPAYGSubscription(provider: ConnectedProvider, site: string, onState: (state: "waiting" | "activating" | "ready", error?: unknown) => void, retryDelayMs = 1_000): () => void {
  let stopped = false;
  let running = false;
  let ready = false;
  let timer: ReturnType<typeof setTimeout> | undefined;
  const schedule = (delay = retryDelayMs) => {
    if (stopped || ready || timer) return;
    timer = setTimeout(() => { timer = undefined; void activate(); }, delay);
  };
  const activate = async () => {
    if (stopped || ready || running) return;
    if (provider.syncStates().gizpay !== "ready") {
      onState("waiting");
      schedule();
      return;
    }
    running = true;
    onState("activating");
    try {
      const payg = await ensurePAYGSubscription(provider.gizPayDatabase, site, provider.mutations);
      if (stopped) return;
      provider.selectPAYGProduct(payg.productID);
      ready = true;
      onState("ready");
    } catch (error) {
      if (!stopped) {
        onState("waiting", error);
        schedule();
      }
    } finally {
      running = false;
    }
  };
  const unsubscribe = provider.subscribe?.(() => schedule(0));
  schedule(0);
  return () => {
    stopped = true;
    clearTimeout(timer);
    unsubscribe?.();
  };
}

async function waitForSubscription(database: PowerSyncDatabase, productID: string, rejectedID: string): Promise<string> {
  const deadline = Date.now() + 30_000;
  while (Date.now() < deadline) {
    const subscriptions = await database.getAll<{ id: string; status: string }>("SELECT id,status FROM my_subscriptions WHERE product_id=? ORDER BY created_at,id", [productID]);
    const winner = subscriptions.find((subscription) => subscription.id !== rejectedID);
    if (winner?.status === "active") return winner.id;
    if (winner) throw new Error(`current PAYG Subscription is ${winner.status}`);
    await new Promise((resolve) => setTimeout(resolve, 100));
  }
  throw new Error("PowerSync did not deliver the concurrently created PAYG Subscription within 30 seconds");
}

export class ReadOnlyCatalogConnector implements PowerSyncBackendConnector {
  private fetchedCredentials = false;
  private invalidCredentials = false;
  constructor(private readonly endpoint: string, private readonly token: () => Promise<string>) {}
  async fetchCredentials() {
    this.fetchedCredentials = false;
    this.invalidCredentials = false;
    const token = await this.token();
    this.fetchedCredentials = true;
    return { endpoint: this.endpoint, token };
  }
  hasFetchedCredentials() { return this.fetchedCredentials; }
  hasInvalidCredentials() { return this.invalidCredentials; }
  invalidateCredentials() { this.invalidCredentials = true; }
  async uploadData(database: PowerSyncDatabase) {
    if (await database.getCrudBatch(1)) throw new Error("Public Catalog connection is read-only");
  }
}
