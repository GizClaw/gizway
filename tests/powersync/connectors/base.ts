import type { CommonPowerSyncDatabase, PowerSyncBackendConnector, PowerSyncCredentials } from '@powersync/node';

export type ConnectorConfig = {
  endpoint: string;
  token: string;
  apiBaseURL: string;
};

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
      if (response.status >= 500) {
        throw new Error(`temporary upload failure: ${response.status}`);
      }
      if (!response.ok) {
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
