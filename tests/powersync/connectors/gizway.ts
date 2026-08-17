import type { CommonPowerSyncDatabase } from '@powersync/node';
import { APIConnector } from './base.js';

export class GizWayConnector extends APIConnector {
  protected mapEntry(table: string, id: string, operation: string, data: Record<string, unknown>, _database: CommonPowerSyncDatabase) {
    if (table === 'my_provider_keys' && operation === 'PUT') {
      return {
        method: 'POST', path: `/user/v1/providers/${segment(data.provider_id)}/keys`,
        body: { id, name: data.name, key: data.key, status: data.status, prices: prices(data.prices_json) }
      };
    }
    if (table === 'my_provider_keys' && operation === 'PATCH' && data.status === 'disabled') {
      return { method: 'POST', path: `/user/v1/provider-keys/${encodeURIComponent(id)}/disable` };
    }
    if (table === 'my_provider_keys' && operation === 'PATCH' && data.prices_json !== undefined) {
      return {
        method: 'PUT', path: `/user/v1/provider-keys/${encodeURIComponent(id)}/prices`,
        body: { prices: prices(data.prices_json) }
      };
    }
    throw new Error(`unsupported GizWay local mutation ${operation} ${table}`);
  }
}

function segment(value: unknown): string {
  if (typeof value !== 'string' || value === '') throw new Error('local mutation is missing a required identifier');
  return encodeURIComponent(value);
}

function prices(value: unknown): unknown[] {
  if (Array.isArray(value)) return value;
  if (typeof value !== 'string') throw new Error('local Provider Key mutation is missing prices_json');
  const parsed: unknown = JSON.parse(value);
  if (!Array.isArray(parsed)) throw new Error('prices_json must contain an array');
  return parsed;
}
