import type { CommonPowerSyncDatabase } from '@powersync/node';
import { APIConnector } from './base.js';

export class GizPayConnector extends APIConnector {
  protected async mapEntry(table: string, id: string, operation: string, data: Record<string, unknown>, database: CommonPowerSyncDatabase) {
    if (table === 'my_merchants' && operation === 'PATCH') {
      return {
        method: 'PATCH', path: `/account/v1/merchants/${encodeURIComponent(id)}`,
        body: select(data, ['public_name', 'status'])
      };
    }
    if (table === 'my_subscriptions' && operation === 'PUT') {
      return {
        method: 'POST', path: `/account/v1/products/${segment(data.product_id)}/subscriptions`,
        body: select(data, ['account_id', 'terms_version'])
      };
    }
    if (table === 'my_subscriptions' && operation === 'PATCH') {
      return {
        method: 'PATCH', path: `/account/v1/subscriptions/${encodeURIComponent(id)}`,
        body: select(data, ['status'])
      };
    }
    if (table === 'my_subscription_keys' && operation === 'PUT') {
      return {
        method: 'POST', path: `/account/v1/subscriptions/${segment(data.subscription_id)}/keys`, body: {}
      };
    }
    if (table === 'my_subscription_keys' && operation === 'PATCH' && data.status === 'revoked') {
      let subscriptionID = data.subscription_id;
      if (subscriptionID === undefined) {
        const rows = await database.getAll<{ subscription_id: string }>(
          'SELECT subscription_id FROM my_subscription_keys WHERE id = ?', [id]
        );
        subscriptionID = rows[0]?.subscription_id;
      }
      return {
        method: 'POST',
        path: `/account/v1/subscriptions/${segment(subscriptionID)}/keys/${encodeURIComponent(id)}/revoke`
      };
    }
    if (table === 'my_topups' && operation === 'PUT') {
      return {
        method: 'POST', path: `/account/v1/accounts/${segment(data.account_id)}/topups`,
        body: select(data, ['channel', 'external_reference', 'amount_microcredits'])
      };
    }
    throw new Error(`unsupported GizPay local mutation ${operation} ${table}`);
  }
}

function segment(value: unknown): string {
  if (typeof value !== 'string' || value === '') throw new Error('local mutation is missing a required identifier');
  return encodeURIComponent(value);
}

function select(data: Record<string, unknown>, fields: string[]): Record<string, unknown> {
  return Object.fromEntries(fields.filter((field) => data[field] !== undefined).map((field) => [field, data[field]]));
}
