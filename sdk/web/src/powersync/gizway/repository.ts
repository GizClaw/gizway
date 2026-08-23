import type { PowerSyncDatabase } from "@powersync/web";
import type { AIOrder, GizWayRepository, GizWaySnapshot, Model, ProviderKey, ProviderKeyDraft, ProviderPrice, Region, UsageMetric } from "../../contracts/gizway";
import type { MutationCoordinator } from "../lifecycle";

export class PowerSyncGizWayRepository implements GizWayRepository {
  constructor(private readonly database: PowerSyncDatabase, private readonly region: Region, private readonly mutations?: MutationCoordinator) {}
  async getSnapshot(): Promise<GizWaySnapshot> {
    const [models, prices, usage, orders, keys] = await Promise.all([
      this.database.getAll<{ id: string; provider_id: string; name: string; provider_name: string; family: string; description: string; context: string; latency: string; availability: string }>("SELECT m.id,m.provider_id,m.name,p.name provider_name,l.family,l.description,l.context,l.latency,l.availability FROM models m JOIN providers p ON p.id=m.provider_id JOIN model_listings l ON l.model_id=m.id ORDER BY l.display_order,m.id"),
      this.database.getAll<{ model_id: string; metric: string; unit_size: number; price_microcredits: number }>("SELECT model_id,metric,unit_size,price_microcredits FROM model_customer_prices ORDER BY model_id,metric"),
      this.database.getAll<{ order_id: string; metric: UsageMetric["metric"]; quantity: number }>("SELECT order_id,metric,quantity FROM my_ai_usage WHERE order_id IS NOT NULL AND metric IN ('input_tokens','output_tokens') ORDER BY order_id,metric"),
      this.database.getAll<{ id: string; model_id: string; model_name: string; subscription_key_id: string; gross_microcredits: number; status: string; created_at: string; completed_at: string | null }>("SELECT o.id,o.model_id,m.name model_name,o.subscription_key_id,o.gross_microcredits,o.status,o.created_at,o.completed_at FROM my_ai_orders o JOIN models m ON m.id=o.model_id ORDER BY o.created_at,o.id"),
      this.database.getAll<{ id: string; provider_id: string; provider_name: string; name: string; key: string; status: ProviderKey["status"]; earned_microcredits: number; last_used_at: string | null; prices_json: string }>("SELECT k.id,k.provider_id,p.name provider_name,k.name,k.key,k.status,k.earned_microcredits,k.last_used_at,k.prices_json FROM my_provider_keys k JOIN providers p ON p.id=k.provider_id ORDER BY k.created_at,k.id"),
    ]);
    const modelPrices = new Map<string, Model["rates"]>();
    for (const row of prices) (modelPrices.get(row.model_id) ?? (modelPrices.set(row.model_id, []), modelPrices.get(row.model_id)!)).push({ metric: row.metric, unitSize: Number(row.unit_size), microcreditsPerUnit: Number(row.price_microcredits) });
    const orderUsage = new Map<string, UsageMetric[]>();
    for (const row of usage) (orderUsage.get(row.order_id) ?? (orderUsage.set(row.order_id, []), orderUsage.get(row.order_id)!)).push({ metric: row.metric, quantity: Number(row.quantity) });
    return {
      region: this.region,
      models: models.map<Model>((row) => ({ id: row.id, providerId: row.provider_id, name: row.name, providerName: row.provider_name, family: row.family, description: row.description, context: row.context, latency: row.latency, availability: row.availability, rates: modelPrices.get(row.id) ?? [] })),
      orders: orders.map<AIOrder>((row) => ({ id: row.id, modelId: row.model_id, modelName: row.model_name, subscriptionKeyId: row.subscription_key_id, grossMicrocredits: Number(row.gross_microcredits), metrics: orderUsage.get(row.id) ?? [], status: row.status, createdAt: row.created_at, completedAt: row.completed_at ?? undefined })),
      providerKeys: keys.map<ProviderKey>((row) => ({ id: row.id, providerId: row.provider_id, providerName: row.provider_name, name: row.name, maskedValue: row.key, status: row.status, earnedMicrocredits: Number(row.earned_microcredits), lastUsedAt: row.last_used_at ?? undefined, prices: parsePrices(row.prices_json) })),
    };
  }
  async createProviderKey(draft: ProviderKeyDraft): Promise<GizWaySnapshot> {
    const id = crypto.randomUUID();
    await this.mutate(id, () => this.database.execute("INSERT INTO my_provider_keys(id,provider_id,key,merchant_id,name,status,prices_json,created_at,updated_at) VALUES(?,?,?,'',?,'active',?,?,?)", [id, draft.providerId, draft.key, draft.name, JSON.stringify(toStoredPrices(draft.prices)), new Date().toISOString(), new Date().toISOString()]));
    return this.getSnapshot();
  }
  async updateProviderKeyPrices(id: string, prices: ProviderPrice[]): Promise<GizWaySnapshot> {
    await this.mutate(id, () => this.database.execute("UPDATE my_provider_keys SET prices_json=?,updated_at=? WHERE id=?", [JSON.stringify(toStoredPrices(prices)), new Date().toISOString(), id]));
    return this.getSnapshot();
  }
  async disableProviderKey(id: string): Promise<GizWaySnapshot> {
    await this.mutate(id, () => this.database.execute("UPDATE my_provider_keys SET status='disabled',updated_at=? WHERE id=?", [new Date().toISOString(), id]));
    return this.getSnapshot();
  }
  private mutate(id: string, write: () => Promise<unknown>): Promise<void> {
    if (!this.mutations) throw new Error("public Catalog connection is read-only");
    return this.mutations.run("my_provider_keys", id, write);
  }
}

function parsePrices(raw: string): ProviderPrice[] {
  try {
    const value = JSON.parse(raw) as unknown;
    if (!Array.isArray(value)) return [];
    return value.flatMap((item): ProviderPrice[] => {
      if (item == null || typeof item !== "object") return [];
      const row = item as Record<string, unknown>;
      const modelId = row.model_id ?? row.modelId;
      const unitSize = row.unit_size ?? row.unitSize;
      const microcredits = row.microcredits_per_unit ?? row.microcreditsPerUnit;
      if (typeof modelId !== "string" || (row.metric !== "input_tokens" && row.metric !== "output_tokens") || typeof unitSize !== "number" || typeof microcredits !== "number") return [];
      return [{ modelId, metric: row.metric, unitSize, microcreditsPerUnit: microcredits }];
    });
  } catch { return []; }
}

function toStoredPrices(prices: ProviderPrice[]) {
  return prices.map((price) => ({ model_id: price.modelId, metric: price.metric, unit_size: price.unitSize, microcredits_per_unit: price.microcreditsPerUnit }));
}
