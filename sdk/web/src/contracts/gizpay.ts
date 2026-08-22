export type CreditBalance = { availableMicrocredits: number; currency: "GIZ_CREDIT" };
export type Product = { id: string; name: string; description: string; billingMode: string; priceText: string; status: string; termsVersion: string };
export type CatalogProduct = { id: string; title: string; description: string; billingMode: string; priceText: string; status: string };
export type Subscription = { id: string; productId: string; status: string; termsVersion: string; createdAt: string };
export type SubscriptionKey = { id: string; name: string; value: string; subscriptionId: string; createdAt: string; lastUsedAt?: string; status: string };
export type Transaction = { id: string; type: string; amountMicrocredits: number; createdAt: string };
export type TopUp = { id: string; amountMicrocredits: number; channel: string; status: string; createdAt?: string; creditedAt?: string };
export type Charge = { id: string; externalOrderId: string; grossMicrocredits: number; createdAt: string };
export type Commission = { id: string; chargeId: string; amountMicrocredits: number; createdAt: string };
export type GizPaySnapshot = {
  profile: { id: string; displayName: string; email: string; merchantId: string };
  balance: CreditBalance;
  products: Product[];
  subscriptions: Subscription[];
  keys: SubscriptionKey[];
  transactions: Transaction[];
  topUps: TopUp[];
  charges: Charge[];
  commissions: Commission[];
};

export interface GizPayRepository {
  getSnapshot(): Promise<GizPaySnapshot>;
  createTopUp(amountMicrocredits: number, channel: string, externalReference: string): Promise<GizPaySnapshot>;
  createSubscription(productId: string): Promise<GizPaySnapshot>;
  createSubscriptionKey(subscriptionId: string, name: string): Promise<GizPaySnapshot>;
  revokeSubscriptionKey(subscriptionId: string, id: string): Promise<GizPaySnapshot>;
}
