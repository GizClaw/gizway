import { describe, expect, test } from 'vitest';
import { readFile } from 'node:fs/promises';
import { GizPaySchema } from '../schemas/gizpay.js';
import { GizWaySchema } from '../schemas/gizway.js';

describe('Milestone 03 client schema', () => {
  test('contains every approved GizPay resource and no internal secret fields', () => {
    expect(GizPaySchema.tables.map((table) => table.name).sort()).toEqual([
      'available_products', 'my_accounts', 'my_balances', 'my_charges', 'my_commissions',
      'my_merchants', 'my_products', 'my_profile', 'my_service_accounts',
      'my_subscription_keys', 'my_subscriptions', 'my_topups', 'my_transactions', 'product_listings'
    ].sort());
    const serialized = JSON.stringify(GizPaySchema.toJSON());
    expect(serialized).toContain('is_default');
    expect(serialized).toContain('order_snapshot');
    for (const forbidden of ['subscription_key_hmac', 'credential', 'ledger_entries', 'outbox']) {
      expect(serialized).not.toContain(forbidden);
    }
  });

  test('contains regional resources and only the owner-visible Provider Key plaintext', () => {
    expect(GizWaySchema.tables.map((table) => table.name).sort()).toEqual([
      'model_customer_prices', 'model_listings', 'models', 'my_ai_orders', 'my_ai_usage', 'my_provider_keys', 'providers'
    ].sort());
    const serialized = JSON.stringify(GizWaySchema.toJSON());
    for (const field of ['external_order_id', 'provider_id', 'completed_at']) {
      expect(serialized).toContain(field);
    }
    expect(serialized).not.toContain('subscription_key_hmac');
    expect(serialized).not.toContain('charge_outbox');
  });

  test('Sync Streams authorize private rows from signed issuer and subject claims', async () => {
    for (const name of ['gizpay-sync-config', 'gizway-cn-sync-config', 'gizway-global-sync-config']) {
      const text = await readFile(new URL(`../config/${name}.yaml`, import.meta.url), 'utf8');
      expect(text).toContain('edition: 3');
      expect(text).toContain("auth.user_id()");
      expect(text).toContain("auth.parameter('iss')");
      expect(text).not.toContain("subscription.parameter('owner");
      expect(text).not.toContain('ledger_entries');
      expect(text).not.toContain('charge_outbox');
      expect(text).not.toContain('subscription_key_hmac');
    }
    const gizway = await readFile(new URL('../config/gizway-global-sync-config.yaml', import.meta.url), 'utf8');
    expect(gizway).toContain('FROM gizway.ai_orders AS "my_ai_orders"');
    expect(gizway).toContain('FROM gizway.model_customer_prices');
  });
});
