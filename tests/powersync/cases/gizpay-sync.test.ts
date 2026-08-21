import { describe, expect, test } from 'vitest';
import { GizPayConnector } from '../connectors/gizpay.js';
import { GizPaySchema } from '../schemas/gizpay.js';
import { createAIUsage, loadEnvironment, temporaryDatabase, waitForFirstSync, waitForRow } from './helpers.js';

describe('PowerSync GizPay', () => {
  test('syncs the authenticated user data into SQLite', async (context) => {
    const env = loadEnvironment();
    if (env == null) return context.skip();
    await createAIUsage(env.wayURL, env.subscriptionKey);
    const local = await temporaryDatabase(GizPaySchema, 'gizpay-sync');
    try {
      await local.database.connect(new GizPayConnector({ endpoint: env.gizpayEndpoint, token: env.token, apiBaseURL: env.payURL }));
      await waitForFirstSync(local.database);
      expect(await local.database.getAll('SELECT id FROM my_accounts')).not.toHaveLength(0);
      expect(await local.database.getAll('SELECT id,product_id,status FROM my_subscriptions')).not.toHaveLength(0);
      expect(await local.database.getAll('SELECT id,key FROM my_subscription_keys')).not.toHaveLength(0);
      expect(await local.database.getAll('SELECT id FROM available_products')).not.toHaveLength(0);
      const merchants = await local.database.getAll<{ is_default: number }>(
        'SELECT is_default FROM my_merchants WHERE is_default = 1'
      );
      expect(merchants).not.toHaveLength(0);
      const charge = await waitForRow(
        local.database,
        'SELECT order_snapshot FROM my_charges LIMIT 1',
        [],
        (row) => typeof row.order_snapshot === 'string' && row.order_snapshot.length > 2
      );
      expect(charge.order_snapshot).toBeDefined();
    } finally {
      await local.cleanup();
    }
  });
});
