import type { CommonPowerSyncDatabase, PowerSyncBackendConnector, PowerSyncCredentials } from "@powersync/web";
import type { Fetch } from "../../config";

export type MutationError = { table: string; id: string; code: string; message: string; status: number };
export type MutationSuccess = { table: string; id: string; status: number; body?: unknown };
export type ConnectorConfig = {
  endpoint: string;
  token: () => Promise<string>;
  apiBaseURL: string;
  fetcher?: Fetch;
  onMutationError?: (error: MutationError) => void;
  onMutationSuccess?: (success: MutationSuccess) => void;
  sleep?: (milliseconds: number) => Promise<void>;
  now?: () => number;
};

export abstract class APIConnector implements PowerSyncBackendConnector {
  private credentialsInvalidated = false;
  constructor(protected readonly config: ConnectorConfig) {}
  async fetchCredentials(): Promise<PowerSyncCredentials> {
    const token = await this.config.token();
    this.credentialsInvalidated = false;
    return { endpoint: this.config.endpoint, token };
  }
  invalidateCredentials(): void { this.credentialsInvalidated = true; }
  hasInvalidCredentials(): boolean { return this.credentialsInvalidated; }

  async uploadData(database: CommonPowerSyncDatabase): Promise<void> {
    const batch = await database.getCrudBatch(1);
    if (batch == null) return;
    const successes: MutationSuccess[] = [];
    for (const entry of batch.crud) {
      const request = await this.mapEntry(entry.table, entry.id, entry.op, entry.opData ?? {}, database);
      let response: Response;
      try {
        response = await (this.config.fetcher ?? globalThis.fetch)(this.config.apiBaseURL + request.path, {
          method: request.method,
          credentials: "omit",
          headers: { Authorization: `Bearer ${await this.config.token()}`, "Content-Type": "application/json" },
          body: request.body == null ? undefined : JSON.stringify(request.body),
        });
      } catch (error) { throw new Error("temporary upload network failure", { cause: error }); }
      if (response.status === 429) {
        const delay = retryAfterMilliseconds(response.headers.get("Retry-After"), this.config.now?.() ?? Date.now());
        if (delay > 0) await (this.config.sleep ?? defaultSleep)(delay);
        throw new Error("temporary upload failure: 429");
      }
      if (response.status >= 500) throw new Error(`temporary upload failure: ${response.status}`);
      if (!response.ok) {
        let value: unknown;
        try { value = await response.json(); } catch { throw new Error(`invalid ErrorResponse from upload API: ${response.status}`); }
        if (!isErrorResponse(value)) throw new Error(`invalid ErrorResponse from upload API: ${response.status}`);
        await batch.complete();
        for (const success of successes) this.config.onMutationSuccess?.(success);
        this.config.onMutationError?.({ table: entry.table, id: entry.id, status: response.status, ...value.error });
        return;
      }
      let body: unknown;
      if (response.status !== 204) try { body = await response.clone().json(); } catch { body = undefined; }
      successes.push({ table: entry.table, id: entry.id, status: response.status, body });
    }
    await batch.complete();
    for (const success of successes) this.config.onMutationSuccess?.(success);
  }

  protected abstract mapEntry(table: string, id: string, operation: string, data: Record<string, unknown>, database: CommonPowerSyncDatabase):
    { method: string; path: string; body?: Record<string, unknown> } | Promise<{ method: string; path: string; body?: Record<string, unknown> }>;
}

export function retryAfterMilliseconds(value: string | null, now: number): number {
  if (value == null) return 0;
  const seconds = Number(value.trim());
  if (Number.isFinite(seconds) && seconds >= 0) return Math.ceil(seconds * 1000);
  const date = Date.parse(value);
  return Number.isFinite(date) ? Math.max(0, date - now) : 0;
}

function defaultSleep(milliseconds: number): Promise<void> { return new Promise((resolve) => setTimeout(resolve, milliseconds)); }
function isErrorResponse(value: unknown): value is { error: { code: string; message: string } } {
  if (value == null || typeof value !== "object") return false;
  const error = (value as { error?: unknown }).error;
  return error != null && typeof error === "object"
    && typeof (error as { code?: unknown }).code === "string"
    && /^[a-z0-9_]+$/.test((error as { code: string }).code)
    && typeof (error as { message?: unknown }).message === "string"
    && (error as { message: string }).message.length > 0;
}
