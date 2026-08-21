import { describe, expect, test } from 'vitest';
import { GizWayConnector } from '../connectors/gizway.js';
import { GizWaySchema } from '../schemas/gizway.js';
import { createAIUsage, loadEnvironment, temporaryDatabase, waitForFirstSync } from './helpers.js';

describe('PowerSync GizWay', () => {
  test('syncs regional catalog, owner Provider Keys, and Usage', async (context) => {
    const env = loadEnvironment();
    if (env == null) return context.skip();
    await createAIUsage(env.wayURL, env.subscriptionKey);
    const local = await temporaryDatabase(GizWaySchema, 'gizway-global-sync');
    try {
      await local.database.connect(new GizWayConnector({ endpoint: env.gizwayGlobalEndpoint, token: env.token, apiBaseURL: env.wayURL }));
      await waitForFirstSync(local.database);
      expect(await local.database.getAll('SELECT id FROM models')).not.toHaveLength(0);
      expect(await local.database.getAll('SELECT id,key FROM my_provider_keys')).not.toHaveLength(0);
      const orders = await local.database.getAll<{
        external_order_id: string; provider_id: string; completed_at: string;
      }>('SELECT external_order_id,provider_id,completed_at FROM my_ai_orders');
      expect(orders).not.toHaveLength(0);
      expect(orders.every((order) => order.external_order_id !== '' && order.provider_id !== '' && order.completed_at != null)).toBe(true);
      expect(await local.database.getAll('SELECT id,model_id,metric,quantity FROM my_ai_usage')).not.toHaveLength(0);
    } finally {
      await local.cleanup();
    }
  });
});
