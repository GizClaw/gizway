import { describe, expect, test } from 'vitest';
import { GizPaySchema } from '../schemas/gizpay.js';
import { GizWaySchema } from '../schemas/gizway.js';
import { temporaryDatabase } from './helpers.js';

describe('two independent local databases', () => {
  test('never exposes a cross-database SQL join or shared file', async () => {
    const pay = await temporaryDatabase(GizPaySchema, 'gizpay-test');
    const way = await temporaryDatabase(GizWaySchema, 'gizway-global-test');
    try {
      expect(pay.directory).not.toBe(way.directory);
      await pay.database.execute("INSERT INTO my_accounts(id,status) VALUES('account-1','active')");
      expect(await pay.database.getAll('SELECT id FROM my_accounts')).toHaveLength(1);
      await expect(way.database.getAll('SELECT id FROM my_accounts')).rejects.toThrow();
    } finally {
      await pay.cleanup();
      await way.cleanup();
    }
  });
});
