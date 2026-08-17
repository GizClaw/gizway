import { describe, expect, test } from 'vitest';
import { GizWayConnector } from '../connectors/gizway.js';
import { GizWaySchema } from '../schemas/gizway.js';
import { loadEnvironment, temporaryDatabase, waitForFirstSync } from './helpers.js';

describe('PowerSync region isolation', () => {
  test('CN and Global model IDs are disjoint', async (context) => {
    const env = loadEnvironment();
    if (env == null || env.gizwayCNEndpoint === '') return context.skip();
    const global = await temporaryDatabase(GizWaySchema, 'global');
    const cn = await temporaryDatabase(GizWaySchema, 'cn');
    try {
      await global.database.connect(new GizWayConnector({ endpoint: env.gizwayGlobalEndpoint, token: env.token, apiBaseURL: env.wayURL }));
      await cn.database.connect(new GizWayConnector({ endpoint: env.gizwayCNEndpoint, token: env.token, apiBaseURL: env.wayURL }));
      await Promise.all([waitForFirstSync(global.database), waitForFirstSync(cn.database)]);
      const globalIDs = new Set((await global.database.getAll<{ id: string }>('SELECT id FROM models')).map((row) => row.id));
      const cnIDs = new Set((await cn.database.getAll<{ id: string }>('SELECT id FROM models')).map((row) => row.id));
      expect([...globalIDs].filter((id) => cnIDs.has(id))).toEqual([]);
      const globalUsage = new Set((await global.database.getAll<{ model_id: string }>('SELECT model_id FROM my_ai_usage')).map((row) => row.model_id));
      const cnUsage = new Set((await cn.database.getAll<{ model_id: string }>('SELECT model_id FROM my_ai_usage')).map((row) => row.model_id));
      expect(globalUsage.size).toBeGreaterThan(0);
      expect(cnUsage.size).toBeGreaterThan(0);
      expect([...globalUsage].filter((id) => cnUsage.has(id))).toEqual([]);
			const usedProviderKeys = await global.database.getAll<{ last_used_at: string | null }>('SELECT last_used_at FROM my_provider_keys');
			expect(usedProviderKeys.length).toBeGreaterThan(0);
			expect(usedProviderKeys.every((key) => key.last_used_at != null)).toBe(true);
    } finally {
      await Promise.all([global.cleanup(), cn.cleanup()]);
    }
  });
});
