import type { PowerSyncDatabase } from "@powersync/web";
import type { AIOrder, GizWayRepository, Model, ProviderKey, ProviderKeyDraft, ProviderPrice, Region, UsagePoint } from "@/data/contracts/gizway";
import type { FakeScenario } from "@/data/fake/scenarios";
import { gizWayFixture } from "@/data/fake/gizway/fixtures";

type ModelRow = { id: string; name: string; provider_id: string; provider: string; family: Model["family"]; description: string; latency: string; context: string; accent: string };
type PriceRow = { model_id: string; metric: string; unit_size: number; price_microcredits: number };
type UsageRow = { order_id: string; metric: "input_tokens" | "output_tokens"; quantity: number };
type OrderRow = { id: string; model: string; subscription_key_id: string; gross_microcredits: number; status: string; created_at: string };
type ProviderKeyRow = { id: string; provider: string; name: string; key: string; status: ProviderKey["status"]; earned_microcredits: number; last_used_at: string | null; prices_json: string };
type MutationRunner = { run(table: string, id: string, write: () => Promise<unknown>): Promise<unknown> };

export class PowerSyncGizWayRepository implements GizWayRepository {
  constructor(private readonly database: PowerSyncDatabase, private readonly mutations?: MutationRunner) {}

  async seed(region: Region, scenario: FakeScenario) {
    const fixture = gizWayFixture(region, scenario);
    await this.database.disconnectAndClear();
    await this.database.writeTransaction(async (tx) => {
      const now = new Date().toISOString();
      const providers = [...new Set(fixture.models.map((model) => model.provider))];
      if (providers.length) await tx.executeBatch("INSERT INTO providers (id,name,kind,status) VALUES (?,?, 'openai','active')", providers.map((provider) => [`provider_${provider.toLowerCase().replaceAll(" ", "_")}`, provider]));
      if (fixture.models.length) {
        await tx.executeBatch("INSERT INTO models (id,provider_id,name,provider_model,status) VALUES (?,?,?,?, 'active')", fixture.models.map((model) => [model.id, `provider_${model.provider.toLowerCase().replaceAll(" ", "_")}`, model.name, model.name]));
        await tx.executeBatch("INSERT INTO model_listings (id,model_id,title,description,family,context,latency,accent,featured,display_order,availability,created_at,updated_at) VALUES (?,?,?,?,?,?,?,?,?,?,?,?,?)", fixture.models.map((model, index) => [`listing_${model.id}`, model.id, model.name, model.description, model.family, model.context, model.latency, model.accent, index < 3 ? 1 : 0, index, "available", now, now]));
        const prices = fixture.models.flatMap((model) => model.rates.map((rate) => [`${model.id}:${rate.metric}`, model.id, rate.metric.toLowerCase() === "input" ? "input_tokens" : rate.metric.toLowerCase() === "output" ? "output_tokens" : rate.metric, rate.unit === "1M" || rate.unit === "1M tokens" ? 1_000_000 : 1, rate.credits]));
        if (prices.length) await tx.executeBatch("INSERT INTO model_customer_prices (id,model_id,metric,unit_size,price_microcredits) VALUES (?,?,?,?,?)", prices);
      }
      if (fixture.orders.length) await tx.executeBatch("INSERT INTO my_ai_orders (id,external_order_id,account_id,subscription_id,subscription_key_id,product_id,model_id,provider_id,gross_microcredits,status,created_at,completed_at) VALUES (?,?, 'personal','subscription',?,'product',?,?,?,?,?,?)", fixture.orders.map((order) => [order.id, `external_${order.id}`, order.keyName, fixture.models.find((model) => model.name === order.model)?.id ?? fixture.models[0]?.id, `provider_${fixture.models[0]?.provider.toLowerCase().replaceAll(" ", "_")}`, order.credits, order.status === "completed" ? "charged" : order.status === "failed" ? "billing_failed" : "pending", order.createdAt, order.status === "pending" ? null : order.createdAt]));
      const usageRows = fixture.orders.flatMap((order) => order.metrics.map((metric) => [`usage_${order.id}_${metric.metric}`, "personal", order.id, fixture.models.find((model) => model.name === order.model)?.id ?? fixture.models[0]?.id, metric.metric, metric.quantity, "completed", order.createdAt]));
      if (usageRows.length) await tx.executeBatch("INSERT INTO my_ai_usage (id,account_id,order_id,model_id,metric,quantity,status,created_at) VALUES (?,?,?,?,?,?,?,?)", usageRows);
      if (fixture.providerKeys.length) await tx.executeBatch("INSERT INTO my_provider_keys (id,provider_id,key,merchant_id,name,last_used_at,earned_microcredits,status,prices_json,created_at,updated_at) VALUES (?,?,?,'merchant',?,?,?,?,?, ?,?)", fixture.providerKeys.map((key) => [key.id, `provider_${key.provider.toLowerCase().replaceAll(" ", "_")}`, key.maskedValue, key.name, key.lastUsedAt ?? null, key.earnedCredits, key.status, JSON.stringify(Array.from({ length: key.modelCount }, (_, index) => ({ model_id: `model_${index}`, metric: "input_tokens" }))), now, now]));
    });
  }

  async getSnapshot(region: Region) {
    const [models, prices, usageRows, orders, providerKeys] = await Promise.all([
      this.database.getAll<ModelRow>("SELECT m.id,m.name,m.provider_id,p.name provider,l.family,l.description,l.latency,l.context,l.accent FROM models m JOIN providers p ON p.id=m.provider_id JOIN model_listings l ON l.model_id=m.id ORDER BY l.display_order,m.id"),
      this.database.getAll<PriceRow>("SELECT model_id,metric,unit_size,price_microcredits FROM model_customer_prices ORDER BY model_id,metric"),
      this.database.getAll<UsageRow>("SELECT order_id,metric,quantity FROM my_ai_usage WHERE order_id IS NOT NULL AND metric IN ('input_tokens','output_tokens') ORDER BY order_id,metric"),
      this.database.getAll<OrderRow>("SELECT o.id,m.name model,o.subscription_key_id,o.gross_microcredits,o.status,o.created_at FROM my_ai_orders o JOIN models m ON m.id=o.model_id ORDER BY o.created_at,o.id"),
      this.database.getAll<ProviderKeyRow>("SELECT k.id,p.name provider,k.name,k.key,k.status,k.earned_microcredits,k.last_used_at,k.prices_json FROM my_provider_keys k JOIN providers p ON p.id=k.provider_id ORDER BY k.created_at,k.id"),
    ]);
    const rates = new Map<string, Model["rates"]>();
    for (const price of prices) (rates.get(price.model_id) ?? (rates.set(price.model_id, []), rates.get(price.model_id)!)).push({ metric: price.metric, unit: price.unit_size === 1_000_000 ? "1M" : String(price.unit_size), credits: Number(price.price_microcredits) });
    const usageByOrder = new Map<string, UsageRow[]>();
    for (const row of usageRows) (usageByOrder.get(row.order_id) ?? (usageByOrder.set(row.order_id, []), usageByOrder.get(row.order_id)!)).push(row);
    const creditByDay = new Map<string, number>();
    for (const order of orders) {
      const day = new Date(order.created_at).toISOString().slice(0, 10);
      creditByDay.set(day, (creditByDay.get(day) ?? 0) + Number(order.gross_microcredits));
    }
    return {
      region,
      models: models.map(({ provider_id, ...model }) => ({ ...model, providerId: provider_id, rates: rates.get(model.id) ?? [] })),
      usage: Array.from(creditByDay, ([day, value]) => ({ day: new Date(`${day}T00:00:00Z`).toLocaleDateString("en-US", { weekday: "short" }), credits: value })).slice(-7) satisfies UsagePoint[],
      orders: orders.map<AIOrder>((order) => {
        const metrics = (usageByOrder.get(order.id) ?? []).map((row) => ({ metric: row.metric, quantity: Number(row.quantity) }));
        const status: AIOrder["status"] = order.status === "charged" ? "completed" : order.status === "billing_failed" ? "failed" : "pending";
        return { id: order.id, model: order.model, keyName: order.subscription_key_id, credits: Number(order.gross_microcredits), tokens: metrics.reduce((sum, metric) => sum + metric.quantity, 0), metrics, status, createdAt: order.created_at };
      }),
      providerKeys: providerKeys.map<ProviderKey>((key) => {
        const prices = readPrices(key.prices_json);
        return { id: key.id, provider: key.provider, name: key.name, maskedValue: key.key, modelCount: new Set(prices.map((price) => price.modelId)).size, status: key.status, earnedCredits: Number(key.earned_microcredits), lastUsedAt: key.last_used_at ?? undefined, prices };
      }),
    };
  }

  async createProviderKey(region: Region, draft: ProviderKeyDraft) {
    const id = crypto.randomUUID(), now = new Date().toISOString(), prices = wirePrices(draft.prices);
    const write = () => this.database.execute("INSERT INTO my_provider_keys(id,provider_id,key,merchant_id,name,last_used_at,earned_microcredits,status,prices_json,created_at,updated_at) VALUES (?,?,?,'',?,NULL,0,'active',?,?,?)", [id, draft.providerId, draft.key, draft.name, JSON.stringify(prices), now, now]);
    if (this.mutations) {
      await this.mutations.run("my_provider_keys", id, write);
      await this.waitForProviderKey(id, (row) => row.merchant_id !== "");
    } else {
      await write();
    }
    return this.getSnapshot(region);
  }

  async updateProviderKeyPrices(region: Region, id: string, prices: ProviderPrice[]) {
    const value = JSON.stringify(wirePrices(prices));
    const write = () => this.database.execute("UPDATE my_provider_keys SET prices_json=?,updated_at=? WHERE id=?", [value, new Date().toISOString(), id]);
    if (this.mutations) await this.mutations.run("my_provider_keys", id, write); else await write();
    return this.getSnapshot(region);
  }

  async disableProviderKey(region: Region, id: string) {
    const write = () => this.database.execute("UPDATE my_provider_keys SET status='disabled',updated_at=? WHERE id=?", [new Date().toISOString(), id]);
    if (this.mutations) await this.mutations.run("my_provider_keys", id, write); else await write();
    return this.getSnapshot(region);
  }

  private async waitForProviderKey(id: string, predicate: (row: { merchant_id: string }) => boolean) {
    const deadline = Date.now() + 30_000;
    while (Date.now() < deadline) {
      const row = await this.database.getOptional<{ merchant_id: string }>("SELECT merchant_id FROM my_provider_keys WHERE id=?", [id]);
      if (row && predicate(row)) return;
      await new Promise((resolve) => setTimeout(resolve, 100));
    }
    throw new Error("PowerSync did not deliver the authoritative GizWay state within 30 seconds");
  }
}

function wirePrices(prices: ProviderPrice[]) {
  return prices.map((price) => ({ model_id: price.modelId, metric: price.metric, unit_size: price.unitSize, microcredits_per_unit: price.microcreditsPerUnit }));
}

function readPrices(value: string): ProviderPrice[] {
  const prices = JSON.parse(value) as Array<{ model_id?: unknown; metric?: unknown; unit_size?: unknown; microcredits_per_unit?: unknown }>;
  return prices.flatMap((price) => {
    if (typeof price.model_id !== "string" || (price.metric !== "input_tokens" && price.metric !== "output_tokens")) return [];
    return [{ modelId: price.model_id, metric: price.metric, unitSize: Number(price.unit_size) || 1_000_000, microcreditsPerUnit: Number(price.microcredits_per_unit) || 0 }];
  });
}
