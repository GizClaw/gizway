import type { PowerSyncDatabase } from "@powersync/web";
import type { Charge, Commission, GizPayRepository, GizPaySnapshot, Product, Subscription, SubscriptionKey, TopUp, Transaction } from "../../contracts/gizpay";
import type { MutationCoordinator } from "../lifecycle";

export class PowerSyncGizPayRepository implements GizPayRepository {
  constructor(private readonly database: PowerSyncDatabase, private readonly mutations?: MutationCoordinator) {}

  async getSnapshot(): Promise<GizPaySnapshot> {
    const [profile, balance, products, subscriptions, keys, transactions, topUps, charges, commissions] = await Promise.all([
      this.database.get<{ id: string; display_name: string; email: string; merchant_id: string }>("SELECT id,display_name,email,merchant_id FROM my_profile LIMIT 1"),
      this.database.get<{ balance_microcredits: number }>("SELECT balance_microcredits FROM my_balances LIMIT 1"),
      this.database.getAll<{ id: string; name: string; description: string; billing_mode: string; price_text: string; status: string; terms_version: string }>("SELECT p.id,p.name,COALESCE(l.description,'') description,p.billing_mode,COALESCE(l.price_text,'') price_text,p.status,p.terms_version FROM available_products p LEFT JOIN product_listings l ON l.product_id=p.id ORDER BY l.display_order,p.id"),
      this.database.getAll<{ id: string; product_id: string; status: string; terms_version: string; created_at: string }>("SELECT id,product_id,status,terms_version,created_at FROM my_subscriptions ORDER BY created_at,id"),
      this.database.getAll<{ id: string; name: string; key: string; subscription_id: string; created_at: string; last_used_at: string | null; status: string }>("SELECT id,name,key,subscription_id,created_at,last_used_at,status FROM my_subscription_keys ORDER BY created_at,id"),
      this.database.getAll<{ id: string; transaction_type: string; amount_microcredits: number; created_at: string }>("SELECT id,transaction_type,amount_microcredits,created_at FROM my_transactions ORDER BY created_at DESC,id"),
      this.database.getAll<{ id: string; amount_microcredits: number; channel: string; status: string; created_at: string | null; credited_at: string | null }>("SELECT id,amount_microcredits,channel,status,created_at,credited_at FROM my_topups ORDER BY COALESCE(created_at,credited_at) DESC,id"),
      this.database.getAll<{ id: string; external_order_id: string; gross_microcredits: number; created_at: string }>("SELECT id,external_order_id,gross_microcredits,created_at FROM my_charges ORDER BY created_at DESC,id"),
      this.database.getAll<{ id: string; charge_id: string; amount_microcredits: number; created_at: string }>("SELECT id,charge_id,amount_microcredits,created_at FROM my_commissions ORDER BY created_at DESC,id"),
    ]);
    return {
      profile: { id: profile.id, displayName: profile.display_name, email: profile.email, merchantId: profile.merchant_id },
      balance: { availableMicrocredits: Number(balance.balance_microcredits), currency: "GIZ_CREDIT" },
      products: products.map<Product>((row) => ({ id: row.id, name: row.name, description: row.description, billingMode: row.billing_mode, priceText: row.price_text, status: row.status, termsVersion: row.terms_version })),
      subscriptions: subscriptions.map<Subscription>((row) => ({ id: row.id, productId: row.product_id, status: row.status, termsVersion: row.terms_version, createdAt: row.created_at })),
      keys: keys.map<SubscriptionKey>((row) => ({ id: row.id, name: row.name, value: row.key, subscriptionId: row.subscription_id, createdAt: row.created_at, lastUsedAt: row.last_used_at ?? undefined, status: row.status })),
      transactions: transactions.map<Transaction>((row) => ({ id: row.id, type: row.transaction_type, amountMicrocredits: Number(row.amount_microcredits), createdAt: row.created_at })),
      topUps: topUps.map<TopUp>((row) => ({ id: row.id, amountMicrocredits: Number(row.amount_microcredits), channel: row.channel, status: row.status, createdAt: row.created_at ?? undefined, creditedAt: row.credited_at ?? undefined })),
      charges: charges.map<Charge>((row) => ({ id: row.id, externalOrderId: row.external_order_id, grossMicrocredits: Number(row.gross_microcredits), createdAt: row.created_at })),
      commissions: commissions.map<Commission>((row) => ({ id: row.id, chargeId: row.charge_id, amountMicrocredits: Number(row.amount_microcredits), createdAt: row.created_at })),
    };
  }

  async createTopUp(amountMicrocredits: number, channel: string, externalReference: string): Promise<GizPaySnapshot> {
    if (!Number.isSafeInteger(amountMicrocredits) || amountMicrocredits <= 0 || !channel || !externalReference) throw new Error("top-up input is invalid");
    const account = await this.database.get<{ id: string }>("SELECT id FROM my_accounts WHERE status='active' ORDER BY created_at,id LIMIT 1");
    const id = crypto.randomUUID();
    await this.mutate("my_topups", id, () => this.database.execute("INSERT INTO my_topups(id,account_id,amount_microcredits,channel,external_reference,status,credited_at) VALUES(?,?,?,?,?,'pending',NULL)", [id, account.id, amountMicrocredits, channel, externalReference]));
    return this.getSnapshot();
  }
  async createSubscription(productId: string): Promise<GizPaySnapshot> {
    const account = await this.database.get<{ id: string }>("SELECT id FROM my_accounts WHERE status='active' ORDER BY created_at,id LIMIT 1");
    const product = await this.database.get<{ terms_version: string }>("SELECT terms_version FROM available_products WHERE id=? AND status='active'", [productId]);
    const id = crypto.randomUUID();
    await this.mutate("my_subscriptions", id, () => this.database.execute("INSERT INTO my_subscriptions(id,account_id,product_id,status,terms_version,created_at) VALUES(?,?,?,'active',?,?)", [id, account.id, productId, product.terms_version, new Date().toISOString()]));
    return this.getSnapshot();
  }
  async createSubscriptionKey(subscriptionId: string, name: string): Promise<GizPaySnapshot> {
    const id = crypto.randomUUID();
    await this.mutate("my_subscription_keys", id, () => this.database.execute("INSERT INTO my_subscription_keys(id,subscription_id,name,key,status,created_at,last_used_at,revoked_at) VALUES(?,?,?,'','active',?,NULL,NULL)", [id, subscriptionId, name, new Date().toISOString()]));
    return this.getSnapshot();
  }
  async revokeSubscriptionKey(subscriptionId: string, id: string): Promise<GizPaySnapshot> {
    await this.mutate("my_subscription_keys", id, () => this.database.execute("UPDATE my_subscription_keys SET subscription_id=?,status='revoked' WHERE id=?", [subscriptionId, id]));
    return this.getSnapshot();
  }
  private mutate(table: string, id: string, write: () => Promise<unknown>): Promise<void> {
    if (!this.mutations) throw new Error("public Catalog connection is read-only");
    return this.mutations.run(table, id, write);
  }
}
