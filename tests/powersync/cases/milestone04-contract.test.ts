import { describe, expect, test, vi } from 'vitest';
import type { CommonPowerSyncDatabase } from '@powersync/node';
import { readFile } from 'node:fs/promises';
import { GizPayConnector } from '../connectors/gizpay.js';
import { GizWayConnector } from '../connectors/gizway.js';
import { GizPaySchema } from '../schemas/gizpay.js';
import { GizWaySchema } from '../schemas/gizway.js';

describe('Milestone 04 PowerSync contract', () => {
  test('publishes Catalog and the web display fields', () => {
    expect(GizPaySchema.tables.map((table) => table.name)).toContain('product_listings');
    expect(GizWaySchema.tables.map((table) => table.name)).toContain('model_listings');
    const gizpay = JSON.stringify(GizPaySchema.toJSON());
    for (const field of ['email', 'display_name', 'merchant_id', 'name', 'last_used_at']) {
      expect(gizpay).toContain(field);
    }
    const profile = GizPaySchema.tables.find((table) => table.name === 'my_profile');
    expect(JSON.stringify(profile?.toJSON())).toContain('merchant_id');
    const gizway = JSON.stringify(GizWaySchema.toJSON());
    for (const field of ['subscription_key_id', 'name', 'last_used_at', 'earned_microcredits', 'family', 'featured']) {
      expect(gizway).toContain(field);
    }
  });

  test('publishes the unique default Merchant ID in my_profile', async () => {
    const gizpay = await readFile(new URL('../../../docker/gizway-powersync/config/pay/sync-config.yaml', import.meta.url), 'utf8');
    const profileQuery = gizpay.split('SELECT my_profile.id')[1]?.split('- |')[0] ?? '';
    expect(profileQuery).toContain('my_profile.merchant_id');
    expect(profileQuery).toContain('FROM client_sync.user_profiles AS "my_profile"');
  });

  test('uses the generated source ID for every customer price row', async () => {
    const migration = await readFile(new URL('../../../data/sql/gizway/migrations/000001_schema.sql', import.meta.url), 'utf8');
    expect(migration).toContain("id TEXT GENERATED ALWAYS AS (model_id || ':' || metric) STORED PRIMARY KEY");
    expect(migration).toContain('UNIQUE (model_id, metric)');
  });

  test('authorizes public Catalog by common and regional roles only', async () => {
    const gizpay = await readFile(new URL('../../../docker/gizway-powersync/config/pay/sync-config.yaml', import.meta.url), 'utf8');
    expect(gizpay).toContain('product_listings');
    expect(gizpay).toContain('public_catalog_global');
    expect(gizpay).toContain('public_catalog_cn');
    const publicCatalog = gizpay.split('current_user:')[0] ?? '';
    expect(publicCatalog).toContain("->> 'public_catalog' IS NULL");
    const cn = await readFile(new URL('../../../docker/gizway-powersync/config/cn/sync-config.yaml', import.meta.url), 'utf8');
    const global = await readFile(new URL('../../../docker/gizway-powersync/config/global/sync-config.yaml', import.meta.url), 'utf8');
    const roleClaim = 'urn:zitadel:iam:org:project:386000000000000001:roles';
	for (const config of [gizpay, cn, global]) {
		expect(config).toContain(roleClaim);
		expect(config).not.toContain("auth.parameter('urn:zitadel:iam:org:project:roles')");
	}
    expect(cn).toContain('model_listings');
    expect(cn).toContain("->> 'public_catalog_cn' IS NOT NULL");
    expect(cn).not.toContain("->> 'public_catalog_global' IS NOT NULL");
    expect(global).toContain("->> 'public_catalog_global' IS NOT NULL");
    expect(global).not.toContain("->> 'public_catalog_cn' IS NOT NULL");
    for (const gizway of [cn, global]) {
      const privateStream = gizway.split('current_user:')[1] ?? '';
      expect(privateStream).toContain('my_provider_keys');
      expect(privateStream).toContain("->> 'public_catalog' IS NULL");
    }
  });

  test.each([
    ['subscription', new GizPayConnector(baseConfig()), {
      table: 'my_subscriptions', id: 'sub-client', op: 'PUT',
      opData: { product_id: 'product-1', account_id: 'account-1', terms_version: 'v1' }
    }, { id: 'sub-client', account_id: 'account-1', terms_version: 'v1' }],
    ['Subscription Key', new GizPayConnector(baseConfig()), {
      table: 'my_subscription_keys', id: 'skey-client', op: 'PUT',
      opData: { subscription_id: 'subscription-1', name: 'CLI' }
    }, { id: 'skey-client', name: 'CLI' }],
    ['Top-up', new GizPayConnector(baseConfig()), {
      table: 'my_topups', id: 'topup-client', op: 'PUT',
      opData: { account_id: 'account-1', channel: 'fake', external_reference: 'ref-1', amount_microcredits: 100 }
    }, { id: 'topup-client', channel: 'fake', external_reference: 'ref-1', amount_microcredits: 100 }],
    ['Provider Key', new GizWayConnector({ ...baseConfig(), apiBaseURL: 'https://way.example' }), {
      table: 'my_provider_keys', id: 'pkey-client', op: 'PUT',
      opData: { provider_id: 'provider-1', name: 'BYOK', key: 'secret', status: 'active', prices_json: '[]' }
    }, { id: 'pkey-client', name: 'BYOK', key: 'secret', status: 'active', prices: [] }]
  ])('uploads the client-generated ID for %s', async (_name, connector, entry, expectedBody) => {
    const complete = vi.fn(async () => undefined);
    const database = databaseFor(entry, complete);
    const fetchMock = vi.fn(async (_url: string, init?: RequestInit) => {
      expect(JSON.parse(String(init?.body))).toEqual(expectedBody);
      return new Response('{}', { status: 200 });
    });
    vi.stubGlobal('fetch', fetchMock);
    await connector.uploadData(database);
    expect(complete).toHaveBeenCalledOnce();
    vi.unstubAllGlobals();
  });

  test('reports a typed deterministic error and completes only that entry', async () => {
    const complete = vi.fn(async () => undefined);
    const onMutationError = vi.fn();
    const connector = new GizPayConnector({ ...baseConfig(), onMutationError } as never);
    vi.stubGlobal('fetch', vi.fn(async () => new Response(
      JSON.stringify({ error: { code: 'resource_id_conflict', message: 'ID is already used' } }),
      { status: 409, headers: { 'Content-Type': 'application/json' } }
    )));
    await connector.uploadData(databaseFor({
      table: 'my_subscriptions', id: 'sub-conflict', op: 'PUT',
      opData: { product_id: 'product-1', account_id: 'account-1', terms_version: 'v1' }
    }, complete));
    expect(onMutationError).toHaveBeenCalledWith(expect.objectContaining({
      table: 'my_subscriptions', id: 'sub-conflict', code: 'resource_id_conflict', message: 'ID is already used'
    }));
    expect(complete).toHaveBeenCalledOnce();
    vi.unstubAllGlobals();
  });

  test.each([
    ['429', new Response(JSON.stringify({ error: { code: 'rate_limited', message: 'retry' } }), { status: 429 })],
    ['5xx', new Response(JSON.stringify({ error: { code: 'unavailable', message: 'retry' } }), { status: 503 })],
    ['invalid error JSON', new Response('not-json', { status: 400 })]
  ])('keeps the CRUD entry for %s', async (_name, response) => {
    const complete = vi.fn(async () => undefined);
    const connector = new GizPayConnector(baseConfig());
    vi.stubGlobal('fetch', vi.fn(async () => response));
    await expect(connector.uploadData(databaseFor({
      table: 'my_merchants', id: 'merchant-1', op: 'PATCH', opData: { public_name: 'Updated' }
    }, complete))).rejects.toThrow();
    expect(complete).not.toHaveBeenCalled();
    vi.unstubAllGlobals();
  });

	test('honors Retry-After before retaining a 429 entry', async () => {
		const complete = vi.fn(async () => undefined);
		const sleep = vi.fn(async () => undefined);
		const connector = new GizPayConnector({ ...baseConfig(), sleep, now: () => Date.parse('2026-08-16T00:00:00Z') });
		vi.stubGlobal('fetch', vi.fn(async () => new Response('', { status: 429, headers: { 'Retry-After': '4' } })));
		await expect(connector.uploadData(databaseFor({
			table: 'my_merchants', id: 'merchant-1', op: 'PATCH', opData: { public_name: 'Updated' }
		}, complete))).rejects.toThrow('429');
		expect(sleep).toHaveBeenCalledWith(4000);
		expect(complete).not.toHaveBeenCalled();
		vi.unstubAllGlobals();
	});
});

function baseConfig() {
  return { endpoint: 'https://sync.example', token: 'human-token', apiBaseURL: 'https://pay.example' };
}

function databaseFor(entry: unknown, complete: () => Promise<void>) {
  return {
    getCrudBatch: vi.fn(async () => ({ crud: [entry], complete })),
    getAll: vi.fn(async () => [{ subscription_id: 'subscription-1' }])
  } as unknown as CommonPowerSyncDatabase;
}
