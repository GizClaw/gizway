import type { GizPaySnapshot } from "@/data/contracts/gizpay";
import type { FakeScenario } from "@/data/fake/scenarios";

const active: GizPaySnapshot = {
  user: {
    id: "usr_idy_demo",
    name: "Idy",
    email: "idy@gizway.com",
    merchantId: "mrc_idy_default",
    merchantName: "Idy",
    merchantStatus: "active",
  },
  balance: {
    available: 284_650,
    currency: "GIZ_CREDIT",
    updatedAt: "2026-08-15T14:18:00+08:00",
  },
  products: [
    {
      id: "prod_payg",
      name: "Pay as you go",
      description: "Access every available model and pay only for what you use.",
      billing: "pay_as_you_go",
      priceLabel: "Usage based",
      active: true,
    },
    {
      id: "prod_pro",
      name: "GizWay Pro",
      description: "A monthly plan for daily AI work.",
      billing: "monthly",
      priceLabel: "Coming soon",
      active: false,
    },
  ],
  subscriptions: [
    {
      id: "sub_payg_01",
      productId: "prod_payg",
      productName: "Pay as you go",
      status: "active",
      startedAt: "2026-07-02T09:00:00+08:00",
    },
  ],
  keys: [
    {
      id: "skey_workbench",
      name: "Workbench",
      value: "giz_live_Q8v1d9Kx7Gm2pR4n",
      subscriptionId: "sub_payg_01",
      createdAt: "2026-07-02T09:06:00+08:00",
      lastUsedAt: "2026-08-15T13:52:00+08:00",
      status: "active",
    },
    {
      id: "skey_video",
      name: "Video studio",
      value: "giz_live_M4a7p2Vs9Ld3cX6q",
      subscriptionId: "sub_payg_01",
      createdAt: "2026-07-18T18:20:00+08:00",
      lastUsedAt: "2026-08-14T22:10:00+08:00",
      status: "active",
    },
  ],
  ledger: [
    { id: "txn_01", label: "AI usage · Claude Sonnet 4", kind: "usage", amount: -1_284, createdAt: "2026-08-17T13:52:00+08:00" },
    { id: "txn_02", label: "Provider earnings · DeepSeek", kind: "commission", amount: 6_420, createdAt: "2026-08-17T11:06:00+08:00" },
    { id: "txn_03", label: "Credit top-up", kind: "topup", amount: 200_000, createdAt: "2026-08-12T09:32:00+08:00" },
    { id: "txn_04", label: "AI usage · GPT-5", kind: "usage", amount: -3_760, createdAt: "2026-08-11T21:18:00+08:00" },
  ],
  topUps: [{ id: "topup_01", amount: 200_000, channel: "fake", status: "succeeded", createdAt: "2026-08-12T09:32:00+08:00" }],
  charges: [{ id: "charge_01", externalOrderId: "ord_01", amount: 1_284, createdAt: "2026-08-17T13:52:00+08:00" }, { id: "charge_02", externalOrderId: "ord_02", amount: 3_760, createdAt: "2026-08-16T21:18:00+08:00" }],
  commissions: [{ id: "commission_01", chargeId: "charge_01", amount: 6_420, createdAt: "2026-08-17T11:06:00+08:00" }],
};

export function gizPayFixture(scenario: FakeScenario): GizPaySnapshot {
  const value = structuredClone(active);
  if (scenario === "new-user") {
    value.balance.available = 0;
    value.subscriptions[0].id = "sub_payg_new_user";
    value.subscriptions[0].startedAt = "2026-08-15T14:18:00+08:00";
    value.keys = [];
    value.ledger = [];
    value.topUps = [];
    value.charges = [];
    value.commissions = [];
  }
  if (scenario === "low-credit") {
    value.balance.available = 720;
  }
  return value;
}
