import { mkdtemp, rm } from 'node:fs/promises';
import { tmpdir } from 'node:os';
import { join } from 'node:path';
import { PowerSyncDatabase, type Schema } from '@powersync/node';

export type SyncEnvironment = {
  gizpayEndpoint: string;
  gizwayGlobalEndpoint: string;
  gizwayCNEndpoint: string;
  token: string;
  tokenTwo: string;
  invalidAudienceToken: string;
  humanToken: string;
  cnCatalogToken: string;
  globalCatalogToken: string;
  subscriptionKey: string;
  payURL: string;
  wayURL: string;
  cnURL: string;
};

export function loadEnvironment(): SyncEnvironment | null {
  const env = {
    gizpayEndpoint: process.env.M03_POWERSYNC_GIZPAY_ENDPOINT ?? '',
    gizwayGlobalEndpoint: process.env.M03_POWERSYNC_GLOBAL_ENDPOINT ?? '',
    gizwayCNEndpoint: process.env.M03_POWERSYNC_CN_ENDPOINT ?? '',
    token: process.env.M03_POWERSYNC_TOKEN ?? '',
    tokenTwo: process.env.M03_POWERSYNC_TOKEN_TWO ?? '',
    invalidAudienceToken: process.env.M03_POWERSYNC_INVALID_AUDIENCE_TOKEN ?? '',
    humanToken: process.env.M03_HUMAN_TOKEN ?? process.env.M03_POWERSYNC_TOKEN ?? '',
    cnCatalogToken: process.env.M04_POWERSYNC_CN_CATALOG_TOKEN ?? '',
    globalCatalogToken: process.env.M04_POWERSYNC_GLOBAL_CATALOG_TOKEN ?? '',
    subscriptionKey: process.env.M03_SUBSCRIPTION_KEY ?? '',
    payURL: process.env.M03_PAY_URL ?? '',
    wayURL: process.env.M03_GLOBAL_URL ?? '',
    cnURL: process.env.M03_CN_URL ?? ''
  };
  return env.gizpayEndpoint && env.gizwayGlobalEndpoint && env.token ? env : null;
}

export async function createAIUsage(baseURL: string, subscriptionKey: string) {
  if (baseURL === '' || subscriptionKey === '') throw new Error('AI usage fixture endpoint and Subscription Key are required');
  const response = await fetch(`${baseURL}/openai/v1/chat/completions`, {
    method: 'POST',
    headers: { Authorization: `Bearer ${subscriptionKey}`, 'Content-Type': 'application/json' },
    body: JSON.stringify({ model: 'story-text', messages: [{ role: 'user', content: 'powersync fixture' }], stream: false })
  });
  if (!response.ok) throw new Error(`AI usage fixture returned ${response.status}: ${await response.text()}`);
}

export async function temporaryDatabase(schema: Schema, name: string) {
  const directory = await mkdtemp(join(tmpdir(), 'gizway-m03-powersync-'));
  const database = new PowerSyncDatabase({
    schema,
    database: { dbFilename: `${name}.db`, dbLocation: directory, implementation: { type: 'node:sqlite' } }
  });
  return {
    database,
    directory,
    async cleanup() {
      await database.close();
      await rm(directory, { recursive: true, force: true });
    }
  };
}

export async function waitForFirstSync(database: PowerSyncDatabase, timeout = 30_000) {
	await database.waitForFirstSync(AbortSignal.timeout(timeout));
}

export async function waitForRow(
  database: PowerSyncDatabase,
  sql: string,
  parameters: unknown[],
  predicate: (row: Record<string, unknown>) => boolean
) {
  for await (const result of database.watch(sql, parameters, {
    signal: AbortSignal.timeout(30_000),
    triggerImmediate: true
  })) {
    const row = result.array[0] as Record<string, unknown> | undefined;
    if (row != null && predicate(row)) return row;
  }
  throw new Error(`query did not reach expected state: ${sql}`);
}
