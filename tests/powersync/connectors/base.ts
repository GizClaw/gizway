import type { CommonPowerSyncDatabase, PowerSyncBackendConnector, PowerSyncCredentials } from '@powersync/node';

export type ConnectorConfig = {
  endpoint: string;
  token: string;
  apiBaseURL: string;
  onMutationError?: (error: MutationError) => void;
  sleep?: (milliseconds: number) => Promise<void>;
  now?: () => number;
};

export type MutationError = { table: string; id: string; code: string; message: string; status: number };

export abstract class APIConnector implements PowerSyncBackendConnector {
  constructor(protected readonly config: ConnectorConfig) {}

  async fetchCredentials(): Promise<PowerSyncCredentials> {
    return { endpoint: this.config.endpoint, token: this.config.token };
  }

  async uploadData(database: CommonPowerSyncDatabase): Promise<void> {
    // Process one operation at a time so a deterministic rejection is isolated
    // without discarding later valid mutations from the queue.
    const batch = await database.getCrudBatch(1);
    if (batch == null) return;
    for (const entry of batch.crud) {
      const request = await this.mapEntry(entry.table, entry.id, entry.op, entry.opData ?? {}, database);
      const response = await fetch(this.config.apiBaseURL + request.path, {
        method: request.method,
        headers: { Authorization: `Bearer ${this.config.token}`, 'Content-Type': 'application/json' },
        body: request.body == null ? undefined : JSON.stringify(request.body)
      });
      if (response.status === 429) {
        const delay = retryAfterMilliseconds(response.headers.get('Retry-After'), this.config.now?.() ?? Date.now());
        if (delay > 0) await (this.config.sleep ?? defaultSleep)(delay);
        throw new Error(`temporary upload failure: ${response.status}`);
      }
      if (response.status >= 500) {
        throw new Error(`temporary upload failure: ${response.status}`);
      }
      if (!response.ok) {
		let value: unknown;
		try {
			value = await response.json();
		} catch {
			throw new Error(`invalid ErrorResponse from upload API: ${response.status}`);
		}
		if (!isErrorResponse(value)) {
			throw new Error(`invalid ErrorResponse from upload API: ${response.status}`);
		}
		this.config.onMutationError?.({ table: entry.table, id: entry.id, status: response.status, ...value.error });
		// A deterministic business rejection must not stall every later local mutation.
        await batch.complete();
        return;
      }
    }
    await batch.complete();
  }

  protected abstract mapEntry(
    table: string,
    id: string,
    operation: string,
    data: Record<string, unknown>,
    database: CommonPowerSyncDatabase
  ): { method: string; path: string; body?: Record<string, unknown> } | Promise<{ method: string; path: string; body?: Record<string, unknown> }>;
}

function retryAfterMilliseconds(value: string | null, now: number): number {
	if (value == null) return 0;
	const seconds = Number(value.trim());
	if (Number.isFinite(seconds) && seconds >= 0) return Math.ceil(seconds * 1000);
	const date = Date.parse(value);
	return Number.isFinite(date) ? Math.max(0, date - now) : 0;
}

function defaultSleep(milliseconds: number): Promise<void> {
	return new Promise((resolve) => setTimeout(resolve, milliseconds));
}

function isErrorResponse(value: unknown): value is { error: { code: string; message: string } } {
	if (value == null || typeof value !== 'object') return false;
	const error = (value as { error?: unknown }).error;
	return error != null && typeof error === 'object'
		&& typeof (error as { code?: unknown }).code === 'string'
		&& /^[a-z0-9_]+$/.test((error as { code: string }).code)
		&& typeof (error as { message?: unknown }).message === 'string'
		&& (error as { message: string }).message.length > 0;
}
