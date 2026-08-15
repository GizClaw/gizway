import { describe, expect, test } from 'vitest';
import { GizPayConnector } from '../connectors/gizpay.js';
import { GizWayConnector } from '../connectors/gizway.js';
import { GizPaySchema } from '../schemas/gizpay.js';
import { GizWaySchema } from '../schemas/gizway.js';
import { loadEnvironment, temporaryDatabase, waitForFirstSync } from './helpers.js';

describe('PowerSync JWT isolation', () => {
  test('two users receive disjoint Accounts and Subscription Keys', async (context) => {
    const env = loadEnvironment();
    if (env == null || env.tokenTwo === '') return context.skip();
    const first = await temporaryDatabase(GizPaySchema, 'user-a');
    const second = await temporaryDatabase(GizPaySchema, 'user-b');
    try {
      await first.database.connect(new GizPayConnector({ endpoint: env.gizpayEndpoint, token: env.token, apiBaseURL: env.payURL }));
      await second.database.connect(new GizPayConnector({ endpoint: env.gizpayEndpoint, token: env.tokenTwo, apiBaseURL: env.payURL }));
      await Promise.all([waitForFirstSync(first.database), waitForFirstSync(second.database)]);
      const idsA = new Set((await first.database.getAll<{ id: string }>('SELECT id FROM my_accounts')).map((row) => row.id));
      const idsB = new Set((await second.database.getAll<{ id: string }>('SELECT id FROM my_accounts')).map((row) => row.id));
      expect([...idsA].filter((id) => idsB.has(id))).toEqual([]);
      const keysA = new Set((await first.database.getAll<{ key: string }>('SELECT key FROM my_subscription_keys')).map((row) => row.key));
      const keysB = new Set((await second.database.getAll<{ key: string }>('SELECT key FROM my_subscription_keys')).map((row) => row.key));
      expect([...keysA].filter((key) => keysB.has(key))).toEqual([]);
    } finally {
      await Promise.all([first.cleanup(), second.cleanup()]);
    }
  });

  test('another user receives no owner Provider Keys, AI Orders, or AI Usage', async (context) => {
    const env = loadEnvironment();
    if (env == null || env.tokenTwo === '') return context.skip();
    const owner = await temporaryDatabase(GizWaySchema, 'gizway-owner');
    const other = await temporaryDatabase(GizWaySchema, 'gizway-other');
    try {
      await owner.database.connect(new GizWayConnector({ endpoint: env.gizwayGlobalEndpoint, token: env.token, apiBaseURL: env.wayURL }));
      await other.database.connect(new GizWayConnector({ endpoint: env.gizwayGlobalEndpoint, token: env.tokenTwo, apiBaseURL: env.wayURL }));
      await Promise.all([waitForFirstSync(owner.database), waitForFirstSync(other.database)]);
      for (const table of ['my_provider_keys', 'my_ai_orders', 'my_ai_usage']) {
        expect(await owner.database.getAll(`SELECT id FROM ${table}`)).not.toHaveLength(0);
        expect(await other.database.getAll(`SELECT id FROM ${table}`)).toHaveLength(0);
      }
    } finally {
      await Promise.all([owner.cleanup(), other.cleanup()]);
    }
  });
});
