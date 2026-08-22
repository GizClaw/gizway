import type { PowerSyncBackendConnector, PowerSyncDatabase } from "@powersync/web";
import type { ServiceSyncState, ServiceSyncStates } from "../client";

type CredentialAwareConnector = PowerSyncBackendConnector & { hasInvalidCredentials?: () => boolean };

export class MutationCoordinator {
  private readonly waiters = new Map<string, { resolve: () => void; reject: (error: Error) => void }>();
  async run(table: string, id: string, write: () => Promise<unknown>): Promise<void> {
    const key = `${table}:${id}`;
    if (this.waiters.has(key)) throw new Error(`mutation ${key} is already pending`);
    const result = new Promise<void>((resolve, reject) => this.waiters.set(key, { resolve, reject }));
    try { await write(); } catch (error) { this.waiters.delete(key); throw error; }
    return result;
  }
  succeed(table: string, id: string): void { this.take(table, id)?.resolve(); }
  reject(table: string, id: string, error: Error): void { this.take(table, id)?.reject(error); }
  cancelAll(error = new Error("PowerSync connection closed")): void {
    for (const waiter of this.waiters.values()) waiter.reject(error);
    this.waiters.clear();
  }
  private take(table: string, id: string) { const key = `${table}:${id}`; const waiter = this.waiters.get(key); this.waiters.delete(key); return waiter; }
}

export async function connectService(database: PowerSyncDatabase, connector: CredentialAwareConnector, signal?: AbortSignal): Promise<ServiceSyncState> {
  try {
    signal?.throwIfAborted();
    await connector.fetchCredentials();
    await database.connect(connector);
    const timeout = AbortSignal.timeout(60_000);
    const combined = signal ? AbortSignal.any([signal, timeout]) : timeout;
    let final = database.currentStatus;
    await database.waitForStatus((status) => { final = status; return status.hasSynced === true || status.downloadError != null; }, combined);
    return syncState(final, connector.hasInvalidCredentials?.() === true ? "denied" : "first_sync");
  } catch (error) {
    if (signal?.aborted) throw error;
    return connector.hasInvalidCredentials?.() === true || isAuthError(error) ? "denied" : "sync_error";
  }
}

export function currentStates(pay: PowerSyncDatabase, way: PowerSyncDatabase, initial: ServiceSyncStates): ServiceSyncStates {
  return { gizpay: syncState(pay.currentStatus, initial.gizpay), gizway: syncState(way.currentStatus, initial.gizway) };
}

export function createPairCloser(pay: PowerSyncDatabase, way: PowerSyncDatabase, beforeClose?: () => void): (clear?: boolean) => Promise<void> {
  let closed = false;
  let closing: Promise<void> | undefined;
  return (clear = false) => {
    if (closed) return Promise.resolve();
    closing ??= (async () => {
      beforeClose?.();
      const disconnects = await Promise.allSettled([pay, way].map((db) => clear ? db.disconnectAndClear() : db.disconnect()));
      const closes = await Promise.allSettled([pay.close(), way.close()]);
      closed = true;
      const failures = [...disconnects, ...closes].filter((result): result is PromiseRejectedResult => result.status === "rejected");
      if (failures.length) throw new AggregateError(failures.map((failure) => failure.reason), "one or more PowerSync databases failed to close");
    })().finally(() => { closing = undefined; });
    return closing;
  };
}

export function watchDatabases(pay: PowerSyncDatabase, way: PowerSyncDatabase, listener: () => void, onError: (error: Error) => void): () => void {
  const controller = new AbortController();
  const statusListeners = [pay, way].map((db) => db.registerListener({ statusChanged: listener }));
  const queries: Array<[PowerSyncDatabase, string]> = [
    [pay, "SELECT id,email,display_name,merchant_id,status FROM my_profile"],
    [pay, "SELECT id,status,created_at FROM my_accounts"],
    [pay, "SELECT id,balance_microcredits FROM my_balances"],
    [pay, "SELECT id,public_name,is_default,status,updated_at FROM my_merchants"],
    [pay, "SELECT id,name,status,terms_version FROM available_products"],
    [pay, "SELECT id,product_id,site,title,description,price_text,display_order,status,updated_at FROM product_listings"],
    [pay, "SELECT id,product_id,status,terms_version,created_at FROM my_subscriptions"],
    [pay, "SELECT id,subscription_id,name,key,status,last_used_at,revoked_at FROM my_subscription_keys"],
    [pay, "SELECT id,amount_microcredits,status,created_at,credited_at FROM my_topups"],
    [pay, "SELECT id,external_order_id,gross_microcredits,created_at FROM my_charges"],
    [pay, "SELECT id,transaction_type,amount_microcredits,created_at FROM my_transactions"],
    [pay, "SELECT id,charge_id,amount_microcredits,created_at FROM my_commissions"],
    [way, "SELECT id,name,provider_id,status FROM models"],
    [way, "SELECT id,name,kind,status FROM providers"],
    [way, "SELECT id,model_id,title,description,family,context,latency,display_order,availability,updated_at FROM model_listings"],
    [way, "SELECT id,model_id,metric,unit_size,price_microcredits FROM model_customer_prices"],
    [way, "SELECT id,model_id,metric,quantity,status,created_at FROM my_ai_usage"],
    [way, "SELECT id,subscription_key_id,gross_microcredits,status,created_at,completed_at FROM my_ai_orders"],
    [way, "SELECT id,name,status,last_used_at,earned_microcredits,prices_json,updated_at FROM my_provider_keys"],
  ];
  for (const [database, sql] of queries) void (async () => {
    try { for await (const result of database.watch(sql, [], { signal: controller.signal, triggerImmediate: false })) { void result; listener(); } }
    catch (error) { if (!controller.signal.aborted) onError(error instanceof Error ? error : new Error(String(error))); }
  })();
  return () => { controller.abort(); for (const dispose of statusListeners) dispose(); };
}

function syncState(status: PowerSyncDatabase["currentStatus"], fallback: ServiceSyncState): ServiceSyncState {
  if (status.connected && status.hasSynced === true) return "ready";
  if (status.hasSynced === true) return "offline";
  if (status.downloadError) return isAuthError(status.downloadError) ? "denied" : "sync_error";
  return fallback;
}
function isAuthError(error: unknown): boolean {
  const message = error instanceof Error ? `${error.name}: ${error.message}` : String(error);
  return /(?:PSYNC_S21|\b401\b|\b403\b|authenticat|authori[sz]|unauthori[sz]|forbidden|denied|credential|\bJWT\b|audience|issuer|signature|expired)/i.test(message);
}
