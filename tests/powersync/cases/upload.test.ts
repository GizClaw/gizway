import { afterEach, describe, expect, test, vi } from 'vitest';
import type { CommonPowerSyncDatabase } from '@powersync/node';
import { GizPayConnector } from '../connectors/gizpay.js';
import { GizWayConnector } from '../connectors/gizway.js';
import { GizPaySchema } from '../schemas/gizpay.js';
import { loadEnvironment, temporaryDatabase, waitForFirstSync, waitForRow } from './helpers.js';

afterEach(() => vi.unstubAllGlobals());

describe('uploadData API boundary', () => {
  test('maps an allowed Merchant mutation to the human API and completes it', async () => {
    const complete = vi.fn(async () => undefined);
    const database = {
      getCrudBatch: vi.fn(async () => ({
        crud: [{ table: 'my_merchants', id: 'merchant-1', op: 'PATCH', opData: { public_name: 'Updated' } }],
        complete
      }))
    } as unknown as CommonPowerSyncDatabase;
    const fetchMock = vi.fn(async () => new Response('{}', { status: 200 }));
    vi.stubGlobal('fetch', fetchMock);
    const connector = new GizPayConnector({ endpoint: 'https://sync.example', token: 'human-token', apiBaseURL: 'https://pay.example' });
    await connector.uploadData(database);
    expect(fetchMock).toHaveBeenCalledWith('https://pay.example/account/v1/merchants/merchant-1', expect.objectContaining({ method: 'PATCH' }));
    expect(complete).toHaveBeenCalledOnce();
  });

  test('retries temporary server errors without completing the queue', async () => {
    const complete = vi.fn(async () => undefined);
    const database = {
      getCrudBatch: vi.fn(async () => ({
        crud: [{ table: 'my_merchants', id: 'merchant-1', op: 'PATCH', opData: { public_name: 'Updated' } }],
        complete
      }))
    } as unknown as CommonPowerSyncDatabase;
    vi.stubGlobal('fetch', vi.fn(async () => new Response('{}', { status: 503 })));
    const connector = new GizPayConnector({ endpoint: 'https://sync.example', token: 'human-token', apiBaseURL: 'https://pay.example' });
    await expect(connector.uploadData(database)).rejects.toThrow('temporary upload failure');
    expect(complete).not.toHaveBeenCalled();
  });

  test('isolates a deterministic business rejection instead of stalling forever', async () => {
    const complete = vi.fn(async () => undefined);
    const database = {
      getCrudBatch: vi.fn(async () => ({
        crud: [{ table: 'my_merchants', id: 'merchant-1', op: 'PATCH', opData: { public_name: '' } }],
        complete
      }))
    } as unknown as CommonPowerSyncDatabase;
    vi.stubGlobal('fetch', vi.fn(async () => new Response('{}', { status: 400 })));
    const connector = new GizPayConnector({ endpoint: 'https://sync.example', token: 'human-token', apiBaseURL: 'https://pay.example' });
    await connector.uploadData(database);
    expect(complete).toHaveBeenCalledOnce();
  });

  test.each([
    ['subscription create', new GizPayConnector({ endpoint: 'https://sync.example', token: 'human-token', apiBaseURL: 'https://pay.example' }),
      { table: 'my_subscriptions', id: 'local-sub', op: 'PUT', opData: { product_id: 'product-1', account_id: 'account-1', terms_version: 'v1' } },
      'https://pay.example/account/v1/products/product-1/subscriptions', 'POST'],
    ['subscription update', new GizPayConnector({ endpoint: 'https://sync.example', token: 'human-token', apiBaseURL: 'https://pay.example' }),
      { table: 'my_subscriptions', id: 'subscription-1', op: 'PATCH', opData: { status: 'paused' } },
      'https://pay.example/account/v1/subscriptions/subscription-1', 'PATCH'],
    ['Subscription Key create', new GizPayConnector({ endpoint: 'https://sync.example', token: 'human-token', apiBaseURL: 'https://pay.example' }),
      { table: 'my_subscription_keys', id: 'local-key', op: 'PUT', opData: { subscription_id: 'subscription-1' } },
      'https://pay.example/account/v1/subscriptions/subscription-1/keys', 'POST'],
    ['Subscription Key revoke', new GizPayConnector({ endpoint: 'https://sync.example', token: 'human-token', apiBaseURL: 'https://pay.example' }),
      { table: 'my_subscription_keys', id: 'key-1', op: 'PATCH', opData: { status: 'revoked' } },
      'https://pay.example/account/v1/subscriptions/subscription-1/keys/key-1/revoke', 'POST'],
    ['Top-up create', new GizPayConnector({ endpoint: 'https://sync.example', token: 'human-token', apiBaseURL: 'https://pay.example' }),
      { table: 'my_topups', id: 'local-topup', op: 'PUT', opData: { account_id: 'account-1', channel: 'fake', external_reference: 'ref-1', amount_microcredits: 100 } },
      'https://pay.example/account/v1/accounts/account-1/topups', 'POST'],
    ['Provider Key create', new GizWayConnector({ endpoint: 'https://sync.example', token: 'human-token', apiBaseURL: 'https://way.example' }),
      { table: 'my_provider_keys', id: 'local-provider-key', op: 'PUT', opData: { provider_id: 'provider-1', key: 'provider-secret', status: 'active', prices_json: '[]' } },
      'https://way.example/user/v1/providers/provider-1/keys', 'POST'],
    ['Provider Key prices', new GizWayConnector({ endpoint: 'https://sync.example', token: 'human-token', apiBaseURL: 'https://way.example' }),
      { table: 'my_provider_keys', id: 'provider-key-1', op: 'PATCH', opData: { prices_json: '[]' } },
      'https://way.example/user/v1/provider-keys/provider-key-1/prices', 'PUT'],
    ['Provider Key disable', new GizWayConnector({ endpoint: 'https://sync.example', token: 'human-token', apiBaseURL: 'https://way.example' }),
      { table: 'my_provider_keys', id: 'provider-key-1', op: 'PATCH', opData: { status: 'disabled' } },
      'https://way.example/user/v1/provider-keys/provider-key-1/disable', 'POST']
  ])('maps %s through its business API', async (_name, connector, entry, url, method) => {
    const complete = vi.fn(async () => undefined);
    const database = {
      getCrudBatch: vi.fn(async () => ({ crud: [entry], complete })),
      getAll: vi.fn(async () => [{ subscription_id: 'subscription-1' }])
    } as unknown as CommonPowerSyncDatabase;
    const fetchMock = vi.fn(async () => new Response('{}', { status: 200 }));
    vi.stubGlobal('fetch', fetchMock);
    await connector.uploadData(database);
    expect(fetchMock).toHaveBeenCalledWith(url, expect.objectContaining({ method }));
    expect(complete).toHaveBeenCalledOnce();
  });

  test('uploads a local Merchant mutation and observes the server result in a fresh database', async (context) => {
    const env = loadEnvironment();
    if (env == null) return context.skip();
    const first = await temporaryDatabase(GizPaySchema, 'upload-source');
    const second = await temporaryDatabase(GizPaySchema, 'upload-resync');
    const connector = new GizPayConnector({ endpoint: env.gizpayEndpoint, token: env.token, apiBaseURL: env.payURL });
    try {
      await first.database.connect(connector);
      await waitForFirstSync(first.database);
      const [account] = await first.database.getAll<{ id: string }>('SELECT id FROM my_accounts LIMIT 1');
      const created = await fetch(`${env.payURL}/account/v1/merchants`, {
        method: 'POST',
        headers: { Authorization: `Bearer ${env.humanToken}`, 'Content-Type': 'application/json' },
        body: JSON.stringify({ settlement_account_id: account.id, legal_name: 'PowerSync Upload', public_name: 'Before Upload' })
      });
      expect(created.status).toBe(201);
      const merchant = await created.json() as { id: string };
      await waitForRow(first.database, 'SELECT public_name FROM my_merchants WHERE id = ?', [merchant.id], () => true);

      const expected = `uploaded-${Date.now()}`;
      await first.database.execute('UPDATE my_merchants SET public_name = ? WHERE id = ?', [expected, merchant.id]);
      await waitForMerchantAPI(env.payURL, env.humanToken, merchant.id, expected);

      await second.database.connect(connector);
      await waitForFirstSync(second.database);
      await waitForRow(
        second.database,
        'SELECT public_name FROM my_merchants WHERE id = ?',
        [merchant.id],
        (row) => row.public_name === expected
      );
    } finally {
      await Promise.all([first.cleanup(), second.cleanup()]);
    }
  });
});

async function waitForMerchantAPI(baseURL: string, token: string, id: string, expected: string) {
  const signal = AbortSignal.timeout(30_000);
  while (!signal.aborted) {
    const response = await fetch(`${baseURL}/account/v1/merchants/${encodeURIComponent(id)}`, {
      headers: { Authorization: `Bearer ${token}` }
    });
    if (response.ok && (await response.json() as { public_name: string }).public_name === expected) return;
    await new Promise((resolve) => setTimeout(resolve, 50));
  }
  throw new Error('Merchant upload did not reach the business API');
}
