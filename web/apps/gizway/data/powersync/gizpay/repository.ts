import type { PowerSyncDatabase } from "@powersync/web";
import type { ChargeRecord, CommissionRecord, GizPayRepository, GizPaySnapshot, LedgerItem, Product, Subscription, SubscriptionKey, TopUpRecord } from "@/data/contracts/gizpay";
import type { FakeScenario } from "@/data/fake/scenarios";
import { gizPayFixture } from "@/data/fake/gizpay/fixtures";

type ProfileRow = { id: string; display_name: string; email: string; merchant_id: string };
type BalanceRow = { balance_microcredits: number };
type MerchantRow = { id: string; public_name: string; status: string };
type ProductRow = { id: string; name: string; description: string; billing_mode: Product["billing"]; price_text: string; status: string };
type SubscriptionRow = { id: string; product_id: string; status: Subscription["status"]; created_at: string; product_name: string };
type KeyRow = { id: string; name: string; key: string; subscription_id: string; created_at: string; last_used_at: string | null; status: SubscriptionKey["status"] };
type TransactionRow = { id: string; transaction_type: string; amount_microcredits: number; created_at: string };
type TopUpRow = { id: string; amount_microcredits: number; channel: string; status: string; created_at: string | null; credited_at: string | null };
type ChargeRow = { id: string; external_order_id: string; gross_microcredits: number; created_at: string };
type CommissionRow = { id: string; charge_id: string; amount_microcredits: number; created_at: string };
type MutationRunner = { run(table: string, id: string, write: () => Promise<unknown>): Promise<unknown> };

export class PowerSyncGizPayRepository implements GizPayRepository {
  constructor(private readonly database: PowerSyncDatabase, private readonly mutations?: MutationRunner, private paygProductID?: string) {}

  selectPAYGProduct(productID: string) {
    this.paygProductID = productID;
  }

  async seed(scenario: FakeScenario) {
    const fixture = gizPayFixture(scenario);
    this.paygProductID = fixture.products.find((product) => product.active)?.id;
    await this.database.disconnectAndClear();
    await this.database.writeTransaction(async (tx) => {
      const now = new Date().toISOString();
      await tx.execute("INSERT INTO my_profile (id, email, display_name, merchant_id, status, created_at) VALUES (?, ?, ?, ?, 'active', ?)", [fixture.user.id, fixture.user.email, fixture.user.name, fixture.user.merchantId, now]);
      await tx.execute("INSERT INTO my_accounts (id, owner_user_id, status, created_at) VALUES ('personal', ?, 'active', ?)", [fixture.user.id, now]);
      await tx.execute("INSERT INTO my_balances (id, account_id, balance_microcredits) VALUES ('personal', 'personal', ?)", [fixture.balance.available]);
      await tx.execute("INSERT INTO my_merchants (id, settlement_account_id, legal_name, public_name, is_default, status, created_at, updated_at) VALUES (?, 'personal', ?, ?, 1, 'active', ?, ?)", [fixture.user.merchantId, fixture.user.name, fixture.user.name, now, now]);
      if (fixture.products.length > 0) {
        await tx.executeBatch("INSERT INTO product_listings (id, product_id, site, title, description, billing_mode, price_text, display_order, status, created_at, updated_at) VALUES (?, ?, 'fake', ?, ?, ?, ?, ?, ?, ?, ?)", fixture.products.map((product, index) => [`listing_${product.id}`, product.id, product.name, product.description, product.billing, product.priceLabel, index, product.active ? "active" : "inactive", now, now]));
        await tx.executeBatch("INSERT INTO available_products (id, merchant_id, name, billing_mode, status, terms_version) VALUES (?, ?, ?, ?, ?, 'fake-v1')", fixture.products.map((product) => [product.id, fixture.user.merchantId, product.name, product.billing, product.active ? "active" : "inactive"]));
      }
      if (fixture.subscriptions.length > 0) await tx.executeBatch("INSERT INTO my_subscriptions (id, account_id, product_id, status, terms_version, created_at) VALUES (?, 'personal', ?, ?, 'fake-v1', ?)", fixture.subscriptions.map((subscription) => [subscription.id, subscription.productId, subscription.status, subscription.startedAt]));
      if (fixture.keys.length > 0) await tx.executeBatch("INSERT INTO my_subscription_keys (id, subscription_id, name, key, status, created_at, last_used_at, revoked_at) VALUES (?, ?, ?, ?, ?, ?, ?, NULL)", fixture.keys.map((key) => [key.id, key.subscriptionId, key.name, key.value, key.status, key.createdAt, key.lastUsedAt ?? null]));
      if (fixture.ledger.length > 0) await tx.executeBatch("INSERT INTO my_transactions (id, account_id, transaction_type, amount_microcredits, created_at) VALUES (?, 'personal', ?, ?, ?)", fixture.ledger.map((item) => [item.id, item.kind, item.amount, item.createdAt]));
      if (fixture.topUps.length > 0) await tx.executeBatch("INSERT INTO my_topups (id,account_id,amount_microcredits,channel,external_reference,status,created_at,credited_at) VALUES (?,'personal',?,?,?, ?,?,?)", fixture.topUps.map((item) => [item.id, item.amount, item.channel, `fake-${item.id}`, item.status, item.createdAt, item.createdAt]));
      if (fixture.charges.length > 0) await tx.executeBatch("INSERT INTO my_charges (id,account_id,subscription_id,external_order_id,gross_microcredits,order_snapshot,created_at) VALUES (?,'personal','subscription',?,?, '{}',?)", fixture.charges.map((item) => [item.id, item.externalOrderId, item.amount, item.createdAt]));
      if (fixture.commissions.length > 0) await tx.executeBatch("INSERT INTO my_commissions (id,merchant_id,charge_id,amount_microcredits,created_at) VALUES (?,?,?, ?,?)", fixture.commissions.map((item) => [item.id, fixture.user.merchantId, item.chargeId, item.amount, item.createdAt]));
    });
  }

  async getSnapshot(): Promise<GizPaySnapshot> {
    const [user, balance, merchant, products, subscriptions, keys, transactions, topUps, charges, commissions] = await Promise.all([
      this.database.get<ProfileRow>("SELECT id, display_name, email, merchant_id FROM my_profile LIMIT 1"),
      this.database.get<BalanceRow>("SELECT balance_microcredits FROM my_balances LIMIT 1"),
      this.database.get<MerchantRow>("SELECT id,public_name,status FROM my_merchants WHERE is_default = 1 LIMIT 1"),
      this.database.getAll<ProductRow>("SELECT p.id, p.name, COALESCE(l.description, '') description, p.billing_mode, COALESCE(l.price_text, '') price_text, p.status FROM available_products p LEFT JOIN product_listings l ON l.product_id=p.id ORDER BY l.display_order, p.id"),
      this.database.getAll<SubscriptionRow>("SELECT s.id,s.product_id,s.status,s.created_at,p.name product_name FROM my_subscriptions s JOIN available_products p ON p.id=s.product_id ORDER BY s.created_at,s.id"),
      this.database.getAll<KeyRow>("SELECT id,name,key,subscription_id,created_at,last_used_at,status FROM my_subscription_keys ORDER BY created_at,id"),
      this.database.getAll<TransactionRow>("SELECT id,transaction_type,amount_microcredits,created_at FROM my_transactions ORDER BY created_at DESC,id"),
      this.database.getAll<TopUpRow>("SELECT id,amount_microcredits,channel,status,created_at,credited_at FROM my_topups ORDER BY COALESCE(created_at,credited_at) DESC,id"),
      this.database.getAll<ChargeRow>("SELECT id,external_order_id,gross_microcredits,created_at FROM my_charges ORDER BY created_at DESC,id"),
      this.database.getAll<CommissionRow>("SELECT id,charge_id,amount_microcredits,created_at FROM my_commissions ORDER BY created_at DESC,id"),
    ]);
    const transactionKind = (value: string): LedgerItem["kind"] => value === "topup" ? "topup" : value === "commission" ? "commission" : "usage";
    return {
      user: { id: user.id, name: user.display_name, email: user.email, merchantId: user.merchant_id, merchantName: merchant.public_name, merchantStatus: merchant.status },
      balance: { available: Number(balance.balance_microcredits), currency: "GIZ_CREDIT", updatedAt: transactions[0]?.created_at ?? "" },
      products: products.map((p) => ({ id: p.id, name: p.name, description: p.description, billing: p.billing_mode, priceLabel: p.price_text, active: p.status === "active" })),
      subscriptions: subscriptions.map((s) => ({ id: s.id, productId: s.product_id, productName: s.product_name, status: s.status, startedAt: s.created_at })),
      keys: keys.map((k) => ({ id: k.id, name: k.name, value: k.key, subscriptionId: k.subscription_id, createdAt: k.created_at, lastUsedAt: k.last_used_at ?? undefined, status: k.status })),
      ledger: transactions.map<LedgerItem>((t) => ({ id: t.id, label: t.transaction_type === "topup" ? "Credit top-up" : t.transaction_type === "commission" ? "Provider commission" : "AI charge", kind: transactionKind(t.transaction_type), amount: Number(t.amount_microcredits), createdAt: t.created_at })),
      topUps: topUps.map<TopUpRecord>((item) => ({ id: item.id, amount: Number(item.amount_microcredits), channel: item.channel, status: item.status, createdAt: item.created_at ?? item.credited_at ?? "" })),
      charges: charges.map<ChargeRecord>((item) => ({ id: item.id, externalOrderId: item.external_order_id, amount: Number(item.gross_microcredits), createdAt: item.created_at })),
      commissions: commissions.map<CommissionRecord>((item) => ({ id: item.id, chargeId: item.charge_id, amount: Number(item.amount_microcredits), createdAt: item.created_at })),
    };
  }

  async topUp(amount: number) {
    const now = new Date().toISOString(), id = crypto.randomUUID();
	if (this.mutations) {
		const account = await this.database.get<{ id: string }>("SELECT id FROM my_accounts WHERE status='active' ORDER BY created_at,id LIMIT 1");
		await this.mutations.run("my_topups", id, () => this.database.execute("INSERT INTO my_topups (id,account_id,amount_microcredits,channel,external_reference,status,credited_at) VALUES (?,?,?,'fake',?,'pending',NULL)", [id, account.id, amount, `fake-${id}`]));
		await this.waitForRow("SELECT EXISTS(SELECT 1 FROM my_topups WHERE id=? AND status='succeeded') ready", id);
		return this.getSnapshot();
	}
    await this.database.writeTransaction(async (tx) => {
      await tx.execute("UPDATE my_balances SET balance_microcredits=balance_microcredits+? WHERE id='personal'", [amount]);
      await tx.execute("INSERT INTO my_topups (id,account_id,amount_microcredits,channel,external_reference,status,credited_at) VALUES (?,'personal',?,'fake',?,'succeeded',?)", [id, amount, `fake-${id}`, now]);
      await tx.execute("INSERT INTO my_transactions (id,account_id,transaction_type,amount_microcredits,created_at) VALUES (?,'personal','topup',?,?)", [`txn_${id}`, amount, now]);
    });
    return this.getSnapshot();
  }

  async createSubscriptionKey(name: string) {
    if (!this.paygProductID) throw new Error("current PAYG Product is not selected");
    const subscription = await this.database.getOptional<{ id: string }>("SELECT id FROM my_subscriptions WHERE product_id=? AND status='active' ORDER BY created_at,id LIMIT 1", [this.paygProductID]);
    if (!subscription) throw new Error("current PAYG Subscription is not ready");
    const id = crypto.randomUUID();
	if (this.mutations) {
		await this.mutations.run("my_subscription_keys", id, () => this.database.execute("INSERT INTO my_subscription_keys (id,subscription_id,name,key,status,created_at,last_used_at,revoked_at) VALUES (?,?,?,'','active',?,NULL,NULL)", [id, subscription.id, name, new Date().toISOString()]));
		return this.waitForSnapshot((snapshot) => snapshot.keys.some((key) => key.id === id && key.value !== ""));
	}
    await this.database.execute("INSERT INTO my_subscription_keys (id,subscription_id,name,key,status,created_at,last_used_at,revoked_at) VALUES (?,?,?,?,'active',?,NULL,NULL)", [id, subscription.id, name, `giz_live_${id.replaceAll("-", "")}`, new Date().toISOString()]);
    return this.getSnapshot();
  }

	async revokeSubscriptionKey(id: string) {
		if (this.mutations) {
			await this.mutations.run("my_subscription_keys", id, () => this.database.execute("UPDATE my_subscription_keys SET status='revoked' WHERE id=?", [id]));
			return this.getSnapshot();
		}
		await this.database.execute("UPDATE my_subscription_keys SET status='revoked',revoked_at=? WHERE id=?", [new Date().toISOString(), id]);
		return this.getSnapshot();
	}

	private async waitForSnapshot(predicate: (snapshot: GizPaySnapshot) => boolean): Promise<GizPaySnapshot> {
		const deadline = Date.now() + 30_000;
		while (Date.now() < deadline) {
			const snapshot = await this.getSnapshot();
			if (predicate(snapshot)) return snapshot;
			await new Promise((resolve) => setTimeout(resolve, 100));
		}
		throw new Error("PowerSync did not deliver the authoritative GizPay state within 30 seconds");
	}

	private async waitForRow(query: string, id: string): Promise<void> {
		const deadline = Date.now() + 30_000;
		while (Date.now() < deadline) {
			if ((await this.database.get<{ ready: number }>(query, [id])).ready === 1) return;
			await new Promise((resolve) => setTimeout(resolve, 100));
		}
		throw new Error("PowerSync did not deliver the authoritative GizPay state within 30 seconds");
	}
}
