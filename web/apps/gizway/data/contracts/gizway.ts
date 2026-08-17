export type Region = "global" | "cn";

export type ModelRate = {
  metric: string;
  unit: string;
  credits: number;
};

export type Model = {
  id: string;
  providerId?: string;
  name: string;
  provider: string;
  family: "chat" | "image" | "audio" | "video" | "realtime";
  description: string;
  rates: ModelRate[];
  latency: string;
  context: string;
  accent: string;
};

export type UsagePoint = {
  day: string;
  credits: number;
};

export type UsageMetric = {
  metric: "input_tokens" | "output_tokens";
  quantity: number;
};

export type AIOrder = {
  id: string;
  model: string;
  keyName: string;
  credits: number;
  tokens: number;
  metrics: UsageMetric[];
  status: "pending" | "completed" | "failed";
  createdAt: string;
};

export type ProviderKey = {
  id: string;
  provider: string;
  name: string;
  maskedValue: string;
  modelCount: number;
  status: "active" | "disabled";
  earnedCredits: number;
  lastUsedAt?: string;
  prices: ProviderPrice[];
};

export type ProviderPrice = { modelId: string; metric: "input_tokens" | "output_tokens"; unitSize: number; microcreditsPerUnit: number };
export type ProviderKeyDraft = { providerId: string; name: string; key: string; prices: ProviderPrice[] };

export type GizWaySnapshot = {
  region: Region;
  models: Model[];
  usage: UsagePoint[];
  orders: AIOrder[];
  providerKeys: ProviderKey[];
};

export interface GizWayRepository {
  getSnapshot(region: Region): Promise<GizWaySnapshot>;
  createProviderKey(region: Region, draft: ProviderKeyDraft): Promise<GizWaySnapshot>;
  updateProviderKeyPrices(region: Region, id: string, prices: ProviderPrice[]): Promise<GizWaySnapshot>;
  disableProviderKey(region: Region, id: string): Promise<GizWaySnapshot>;
}
