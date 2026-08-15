import { access } from 'node:fs/promises';
import { describe, expect, test } from 'vitest';
import { GizPayConnector } from '../connectors/gizpay.js';
import { GizPaySchema } from '../schemas/gizpay.js';
import { loadEnvironment, temporaryDatabase, waitForFirstSync, waitForRow } from './helpers.js';

describe('PowerSync offline lifecycle', () => {
  test('keeps synced data offline and receives a later server mutation after reconnect', async (context) => {
    const env = loadEnvironment();
    if (env == null) return context.skip();
    const local = await temporaryDatabase(GizPaySchema, 'offline-reconnect');
    try {
      const connector = new GizPayConnector({ endpoint: env.gizpayEndpoint, token: env.token, apiBaseURL: env.payURL });
      await local.database.connect(connector);
      await waitForFirstSync(local.database);
      const [merchant] = await local.database.getAll<{ id: string }>('SELECT id FROM my_merchants LIMIT 1');
      expect(merchant).toBeDefined();

      await local.database.disconnect();
      expect(await local.database.getAll('SELECT id FROM my_merchants')).not.toHaveLength(0);

      const publicName = `reconnected-${Date.now()}`;
      const response = await fetch(`${env.payURL}/account/v1/merchants/${merchant.id}`, {
        method: 'PATCH',
        headers: { Authorization: `Bearer ${env.humanToken}`, 'Content-Type': 'application/json' },
        body: JSON.stringify({ public_name: publicName })
      });
      expect(response.status).toBe(200);

      await local.database.connect(connector);
      await waitForRow(
        local.database,
        'SELECT public_name FROM my_merchants WHERE id = ?',
        [merchant.id],
        (row) => row.public_name === publicName
      );
    } finally {
      await local.cleanup();
    }
  });

  test('removes the database directory when an identity logs out', async () => {
    const local = await temporaryDatabase(GizPaySchema, 'logout');
    const directory = local.directory;
    await local.database.execute("INSERT INTO my_subscription_keys(id,key,status) VALUES('key-a','secret-a','active')");
    await local.cleanup();
    await expect(access(directory)).rejects.toThrow();
  });
});
