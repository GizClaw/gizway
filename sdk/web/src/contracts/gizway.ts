export type Region = "global" | "cn";

export type ModelRate = { metric: string; unitSize: number; microcreditsPerUnit: number };
export type Model = {
  id: string;
  providerId: string;
  name: string;
  providerName: string;
  family: string;
  description: string;
  context: string;
  latency: string;
  availability: string;
  rates: ModelRate[];
};
export type CatalogModel = { id: string; title: string; family: string; description: string; context: string; latency: string; availability: string; rates: ModelRate[] };
export type UsageMetric = { metric: "input_tokens" | "output_tokens"; quantity: number };
export type AIOrder = {
  id: string;
  modelId: string;
  modelName: string;
  subscriptionKeyId: string;
  grossMicrocredits: number;
  metrics: UsageMetric[];
  status: string;
  createdAt: string;
  completedAt?: string;
};
export type ProviderPrice = { modelId: string; metric: "input_tokens" | "output_tokens"; unitSize: number; microcreditsPerUnit: number };
export type ProviderKey = {
  id: string;
  providerId: string;
  providerName: string;
  name: string;
  maskedValue: string;
  status: "active" | "disabled";
  earnedMicrocredits: number;
  lastUsedAt?: string;
  prices: ProviderPrice[];
};
export type ProviderKeyDraft = { providerId: string; name: string; key: string; prices: ProviderPrice[] };
export type GizWaySnapshot = { region: Region; models: Model[]; orders: AIOrder[]; providerKeys: ProviderKey[] };

export interface GizWayRepository {
  getSnapshot(): Promise<GizWaySnapshot>;
  createProviderKey(draft: ProviderKeyDraft): Promise<GizWaySnapshot>;
  updateProviderKeyPrices(id: string, prices: ProviderPrice[]): Promise<GizWaySnapshot>;
  disableProviderKey(id: string): Promise<GizWaySnapshot>;
}
