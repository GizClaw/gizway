import type { GizWaySnapshot, Region } from "@/data/contracts/gizway";
import type { FakeScenario } from "@/data/fake/scenarios";

const usage = [
  { day: "Mon", credits: 14200 },
  { day: "Tue", credits: 21800 },
  { day: "Wed", credits: 17600 },
  { day: "Thu", credits: 29400 },
  { day: "Fri", credits: 23600 },
  { day: "Sat", credits: 34200 },
  { day: "Sun", credits: 26800 },
];

const common = {
  usage,
  orders: [
    { id: "ord_01", model: "Claude Sonnet 4", keyName: "Workbench", credits: 1284, tokens: 18242, metrics: [{ metric: "input_tokens" as const, quantity: 16000 }, { metric: "output_tokens" as const, quantity: 2242 }], status: "completed" as const, createdAt: "2026-08-17T13:52:00+08:00" },
    { id: "ord_02", model: "GPT-5", keyName: "Workbench", credits: 3760, tokens: 24908, metrics: [{ metric: "input_tokens" as const, quantity: 21000 }, { metric: "output_tokens" as const, quantity: 3908 }], status: "completed" as const, createdAt: "2026-08-16T21:18:00+08:00" },
    { id: "ord_03", model: "Veo 3", keyName: "Video studio", credits: 8400, tokens: 12000, metrics: [{ metric: "input_tokens" as const, quantity: 4000 }, { metric: "output_tokens" as const, quantity: 8000 }], status: "pending" as const, createdAt: "2026-08-15T18:30:00+08:00" },
    { id: "ord_04", model: "Gemini 2.5 Flash", keyName: "Workbench", credits: 340, tokens: 8840, metrics: [{ metric: "input_tokens" as const, quantity: 7600 }, { metric: "output_tokens" as const, quantity: 1240 }], status: "failed" as const, createdAt: "2026-08-14T09:12:00+08:00" },
  ],
  providerKeys: [
    { id: "pkey_deepseek", provider: "DeepSeek", name: "Production pool", maskedValue: "sk-••••••••42QF", modelCount: 1, status: "active" as const, earnedCredits: 36420, lastUsedAt: "2026-08-17T11:06:00+08:00", prices: [{ modelId: "deepseek/deepseek-v3.1", metric: "input_tokens" as const, unitSize: 1_000_000, microcreditsPerUnit: 180 }, { modelId: "deepseek/deepseek-v3.1", metric: "output_tokens" as const, unitSize: 1_000_000, microcreditsPerUnit: 720 }] },
    { id: "pkey_openai", provider: "OpenAI", name: "Personal tier", maskedValue: "sk-proj-••••7MJ2", modelCount: 1, status: "active" as const, earnedCredits: 12860, lastUsedAt: "2026-08-16T21:18:00+08:00", prices: [{ modelId: "openai/gpt-5", metric: "input_tokens" as const, unitSize: 1_000_000, microcreditsPerUnit: 900 }, { modelId: "openai/gpt-5", metric: "output_tokens" as const, unitSize: 1_000_000, microcreditsPerUnit: 7_000 }] },
  ],
};

export function gizWayFixture(region: Region, scenario: FakeScenario): GizWaySnapshot {
  const models = region === "global"
    ? [
        { id: "openai/gpt-5", name: "GPT-5", provider: "OpenAI", family: "chat" as const, description: "Reasoning, coding and tool use for demanding work.", rates: [{ metric: "Input", unit: "1M tokens", credits: 1250 }, { metric: "Output", unit: "1M tokens", credits: 10000 }], latency: "Balanced", context: "400K", accent: "#111827" },
        { id: "anthropic/claude-sonnet-4", name: "Claude Sonnet 4", provider: "Anthropic", family: "chat" as const, description: "Fast, thoughtful responses with excellent long-context work.", rates: [{ metric: "Input", unit: "1M tokens", credits: 3000 }, { metric: "Output", unit: "1M tokens", credits: 15000 }], latency: "Fast", context: "200K", accent: "#c26a45" },
        { id: "google/gemini-2.5-flash", name: "Gemini 2.5 Flash", provider: "Google", family: "chat" as const, description: "Low-latency multimodal intelligence for high-volume apps.", rates: [{ metric: "Input", unit: "1M tokens", credits: 300 }, { metric: "Output", unit: "1M tokens", credits: 2500 }], latency: "Very fast", context: "1M", accent: "#3979f6" },
        { id: "google/veo-3", name: "Veo 3", provider: "Google", family: "video" as const, description: "High-fidelity video generation with native audio.", rates: [{ metric: "Input", unit: "1M tokens", credits: 8000 }, { metric: "Output", unit: "1M tokens", credits: 16000 }], latency: "Generation", context: "Media", accent: "#7c3aed" },
      ]
    : [
        { id: "deepseek/deepseek-v3.1", name: "DeepSeek V3.1", provider: "DeepSeek", family: "chat" as const, description: "High-value reasoning and coding for production workloads.", rates: [{ metric: "Input", unit: "1M tokens", credits: 270 }, { metric: "Output", unit: "1M tokens", credits: 1100 }], latency: "Fast", context: "128K", accent: "#4268ed" },
        { id: "doubao/seed-1.6", name: "Doubao Seed 1.6", provider: "Volcengine", family: "chat" as const, description: "General multimodal model optimized for Chinese workloads.", rates: [{ metric: "Input", unit: "1M tokens", credits: 220 }, { metric: "Output", unit: "1M tokens", credits: 1200 }], latency: "Very fast", context: "256K", accent: "#635bff" },
        { id: "minimax/speech-02", name: "MiniMax Speech 02", provider: "MiniMax", family: "audio" as const, description: "Natural multilingual speech with expressive voice control.", rates: [{ metric: "Input", unit: "1M tokens", credits: 800 }, { metric: "Output", unit: "1M tokens", credits: 1200 }], latency: "Realtime", context: "Audio", accent: "#ec4899" },
        { id: "kling/video-2.1", name: "Kling 2.1", provider: "Kuaishou", family: "video" as const, description: "Cinematic image-to-video and text-to-video generation.", rates: [{ metric: "Input", unit: "1M tokens", credits: 6500 }, { metric: "Output", unit: "1M tokens", credits: 13000 }], latency: "Generation", context: "Media", accent: "#0f766e" },
      ];
  const snapshot = structuredClone({ region, models, ...common });
  if (scenario === "new-user") {
    snapshot.usage = usage.map((point) => ({ ...point, credits: 0 }));
    snapshot.orders = [];
    snapshot.providerKeys = [];
  }
  return snapshot;
}
