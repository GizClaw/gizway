export type CreditBalance = {
  available: number;
  currency: "GIZ_CREDIT";
  updatedAt: string;
};

export type Product = {
  id: string;
  name: string;
  description: string;
  billing: "pay_as_you_go" | "monthly";
  priceLabel: string;
  active: boolean;
};

export type Subscription = {
  id: string;
  productId: string;
  productName: string;
  status: "active" | "paused" | "inactive";
  startedAt: string;
};

export type SubscriptionKey = {
  id: string;
  name: string;
  value: string;
  subscriptionId: string;
  createdAt: string;
  lastUsedAt?: string;
  status: "active" | "revoked";
};

export type LedgerItem = {
  id: string;
  label: string;
  kind: "topup" | "usage" | "commission";
  amount: number;
  createdAt: string;
};

export type TopUpRecord = { id: string; amount: number; channel: string; status: string; createdAt: string };
export type ChargeRecord = { id: string; externalOrderId: string; amount: number; createdAt: string };
export type CommissionRecord = { id: string; chargeId: string; amount: number; createdAt: string };

export type GizPaySnapshot = {
  user: { id: string; name: string; email: string; merchantId: string; merchantName: string; merchantStatus: string };
  balance: CreditBalance;
  products: Product[];
  subscriptions: Subscription[];
  keys: SubscriptionKey[];
  ledger: LedgerItem[];
  topUps: TopUpRecord[];
  charges: ChargeRecord[];
  commissions: CommissionRecord[];
};

export interface GizPayRepository {
  getSnapshot(): Promise<GizPaySnapshot>;
  topUp(amount: number): Promise<GizPaySnapshot>;
  createSubscriptionKey(name: string): Promise<GizPaySnapshot>;
  revokeSubscriptionKey(id: string): Promise<GizPaySnapshot>;
}
