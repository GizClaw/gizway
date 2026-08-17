"use client";

import { useEffect, useId, useRef, useState } from "react";
import {
  Activity,
  ArrowDownRight,
  ArrowUpRight,
  Bot,
  Box,
  Check,
  ChevronDown,
  CircleDollarSign,
  Copy,
  CreditCard,
  Eye,
  EyeOff,
  KeyRound,
  LayoutDashboard,
  LogOut,
  Menu,
  MessageSquareText,
  Plus,
  Search,
  Settings as SettingsIcon,
  Sparkles,
  TerminalSquare,
  WalletCards,
  X,
  Zap,
} from "lucide-react";
import { Badge } from "@/components/ui/badge";
import { Button } from "@/components/ui/button";
import { Card, CardContent, CardDescription, CardHeader, CardTitle } from "@/components/ui/card";
import type { GizPaySnapshot, SubscriptionKey } from "@/data/contracts/gizpay";
import type { GizWaySnapshot, Model, ProviderKey, ProviderKeyDraft, ProviderPrice, Region } from "@/data/contracts/gizway";
import { createPrototypeDataProvider, type PrototypeDataProvider } from "@/data/provider";
import type { ServiceSyncStates } from "@/data/provider";
import { readDashboardSnapshots } from "@/data/dashboard-snapshot";
import { fakeScenarios, type FakeScenario } from "@/data/fake/scenarios";
import { cn } from "@/lib/utils";

type Section = "overview" | "models" | "usage" | "keys" | "providers" | "credits" | "settings";

const navItems: Array<{ id: Section; label: string; icon: typeof LayoutDashboard }> = [
  { id: "overview", label: "Overview", icon: LayoutDashboard },
  { id: "models", label: "Models", icon: Box },
  { id: "usage", label: "Usage", icon: Activity },
  { id: "keys", label: "Subscription Keys", icon: KeyRound },
  { id: "providers", label: "Provider Keys", icon: TerminalSquare },
  { id: "credits", label: "Credits", icon: WalletCards },
  { id: "settings", label: "Settings", icon: SettingsIcon },
];

function credits(value: number) {
  return new Intl.NumberFormat("en-US").format(value);
}

function timestamp(value?: string) {
  if (!value) return "Not yet synced";
  const date = new Date(value);
  return Number.isNaN(date.valueOf()) ? value : date.toLocaleString();
}

const dialogFocusable = [
  "button:not([disabled])",
  "input:not([disabled])",
  "select:not([disabled])",
  "textarea:not([disabled])",
  "a[href]",
  "[tabindex]:not([tabindex='-1'])",
].join(",");

function useDialogFocus(onClose: () => void) {
  const id = useId();
  const close = useRef(onClose);

  useEffect(() => {
    close.current = onClose;
  }, [onClose]);

  useEffect(() => {
    const previous = document.activeElement instanceof HTMLElement ? document.activeElement : undefined;
    const dialog = document.getElementById(id);
    const preferred = dialog?.querySelector<HTMLElement>("[data-autofocus]");
    const first = dialog?.querySelector<HTMLElement>(dialogFocusable);
    (preferred ?? first ?? dialog)?.focus();
    return () => {
      if (previous?.isConnected) previous.focus();
    };
  }, [id]);

  function onKeyDown(event: React.KeyboardEvent<HTMLDivElement>) {
    if (event.key === "Escape") {
      event.preventDefault();
      close.current();
      return;
    }
    if (event.key !== "Tab") return;
    const dialog = document.getElementById(id);
    const focusable = dialog ? Array.from(dialog.querySelectorAll<HTMLElement>(dialogFocusable)) : [];
    if (focusable.length === 0) {
      event.preventDefault();
      dialog?.focus();
      return;
    }
    const first = focusable[0];
    const last = focusable[focusable.length - 1];
    if (event.shiftKey && document.activeElement === first) {
      event.preventDefault();
      last.focus();
    } else if (!event.shiftKey && document.activeElement === last) {
      event.preventDefault();
      first.focus();
    }
  }

  return { id, onKeyDown };
}

export function GizWayDashboard({ siteRegion, dataProvider, showScenarioSelector = true, onLogout }: { siteRegion: Region; dataProvider?: PrototypeDataProvider; showScenarioSelector?: boolean; onLogout?: () => void }) {
  const [section, setSection] = useState<Section>("overview");
  const [scenario, setScenario] = useState<FakeScenario>("active-payg");
  const [pay, setPay] = useState<GizPaySnapshot | null>(null);
  const [way, setWay] = useState<GizWaySnapshot | null>(null);
  const [loadError, setLoadError] = useState("");
  const [queryErrors, setQueryErrors] = useState<Partial<Record<"gizpay" | "gizway", string>>>({});
  const [provider, setProvider] = useState<PrototypeDataProvider | null>(dataProvider ?? null);
  const [topUpOpen, setTopUpOpen] = useState(false);
  const [newKeyOpen, setNewKeyOpen] = useState(false);
  const [providerKeyDialog, setProviderKeyDialog] = useState<{ mode: "create" } | { mode: "prices"; key: ProviderKey }>();
  const [commandPending, setCommandPending] = useState(false);
  const [commandError, setCommandError] = useState("");
  const [syncStates, setSyncStates] = useState<ServiceSyncStates>({ gizpay: "first_sync", gizway: "first_sync" });
  const [initialized, setInitialized] = useState(false);

  async function command(action: () => Promise<void>) {
    setCommandPending(true);
    setCommandError("");
    try { await action(); } catch (reason) { setCommandError(reason instanceof Error ? reason.message : "Command failed"); } finally { setCommandPending(false); }
  }
  const [mobileNav, setMobileNav] = useState(false);

	useEffect(() => {
		let cancelled = false;
		let unsubscribe: (() => void) | undefined;
		const load = dataProvider ? Promise.resolve(dataProvider) : createPrototypeDataProvider(scenario, siteRegion);
		void load.then(async (nextProvider) => {
			if (!cancelled) setLoadError("");
			let refreshing = false;
			let refreshAgain = false;
			const refresh = async () => {
				if (refreshing) {
					refreshAgain = true;
					return;
				}
				refreshing = true;
				try {
					do {
						refreshAgain = false;
						const snapshots = await readDashboardSnapshots(nextProvider, siteRegion);
						if (cancelled) return;
						const states = { ...(nextProvider.syncStates?.() ?? { gizpay: "ready", gizway: "ready" }) };
						if (snapshots.errors.gizpay) states.gizpay = "sync_error";
						if (snapshots.errors.gizway) states.gizway = "sync_error";
						setSyncStates(states);
						setQueryErrors(snapshots.errors);
						if (snapshots.pay) setPay(snapshots.pay);
						if (snapshots.way) setWay(snapshots.way);
						setInitialized(true);
					} while (refreshAgain);
				} finally {
					refreshing = false;
				}
			};
			await refresh();
			if (cancelled) return;
      setProvider(nextProvider);
			unsubscribe = nextProvider.subscribe?.(() => { void refresh().catch((reason: unknown) => {
				if (!cancelled) setLoadError(reason instanceof Error ? reason.message : "Failed to refresh local PowerSync data");
			}); });
    }).catch((reason: unknown) => {
      console.error("Failed to open local PowerSync databases", reason);
      if (!cancelled) setLoadError(reason instanceof Error ? reason.message : "Failed to open local PowerSync databases");
    });
    return () => { cancelled = true; unsubscribe?.(); };
  }, [scenario, siteRegion, dataProvider]);

  async function handleTopUp(amount: number) {
    if (!provider) return;
    await command(async () => {
      setPay(await provider.gizpay.topUp(amount));
      setTopUpOpen(false);
    });
  }

  async function handleNewKey(name: string) {
    if (!provider) return;
    await command(async () => {
      setPay(await provider.gizpay.createSubscriptionKey(name));
      setNewKeyOpen(false);
    });
  }

  async function handleRevokeKey(id: string) {
    if (!provider) return;
    await command(async () => setPay(await provider.gizpay.revokeSubscriptionKey(id)));
  }

  async function handleCreateProviderKey(draft: ProviderKeyDraft) {
    if (!provider) return;
    await command(async () => {
      setWay(await provider.gizway.createProviderKey(siteRegion, draft));
      setProviderKeyDialog(undefined);
    });
  }

  async function handleProviderPrices(id: string, prices: ProviderPrice[]) {
    if (!provider) return;
    await command(async () => {
      setWay(await provider.gizway.updateProviderKeyPrices(siteRegion, id, prices));
      setProviderKeyDialog(undefined);
    });
  }

  async function handleDisableProviderKey(id: string) {
    if (!provider) return;
    await command(async () => setWay(await provider.gizway.disableProviderKey(siteRegion, id)));
  }

  if (loadError) {
    return <div className="grid min-h-screen place-items-center bg-app p-6"><div role="alert" className="max-w-lg rounded-xl border border-red-200 bg-red-50 px-5 py-4 text-sm text-red-900">{loadError}</div></div>;
  }
  if (!initialized) {
    return <div className="grid min-h-screen place-items-center bg-app text-sm text-muted-foreground">Opening local PowerSync databases…</div>;
  }

  const displayPay = pay ?? unavailableGizPaySnapshot();
  const baseWay = way ?? unavailableGizWaySnapshot(siteRegion);
  const keyNames = new Map(displayPay.keys.map((key) => [key.id, key.name]));
  const displayWay = { ...baseWay, orders: baseWay.orders.map((order) => ({ ...order, keyName: keyNames.get(order.keyName) ?? order.keyName })) };
  const paygProduct = displayPay.products.find((product) => product.billing === "pay_as_you_go" && product.active);
  const activePAYGSubscription = displayPay.subscriptions.some((subscription) => subscription.productId === paygProduct?.id && subscription.status === "active");

  return (
    <div className="min-h-screen bg-app text-foreground">
      <span className="sr-only" data-testid="gizpay-sync-state" data-state={syncStates.gizpay} />
      <span className="sr-only" data-testid="gizway-sync-state" data-state={syncStates.gizway} />
      <span className="sr-only" data-testid="regional-model-count">{displayWay.models.length}</span>
      <span className="sr-only" data-testid="regional-price-count">{displayWay.models.reduce((count, model) => count + model.rates.length, 0)}</span>
      <Sidebar section={section} setSection={setSection} user={displayPay.user} onLogout={onLogout} className="hidden lg:flex" />

      {mobileNav && (
        <div className="fixed inset-0 z-50 flex lg:hidden">
          <button className="absolute inset-y-0 left-[248px] right-0 bg-black/30" aria-label="Close navigation" onClick={() => setMobileNav(false)} />
          <Sidebar section={section} setSection={(value) => { setSection(value); setMobileNav(false); }} user={displayPay.user} onLogout={onLogout} className="relative z-10 flex" />
        </div>
      )}

      <div className="lg:pl-[248px]">
        <header className="sticky top-0 z-30 flex h-16 items-center justify-between border-b border-border/80 bg-app/92 px-4 backdrop-blur-xl sm:px-6 lg:px-8">
          <div className="flex items-center gap-3">
            <Button variant="ghost" size="icon" className="lg:hidden" onClick={() => setMobileNav(true)} aria-label="Open navigation">
              <Menu className="size-5" />
            </Button>
            <div className="relative hidden sm:block">
              <Search className="absolute left-3 top-1/2 size-4 -translate-y-1/2 text-muted-foreground" />
              <input className="h-9 w-72 rounded-lg border border-border bg-card pl-9 pr-3 text-sm outline-none placeholder:text-muted-foreground focus:border-brand/50 focus:ring-2 focus:ring-brand/10" placeholder="Search models, usage, keys…" />
            </div>
          </div>

          <div className="flex items-center gap-2">
            {showScenarioSelector && <label className="hidden items-center gap-2 rounded-lg border border-border bg-card px-2.5 py-1.5 text-xs text-muted-foreground xl:flex">
              <span>PowerSync local</span>
              <select value={scenario} onChange={(event) => setScenario(event.target.value as FakeScenario)} className="bg-transparent font-medium text-foreground outline-none">
                {fakeScenarios.map((item) => <option key={item.id} value={item.id}>{item.label}</option>)}
              </select>
            </label>}
            <Button variant="outline" size="sm" className="hidden sm:inline-flex" onClick={() => setTopUpOpen(true)}>
              <CreditCard className="size-3.5" /> Top up
            </Button>
            <div className="ml-1 grid size-8 place-items-center rounded-full bg-[#17231d] text-xs font-semibold text-white">ID</div>
          </div>
        </header>

        <main className="mx-auto max-w-[1500px] px-4 py-6 sm:px-6 lg:px-8 lg:py-8">
          {syncStates.gizpay !== "ready" && <ServiceState service="GizPay" state={syncStates.gizpay} />}
          {syncStates.gizway !== "ready" && <ServiceState service="Regional GizWay" state={syncStates.gizway} />}
          {queryErrors.gizpay && <span className="sr-only" data-testid="gizpay-query-error">{queryErrors.gizpay}</span>}
          {queryErrors.gizway && <span className="sr-only" data-testid="gizway-query-error">{queryErrors.gizway}</span>}
          {commandPending && <div role="status" data-testid="runtime-state" data-state="command_pending" className="mb-4 rounded-xl border border-brand/20 bg-brand-soft px-4 py-3 text-sm">Saving and waiting for server confirmation…</div>}
          {commandError && <div role="alert" data-testid="runtime-state" data-state="command_failed" className="mb-4 rounded-xl border border-red-200 bg-red-50 px-4 py-3 text-sm text-red-900">{commandError}</div>}
          {section === "overview" && <Overview pay={displayPay} way={displayWay} syncStates={syncStates} setSection={setSection} onTopUp={() => setTopUpOpen(true)} />}
          {section === "models" && <Models models={displayWay.models} />}
          {section === "usage" && <Usage way={displayWay} />}
          {section === "keys" && <SubscriptionKeys keys={displayPay.keys} activeSubscription={activePAYGSubscription} onCreate={() => setNewKeyOpen(true)} onRevoke={(id) => void handleRevokeKey(id)} />}
          {section === "providers" && <ProviderKeys way={displayWay} merchantId={displayPay.user.merchantId} onAdd={() => setProviderKeyDialog({ mode: "create" })} onConfigure={(key) => setProviderKeyDialog({ mode: "prices", key })} onDisable={(id) => void handleDisableProviderKey(id)} />}
          {section === "credits" && <Credits pay={displayPay} onTopUp={() => setTopUpOpen(true)} />}
          {section === "settings" && <Settings pay={displayPay} onLogout={onLogout} />}
        </main>
      </div>

      {topUpOpen && <TopUpDialog onClose={() => setTopUpOpen(false)} onTopUp={handleTopUp} />}
      {newKeyOpen && <NewKeyDialog onClose={() => setNewKeyOpen(false)} onCreate={(name) => void handleNewKey(name)} />}
      {providerKeyDialog && <ProviderKeyDialog mode={providerKeyDialog} models={displayWay.models} onClose={() => setProviderKeyDialog(undefined)} onCreate={(draft) => void handleCreateProviderKey(draft)} onUpdate={(id, prices) => void handleProviderPrices(id, prices)} />}
    </div>
  );
}

function unavailableGizPaySnapshot(): GizPaySnapshot {
  return {
    user: { id: "", name: "User", email: "", merchantId: "", merchantName: "Unavailable", merchantStatus: "unavailable" },
    balance: { available: 0, currency: "GIZ_CREDIT", updatedAt: "" },
    products: [], subscriptions: [], keys: [], ledger: [], topUps: [], charges: [], commissions: [],
  };
}

function unavailableGizWaySnapshot(region: Region): GizWaySnapshot {
  return { region, models: [], usage: [], orders: [], providerKeys: [] };
}

function ServiceState({ service, state }: { service: string; state: ServiceSyncStates["gizpay"] }) {
  return <div role="status" data-service={service} data-state={state} className="mb-4 rounded-xl border border-amber-200 bg-amber-50 px-4 py-3 text-sm text-amber-900"><strong>{service}</strong> is {state.replaceAll("_", " ")}. Locally cached data may be incomplete or stale.</div>;
}

function Sidebar({ section, setSection, user, onLogout, className }: {
  section: Section;
  setSection: (value: Section) => void;
  user: GizPaySnapshot["user"];
  onLogout?: () => void;
  className?: string;
}) {
  return (
    <aside className={cn("fixed inset-y-0 left-0 z-40 w-[248px] flex-col border-r border-border bg-sidebar", className)}>
      <div className="flex h-16 items-center gap-2.5 border-b border-border px-5">
        <div className="grid size-8 place-items-center rounded-xl bg-brand text-white shadow-sm">
          <Zap className="size-[17px] fill-current" />
        </div>
        <div className="leading-tight">
          <div className="text-[15px] font-bold tracking-[-0.02em]">GizWay</div>
          <div className="text-[10px] font-medium uppercase tracking-[0.16em] text-muted-foreground">AI network</div>
        </div>
      </div>
      <nav className="flex-1 space-y-1 px-3 py-5">
        <p className="mb-2 px-3 text-[10px] font-semibold uppercase tracking-[0.13em] text-muted-foreground/80">Workspace</p>
        {navItems.map((item) => {
          const Icon = item.icon;
          const active = section === item.id;
          return (
            <button key={item.id} onClick={() => setSection(item.id)} className={cn("flex h-10 w-full items-center gap-3 rounded-lg px-3 text-left text-sm font-medium transition-colors", active ? "bg-sidebar-active text-foreground" : "text-muted-foreground hover:bg-muted hover:text-foreground")}>
              <Icon className={cn("size-[17px]", active && "text-brand")} />
              {item.label}
            </button>
          );
        })}
      </nav>
      <div className="border-t border-border p-3">
        <div className="flex w-full items-center gap-3 rounded-xl px-3 py-2.5 text-left">
          <div className="grid size-9 place-items-center rounded-full bg-[#17231d] text-xs font-semibold text-white">ID</div>
          <div className="min-w-0 flex-1">
            <div className="truncate text-sm font-semibold">{user.name}</div>
            <div className="truncate text-xs text-muted-foreground">{user.email}</div>
          </div>
          {onLogout && <button type="button" aria-label="Sign out" title="Sign out" onClick={onLogout} className="grid size-8 place-items-center rounded-lg text-muted-foreground hover:bg-muted hover:text-foreground"><LogOut className="size-4" /></button>}
        </div>
      </div>
    </aside>
  );
}

function PageHeading({ eyebrow, title, description, action }: { eyebrow?: string; title: string; description: string; action?: React.ReactNode }) {
  return (
    <div className="mb-6 flex flex-col justify-between gap-4 sm:flex-row sm:items-end">
      <div>
        {eyebrow && <div className="mb-1.5 text-xs font-semibold uppercase tracking-[0.12em] text-brand">{eyebrow}</div>}
        <h1 className="text-2xl font-bold tracking-[-0.03em] sm:text-[30px]">{title}</h1>
        <p className="mt-1.5 max-w-2xl text-sm leading-6 text-muted-foreground">{description}</p>
      </div>
      {action}
    </div>
  );
}

function Overview({ pay, way, syncStates, setSection, onTopUp }: { pay: GizPaySnapshot; way: GizWaySnapshot; syncStates: ServiceSyncStates; setSection: (value: Section) => void; onTopUp: () => void }) {
  const used = way.usage.reduce((sum, point) => sum + point.credits, 0);
  const max = Math.max(1, ...way.usage.map((point) => point.credits));
  const providerEarnings = way.providerKeys.reduce((sum, key) => sum + key.earnedCredits, 0);
  const paygProduct = pay.products.find((product) => product.billing === "pay_as_you_go" && product.active);
  const paygSubscription = pay.subscriptions.find((subscription) => subscription.productId === paygProduct?.id);

  return (
    <>
      <PageHeading eyebrow="AI workspace" title={`Good afternoon, ${pay.user.name}`} description="Your AI usage, credits and provider earnings in one place." action={<Button variant="primary" onClick={onTopUp}><CreditCard className="size-4" /> Add credit</Button>} />

      {paygSubscription?.status === "active" && pay.keys.length === 0 && (
        <Card className="mb-5 overflow-hidden border-brand/20 bg-brand-soft">
          <CardContent className="flex flex-col justify-between gap-4 py-5 sm:flex-row sm:items-center">
            <div>
              <div className="flex items-center gap-2 text-sm font-semibold"><Sparkles className="size-4 text-brand" /> Your PAYG subscription is ready</div>
              <p className="mt-1 text-sm text-muted-foreground">Create a Subscription Key whenever you want to start using GizWay models.</p>
            </div>
            <Button variant="primary" onClick={() => setSection("keys")}>Create a Key</Button>
          </CardContent>
        </Card>
      )}

      <div className="grid gap-4 md:grid-cols-2 xl:grid-cols-4">
        <MetricCard label="Available credit" value={credits(pay.balance.available)} sub="GIZ Credit" icon={WalletCards} tone="green" action={<button className="text-xs font-semibold text-brand" onClick={onTopUp}>Add credit</button>} />
        <MetricCard label="AI spend · recent" value={credits(used)} sub="From regional AI Orders" icon={Activity} tone="blue" />
        <MetricCard label="Provider earnings" value={credits(providerEarnings)} sub="Settled commissions" icon={ArrowDownRight} tone="purple" />
        <MetricCard label="Active keys" value={String(pay.keys.filter((key) => key.status === "active").length)} sub={`Across ${pay.subscriptions.length} subscription${pay.subscriptions.length === 1 ? "" : "s"}`} icon={KeyRound} tone="amber" />
      </div>

      <div className="mt-5 grid gap-4 md:grid-cols-3">
        <StatusCard title="PAYG Subscription" value={paygSubscription?.status ?? "missing"} detail={paygProduct?.name ?? "No active PAYG Product"} testID="payg-status" />
        <StatusCard title="GizPay PowerSync" value={syncStates.gizpay} detail={`Balance updated ${timestamp(pay.balance.updatedAt)}`} testID="overview-gizpay-sync" />
        <StatusCard title="GizWay PowerSync" value={syncStates.gizway} detail="Regional Models, Usage and Orders" testID="overview-gizway-sync" />
      </div>

      <div className="mt-5 grid gap-5 xl:grid-cols-[1.55fr_1fr]">
        <Card>
          <CardHeader className="flex-row items-start justify-between">
            <div><CardTitle>Credit activity</CardTitle><CardDescription>AI spend during the last seven days</CardDescription></div>
            <button className="flex items-center gap-1 text-xs font-medium text-muted-foreground">Last 7 days <ChevronDown className="size-3.5" /></button>
          </CardHeader>
          <CardContent>
            <div className="flex h-52 items-end gap-2.5 sm:gap-4">
              {way.usage.map((point, index) => (
                <div key={point.day} className="flex h-full flex-1 flex-col justify-end gap-2">
                  <div className="group relative flex h-[170px] items-end rounded-lg bg-muted/60 p-1">
                    <div className={cn("w-full rounded-md transition-all", index === way.usage.length - 1 ? "bg-brand" : "bg-chart")} style={{ height: `${Math.max(16, (point.credits / max) * 100)}%` }} />
                    <div className="pointer-events-none absolute -top-7 left-1/2 hidden -translate-x-1/2 rounded bg-foreground px-2 py-1 text-[10px] text-background group-hover:block">{credits(point.credits)}</div>
                  </div>
                  <span className="text-center text-[11px] font-medium text-muted-foreground">{point.day}</span>
                </div>
              ))}
            </div>
          </CardContent>
        </Card>

        <Card>
          <CardHeader className="flex-row items-start justify-between">
            <div><CardTitle>Recent activity</CardTitle><CardDescription>Credits moving through your account</CardDescription></div>
            <button onClick={() => setSection("credits")} className="text-xs font-semibold text-brand">View all</button>
          </CardHeader>
          <CardContent className="space-y-1">
            {pay.ledger.slice(0, 4).map((item) => (
              <div key={item.id} className="flex items-center gap-3 rounded-xl px-1 py-2.5">
                <div className={cn("grid size-9 place-items-center rounded-full", item.amount > 0 ? "bg-emerald-50 text-emerald-700" : "bg-muted text-muted-foreground")}>
                  {item.amount > 0 ? <ArrowDownRight className="size-4" /> : <ArrowUpRight className="size-4" />}
                </div>
                <div className="min-w-0 flex-1"><div className="truncate text-sm font-medium">{item.label}</div><div className="text-xs text-muted-foreground">{item.createdAt}</div></div>
                <div className={cn("text-sm font-semibold tabular-nums", item.amount > 0 ? "text-emerald-700" : "text-foreground")}>{item.amount > 0 ? "+" : ""}{credits(item.amount)}</div>
              </div>
            ))}
          </CardContent>
        </Card>
      </div>

      <Card className="mt-5 overflow-hidden">
        <CardHeader className="flex-row items-start justify-between"><div><CardTitle>Recent AI Orders</CardTitle><CardDescription>Latest regional requests and final Credit cost</CardDescription></div><button onClick={() => setSection("usage")} className="text-xs font-semibold text-brand">View Usage</button></CardHeader>
        <div className="overflow-x-auto"><table className="w-full min-w-[680px] text-left"><thead className="border-y border-border bg-muted/40 text-[11px] uppercase tracking-[0.08em] text-muted-foreground"><tr><th className="px-5 py-3">Order</th><th className="px-5 py-3">Model</th><th className="px-5 py-3">Subscription Key</th><th className="px-5 py-3">Credit</th><th className="px-5 py-3">Status</th></tr></thead><tbody>{way.orders.slice(-5).reverse().map((order) => <tr key={order.id} className="border-b border-border last:border-0"><td className="px-5 py-3"><div className="font-mono text-xs">{order.id}</div><div className="mt-1 text-xs text-muted-foreground">{timestamp(order.createdAt)}</div></td><td className="px-5 py-3 text-sm font-medium">{order.model}</td><td className="px-5 py-3 text-sm">{order.keyName}</td><td className="px-5 py-3 text-sm font-semibold">{credits(order.credits)}</td><td className="px-5 py-3"><Badge tone={order.status === "completed" ? "green" : order.status === "pending" ? "amber" : "neutral"}>{order.status}</Badge></td></tr>)}</tbody></table></div>
        {way.orders.length === 0 && <div className="px-5 py-10 text-center text-sm text-muted-foreground">No AI Orders in this region yet.</div>}
      </Card>

      <div className="mt-5 grid gap-5 xl:grid-cols-[1.55fr_1fr]">
        <Card>
          <CardHeader className="flex-row items-start justify-between">
            <div><CardTitle>Popular models</CardTitle><CardDescription>Ready to use with your PAYG subscription</CardDescription></div>
            <button onClick={() => setSection("models")} className="text-xs font-semibold text-brand">Explore catalog</button>
          </CardHeader>
          <CardContent className="grid gap-3 sm:grid-cols-2">
            {way.models.slice(0, 4).map((model) => <CompactModel key={model.id} model={model} />)}
          </CardContent>
        </Card>
        <Card className="overflow-hidden bg-[#17231d] text-white">
          <CardContent className="relative flex h-full min-h-56 flex-col justify-between py-6">
            <div className="absolute right-0 top-0 size-36 translate-x-10 -translate-y-10 rounded-full border-[28px] border-white/5" />
            <div>
              <Badge className="border-white/10 bg-white/10 text-white">GizWay Network</Badge>
              <h3 className="mt-4 max-w-xs text-xl font-semibold leading-7 tracking-[-0.02em]">Turn your unused provider capacity into AI credit.</h3>
              <p className="mt-2 max-w-sm text-sm leading-6 text-white/60">Add a Provider Key, set your cost, and earn credits whenever GizWay routes traffic to you.</p>
            </div>
            <Button className="mt-5 w-fit bg-white text-[#17231d] hover:bg-white/90" onClick={() => setSection("providers")}>Add Provider Key</Button>
          </CardContent>
        </Card>
      </div>
    </>
  );
}

function MetricCard({ label, value, sub, icon: Icon, trend, tone, action }: { label: string; value: string; sub: string; icon: typeof Activity; trend?: string; tone: "green" | "blue" | "purple" | "amber"; action?: React.ReactNode }) {
  const tones = { green: "bg-emerald-50 text-emerald-700", blue: "bg-blue-50 text-blue-700", purple: "bg-violet-50 text-violet-700", amber: "bg-amber-50 text-amber-700" };
  return (
    <Card>
      <CardContent className="pt-5">
        <div className="flex items-start justify-between"><div className={cn("grid size-9 place-items-center rounded-xl", tones[tone])}><Icon className="size-[17px]" /></div>{trend && <Badge tone="green"><ArrowUpRight className="mr-1 size-3" />{trend}</Badge>}</div>
        <div className="mt-5 text-[26px] font-bold tracking-[-0.035em] tabular-nums">{value}</div>
        <div className="mt-1 flex items-center justify-between gap-2"><span className="text-xs text-muted-foreground"><strong className="font-medium text-foreground">{label}</strong> · {sub}</span>{action}</div>
      </CardContent>
    </Card>
  );
}

function StatusCard({ title, value, detail, testID }: { title: string; value: string; detail: string; testID: string }) {
  return <Card><CardContent className="pt-5"><div className="flex items-center justify-between"><div className="text-sm font-semibold">{title}</div><Badge tone={value === "active" || value === "ready" ? "green" : "neutral"}>{value}</Badge></div><div data-testid={testID} data-state={value} className="mt-3 text-xs text-muted-foreground">{detail}</div></CardContent></Card>;
}

function CompactModel({ model }: { model: Model }) {
  return (
    <div className="flex items-center gap-3 rounded-xl border border-border p-3.5 transition-colors hover:border-brand/30 hover:bg-muted/30">
      <div className="grid size-10 place-items-center rounded-xl text-sm font-bold text-white" style={{ background: model.accent }}>{model.name.slice(0, 1)}</div>
      <div className="min-w-0 flex-1"><div className="truncate text-sm font-semibold">{model.name}</div><div className="text-xs text-muted-foreground">{model.provider} · {model.context}</div></div>
      <Badge tone={model.family === "video" ? "purple" : "neutral"}>{model.family}</Badge>
    </div>
  );
}

function Models({ models }: { models: Model[] }) {
  const [search, setSearch] = useState("");
  const visible = models.filter((model) => `${model.name} ${model.provider} ${model.family}`.toLowerCase().includes(search.toLowerCase()));
  return (
    <>
      <PageHeading eyebrow="Model catalog" title="Models" description="Compare available models and the GIZ Credit charged for each billing metric." action={<div className="relative"><Search className="absolute left-3 top-1/2 size-4 -translate-y-1/2 text-muted-foreground" /><input value={search} onChange={(event) => setSearch(event.target.value)} className="h-10 w-64 rounded-lg border border-border bg-card pl-9 pr-3 text-sm outline-none focus:border-brand/40" placeholder="Search models" /></div>} />
      <div className="grid gap-4 md:grid-cols-2 xl:grid-cols-3">
        {visible.map((model) => (
          <Card key={model.id} className="group transition-all hover:-translate-y-0.5 hover:border-brand/25 hover:shadow-lg">
            <CardContent className="pt-5">
              <div className="flex items-start justify-between"><div className="grid size-11 place-items-center rounded-xl text-base font-bold text-white" style={{ background: model.accent }}>{model.name[0]}</div><Badge tone={model.family === "video" ? "purple" : model.family === "audio" ? "amber" : "neutral"}>{model.family}</Badge></div>
              <h3 className="mt-5 text-lg font-semibold tracking-[-0.02em]">{model.name}</h3>
              <div className="mt-0.5 text-xs font-medium text-muted-foreground">{model.provider} · {model.id}</div>
              <p className="mt-3 min-h-12 text-sm leading-6 text-muted-foreground">{model.description}</p>
              <div className="mt-5 grid grid-cols-3 gap-2 border-t border-border pt-4">
                <ModelStat label={model.rates[0] ? `${model.rates[0].metric} · ${model.rates[0].unit}` : "Rate"} value={model.rates[0] ? `${credits(model.rates[0].credits)} Credit` : "—"} />
                <ModelStat label={model.rates[1] ? `${model.rates[1].metric} · ${model.rates[1].unit}` : "Additional rate"} value={model.rates[1] ? `${credits(model.rates[1].credits)} Credit` : "—"} />
                <ModelStat label="Context" value={model.context} />
              </div>
            </CardContent>
          </Card>
        ))}
      </div>
    </>
  );
}

function ModelStat({ label, value }: { label: string; value: string }) {
  return <div><div className="text-[10px] text-muted-foreground">{label}</div><div className="mt-1 text-xs font-semibold">{value}</div></div>;
}

function Usage({ way }: { way: GizWaySnapshot }) {
  const [timeRange, setTimeRange] = useState("all");
  const [model, setModel] = useState("all");
  const [expanded, setExpanded] = useState<Record<string, boolean>>({});
  const [openedAt] = useState(() => Date.now());
  const cutoff = timeRange === "24h" ? openedAt - 86_400_000 : timeRange === "7d" ? openedAt - 7 * 86_400_000 : timeRange === "30d" ? openedAt - 30 * 86_400_000 : 0;
  const visible = way.orders.filter((order) => (model === "all" || order.model === model) && (cutoff === 0 || new Date(order.createdAt).valueOf() >= cutoff));
  const total = visible.reduce((sum, order) => sum + order.credits, 0);
  const models = [...new Set(way.orders.map((order) => order.model))].sort();
  return (
    <>
      <PageHeading eyebrow="AI consumption" title="Usage" description="Understand every request, model and Credit consumed in this regional workspace." action={<div className="flex gap-2"><label className="text-xs font-semibold text-muted-foreground">Time range<select aria-label="Time range" value={timeRange} onChange={(event) => setTimeRange(event.target.value)} className="ml-2 h-10 rounded-lg border border-border bg-card px-3 text-sm font-medium text-foreground"><option value="all">All time</option><option value="24h">Last 24 hours</option><option value="7d">Last 7 days</option><option value="30d">Last 30 days</option></select></label><label className="text-xs font-semibold text-muted-foreground">Model<select aria-label="Model filter" value={model} onChange={(event) => setModel(event.target.value)} className="ml-2 h-10 rounded-lg border border-border bg-card px-3 text-sm font-medium text-foreground"><option value="all">All models</option>{models.map((name) => <option key={name} value={name}>{name}</option>)}</select></label></div>} />
      <div className="grid gap-4 sm:grid-cols-3"><MetricCard label="Total credits" value={credits(total)} sub="Filtered AI Orders" icon={CircleDollarSign} tone="green" /><MetricCard label="Model requests" value={String(visible.length)} sub="Filtered period" icon={MessageSquareText} tone="blue" /><MetricCard label="Total tokens" value={credits(visible.reduce((sum, order) => sum + order.tokens, 0))} sub="Input and output" icon={Bot} tone="purple" /></div>
      <Card className="mt-5 overflow-hidden">
        <CardHeader><CardTitle>AI Orders</CardTitle><CardDescription>Expand an Order to inspect each normalized billing Metric</CardDescription></CardHeader>
        <div className="overflow-x-auto"><table className="w-full min-w-[780px] text-left"><thead className="border-y border-border bg-muted/40 text-[11px] uppercase tracking-[0.08em] text-muted-foreground"><tr><th className="px-5 py-3 font-semibold">Order</th><th className="px-5 py-3 font-semibold">Model</th><th className="px-5 py-3 font-semibold">Subscription Key</th><th className="px-5 py-3 font-semibold">Tokens</th><th className="px-5 py-3 font-semibold">Credits</th><th className="px-5 py-3 font-semibold">Status</th><th /></tr></thead>{visible.map((order) => <tbody key={order.id} className="border-b border-border last:border-0"><tr><td className="px-5 py-4"><div className="font-mono text-xs font-medium">{order.id}</div><div className="mt-1 text-xs text-muted-foreground">{timestamp(order.createdAt)}</div></td><td className="px-5 py-4 text-sm font-medium">{order.model}</td><td className="px-5 py-4 text-sm text-muted-foreground">{order.keyName}</td><td className="px-5 py-4 text-sm tabular-nums">{credits(order.tokens)}</td><td className="px-5 py-4 text-sm font-semibold tabular-nums">{credits(order.credits)}</td><td className="px-5 py-4"><Badge tone={order.status === "completed" ? "green" : order.status === "pending" ? "amber" : "neutral"}>{order.status}</Badge></td><td className="px-5 py-4"><Button variant="ghost" size="sm" aria-expanded={Boolean(expanded[order.id])} onClick={() => setExpanded((state) => ({ ...state, [order.id]: !state[order.id] }))}>{expanded[order.id] ? "Hide metrics" : "Show metrics"}</Button></td></tr>{expanded[order.id] && <tr><td colSpan={7} className="bg-muted/30 px-5 py-4"><div className="grid gap-3 sm:grid-cols-2">{order.metrics.map((metric) => <div key={metric.metric} className="rounded-lg border border-border bg-card px-4 py-3"><div className="text-xs font-semibold text-muted-foreground">{metric.metric}</div><div className="mt-1 text-sm font-semibold tabular-nums">{credits(metric.quantity)} tokens</div></div>)}</div>{order.metrics.length === 0 && <div className="text-sm text-muted-foreground">Metric details are still synchronizing.</div>}</td></tr>}</tbody>)}</table></div>
        {visible.length === 0 && <div className="px-5 py-12 text-center text-sm text-muted-foreground">No AI Orders match these filters.</div>}
      </Card>
    </>
  );
}

function SubscriptionKeys({ keys, activeSubscription, onCreate, onRevoke }: { keys: SubscriptionKey[]; activeSubscription: boolean; onCreate: () => void; onRevoke: (id: string) => void }) {
  const [visible, setVisible] = useState<Record<string, boolean>>({});
  const [copied, setCopied] = useState<string>();
  async function copy(key: SubscriptionKey) {
    await navigator.clipboard?.writeText(key.value);
    setCopied(key.id);
    setTimeout(() => setCopied(undefined), 1200);
  }
  return (
    <>
      <PageHeading eyebrow="Authentication" title="Subscription Keys" description="Create a separate Key for every app, device or AI workflow." action={<Button variant="primary" disabled={!activeSubscription} onClick={onCreate}><Plus className="size-4" /> Create Key</Button>} />
      <Card className="overflow-hidden">
        <div className="overflow-x-auto"><table className="w-full min-w-[760px] text-left"><thead className="border-b border-border bg-muted/40 text-[11px] uppercase tracking-[0.08em] text-muted-foreground"><tr><th className="px-5 py-3 font-semibold">Name</th><th className="px-5 py-3 font-semibold">Key</th><th className="px-5 py-3 font-semibold">Created</th><th className="px-5 py-3 font-semibold">Last used</th><th className="px-5 py-3 font-semibold">Status</th><th /></tr></thead><tbody>{keys.map((key) => <tr key={key.id} className="border-b border-border last:border-0"><td className="px-5 py-4"><div className="text-sm font-semibold">{key.name}</div><div className="mt-1 font-mono text-[11px] text-muted-foreground">{key.id}</div></td><td className="px-5 py-4"><div className="flex items-center gap-2"><code className="w-52 truncate rounded-md bg-muted px-2.5 py-1.5 text-xs">{visible[key.id] ? key.value : `${key.value.slice(0, 9)}••••••••••••`}</code><Button aria-label={`${visible[key.id] ? "Hide" : "Show"} ${key.name}`} variant="ghost" size="icon" onClick={() => setVisible((state) => ({ ...state, [key.id]: !state[key.id] }))}>{visible[key.id] ? <EyeOff className="size-4" /> : <Eye className="size-4" />}</Button><Button aria-label={`Copy ${key.name}`} variant="ghost" size="icon" onClick={() => void copy(key)}>{copied === key.id ? <Check className="size-4 text-emerald-600" /> : <Copy className="size-4" />}</Button></div></td><td className="px-5 py-4 text-sm text-muted-foreground">{new Date(key.createdAt).toLocaleDateString()}</td><td className="px-5 py-4 text-sm text-muted-foreground">{key.lastUsedAt ? new Date(key.lastUsedAt).toLocaleString() : "Never"}</td><td className="px-5 py-4"><Badge tone={key.status === "active" ? "green" : "neutral"}>{key.status}</Badge></td><td className="px-5 py-4">{key.status === "active" && <Button variant="ghost" size="sm" onClick={() => onRevoke(key.id)}>Revoke</Button>}</td></tr>)}</tbody></table></div>
        {keys.length === 0 && <div className="grid place-items-center px-6 py-20 text-center"><div className="grid size-12 place-items-center rounded-2xl bg-muted"><KeyRound className="size-5 text-muted-foreground" /></div><h3 className="mt-4 text-sm font-semibold">No Subscription Keys yet</h3><p className="mt-1 max-w-sm text-sm text-muted-foreground">Activate a PAYG subscription, then create your first key.</p></div>}
      </Card>
      <div className="mt-4 rounded-xl border border-amber-200 bg-amber-50 p-4 text-sm leading-6 text-amber-900"><strong>Keep Keys private.</strong> A Subscription Key can spend credit from the account connected to its subscription.</div>
    </>
  );
}

function NewKeyDialog({ onClose, onCreate }: { onClose: () => void; onCreate: (name: string) => void }) {
  const [name, setName] = useState("");
  const dialog = useDialogFocus(onClose);
  return (
    <div className="fixed inset-0 z-[70] grid place-items-center bg-black/35 p-4 backdrop-blur-sm">
      <button className="absolute inset-0" onClick={onClose} aria-label="Close Subscription Key dialog" />
      <Card id={dialog.id} onKeyDown={dialog.onKeyDown} role="dialog" aria-modal="true" aria-labelledby="new-key-title" tabIndex={-1} className="relative z-10 w-full max-w-md shadow-2xl">
        <CardHeader className="flex-row items-start justify-between"><div><CardTitle id="new-key-title" className="text-lg">Create Subscription Key</CardTitle><CardDescription className="mt-1">Use a recognizable name for the app or device.</CardDescription></div><Button aria-label="Close Subscription Key dialog" variant="ghost" size="icon" onClick={onClose}><X className="size-4" /></Button></CardHeader>
        <CardContent><label className="block text-xs font-semibold text-muted-foreground">Key name<input data-autofocus value={name} onChange={(event) => setName(event.target.value)} className="mt-2 h-11 w-full rounded-lg border border-border px-3 text-sm text-foreground outline-none focus:border-brand/40" /></label><Button variant="primary" className="mt-5 w-full" disabled={name.trim() === ""} onClick={() => onCreate(name.trim())}>Create Key</Button></CardContent>
      </Card>
    </div>
  );
}

function ProviderKeyDialog({ mode, models, onClose, onCreate, onUpdate }: { mode: { mode: "create" } | { mode: "prices"; key: ProviderKey }; models: Model[]; onClose: () => void; onCreate: (draft: ProviderKeyDraft) => void; onUpdate: (id: string, prices: ProviderPrice[]) => void }) {
  const providers = Array.from(new Map(models.map((model) => [model.providerId ?? model.provider, { id: model.providerId ?? model.provider, name: model.provider }])).values());
  const initialProvider = mode.mode === "prices" ? providers.find((provider) => provider.name === mode.key.provider)?.id ?? providers[0]?.id ?? "" : providers[0]?.id ?? "";
  const [providerId, setProviderId] = useState(initialProvider);
  const providerModels = models.filter((model) => (model.providerId ?? model.provider) === providerId);
  const initialModelId = mode.mode === "prices" ? mode.key.prices[0]?.modelId ?? providerModels[0]?.id ?? "" : providerModels[0]?.id ?? "";
  const [modelId, setModelId] = useState(initialModelId);
  const [name, setName] = useState("");
  const [key, setKey] = useState("");
  const existingPrices = mode.mode === "prices" ? mode.key.prices : [];
  const priceFor = (metric: ProviderPrice["metric"], selectedModelId = modelId) => existingPrices.find((price) => price.modelId === selectedModelId && price.metric === metric)?.microcreditsPerUnit ?? 0;
  const [inputPrice, setInputPrice] = useState(() => priceFor("input_tokens", initialModelId));
  const [outputPrice, setOutputPrice] = useState(() => priceFor("output_tokens", initialModelId));
  const selectedPrices: ProviderPrice[] = [
    { modelId, metric: "input_tokens", unitSize: 1_000_000, microcreditsPerUnit: inputPrice },
    { modelId, metric: "output_tokens", unitSize: 1_000_000, microcreditsPerUnit: outputPrice },
  ];
  const prices = [...existingPrices.filter((price) => price.modelId !== modelId), ...selectedPrices];
  const valid = modelId !== "" && inputPrice >= 0 && outputPrice >= 0 && (mode.mode === "prices" || name.trim() !== "" && key.trim() !== "");
  const dialog = useDialogFocus(onClose);
  return (
    <div className="fixed inset-0 z-[70] grid place-items-center overflow-y-auto bg-black/35 p-4 backdrop-blur-sm">
      <button className="absolute inset-0" onClick={onClose} aria-label="Close Provider Key dialog" />
      <Card id={dialog.id} onKeyDown={dialog.onKeyDown} role="dialog" aria-modal="true" aria-labelledby="provider-key-title" tabIndex={-1} className="relative z-10 w-full max-w-lg shadow-2xl">
        <CardHeader className="flex-row items-start justify-between"><div><CardTitle id="provider-key-title" className="text-lg">{mode.mode === "create" ? "Add Provider Key" : "Configure purchase prices"}</CardTitle><CardDescription className="mt-1">Prices use the normalized input_tokens and output_tokens metrics.</CardDescription></div><Button aria-label="Close Provider Key dialog" variant="ghost" size="icon" onClick={onClose}><X className="size-4" /></Button></CardHeader>
        <CardContent className="space-y-4">
          <label className="block text-xs font-semibold text-muted-foreground">Provider<select data-autofocus={mode.mode === "create" ? true : undefined} disabled={mode.mode === "prices"} value={providerId} onChange={(event) => { const next = event.target.value; setProviderId(next); setModelId(models.find((model) => (model.providerId ?? model.provider) === next)?.id ?? ""); }} className="mt-2 h-11 w-full rounded-lg border border-border bg-card px-3 text-sm text-foreground">{providers.map((provider) => <option key={provider.id} value={provider.id}>{provider.name}</option>)}</select></label>
          {mode.mode === "create" && <><label className="block text-xs font-semibold text-muted-foreground">Key name<input value={name} onChange={(event) => setName(event.target.value)} className="mt-2 h-11 w-full rounded-lg border border-border px-3 text-sm text-foreground" /></label><label className="block text-xs font-semibold text-muted-foreground">Provider Key<input type="password" value={key} onChange={(event) => setKey(event.target.value)} className="mt-2 h-11 w-full rounded-lg border border-border px-3 font-mono text-sm text-foreground" /></label></>}
          <label className="block text-xs font-semibold text-muted-foreground">Model<select value={modelId} onChange={(event) => { const next = event.target.value; setModelId(next); setInputPrice(priceFor("input_tokens", next)); setOutputPrice(priceFor("output_tokens", next)); }} className="mt-2 h-11 w-full rounded-lg border border-border bg-card px-3 text-sm text-foreground">{providerModels.map((model) => <option key={model.id} value={model.id}>{model.name}</option>)}</select></label>
          <div className="grid gap-3 sm:grid-cols-2"><label className="block text-xs font-semibold text-muted-foreground">Input price / 1M<input data-autofocus={mode.mode === "prices" ? true : undefined} type="number" min={0} value={inputPrice} onChange={(event) => setInputPrice(Number(event.target.value))} className="mt-2 h-11 w-full rounded-lg border border-border px-3 text-sm text-foreground" /></label><label className="block text-xs font-semibold text-muted-foreground">Output price / 1M<input type="number" min={0} value={outputPrice} onChange={(event) => setOutputPrice(Number(event.target.value))} className="mt-2 h-11 w-full rounded-lg border border-border px-3 text-sm text-foreground" /></label></div>
          <Button variant="primary" className="w-full" disabled={!valid} onClick={() => mode.mode === "create" ? onCreate({ providerId, name: name.trim(), key: key.trim(), prices }) : onUpdate(mode.key.id, prices)}>{mode.mode === "create" ? "Add Provider Key" : "Save prices"}</Button>
        </CardContent>
      </Card>
    </div>
  );
}

function ProviderKeys({ way, merchantId, onAdd, onConfigure, onDisable }: { way: GizWaySnapshot; merchantId: string; onAdd: () => void; onConfigure: (key: ProviderKey) => void; onDisable: (id: string) => void }) {
  const [visible, setVisible] = useState<Record<string, boolean>>({});
  return (
    <>
      <PageHeading eyebrow="Supply the network" title="Provider Keys" description="Contribute upstream capacity and receive AI credits when your keys are used." action={<Button variant="primary" onClick={onAdd}><Plus className="size-4" /> Add Provider Key</Button>} />
      <Card className="mb-5 border-brand/15 bg-brand-soft"><CardContent className="flex flex-col justify-between gap-4 py-5 sm:flex-row sm:items-center"><div><div className="text-sm font-semibold">Earnings settle to your default Merchant</div><div className="mt-1 font-mono text-xs text-muted-foreground">{merchantId}</div></div><Badge tone="green"><Check className="mr-1 size-3" /> Connected</Badge></CardContent></Card>
      <div className="grid gap-4 lg:grid-cols-2">
        {way.providerKeys.map((key) => (
          <Card key={key.id}><CardContent className="pt-5"><div className="flex items-start justify-between"><div className="flex items-center gap-3"><div className="grid size-11 place-items-center rounded-xl bg-[#17231d] text-sm font-bold text-white">{key.provider[0]}</div><div><div className="font-semibold">{key.name}</div><div className="text-xs text-muted-foreground">{key.provider} · {visible[key.id] ? key.maskedValue : `${key.maskedValue.slice(0, 7)}••••••••`}</div></div></div><Badge tone={key.status === "active" ? "green" : "neutral"}>{key.status}</Badge></div><div className="mt-4 flex gap-2"><Button variant="ghost" size="sm" onClick={() => setVisible((state) => ({ ...state, [key.id]: !state[key.id] }))}>{visible[key.id] ? "Hide Key" : "Show Key"}</Button>{visible[key.id] && <Button variant="ghost" size="sm" onClick={() => void navigator.clipboard?.writeText(key.maskedValue)}>Copy Key</Button>}</div><div className="mt-4 grid grid-cols-3 gap-3 rounded-xl bg-muted/50 p-4"><ModelStat label="Models" value={String(key.modelCount)} /><ModelStat label="Earned" value={credits(key.earnedCredits)} /><ModelStat label="Last used" value={key.lastUsedAt ? timestamp(key.lastUsedAt) : "Never"} /></div><div className="mt-4 flex justify-between"><Button variant="ghost" size="sm" disabled={key.status !== "active"} onClick={() => onConfigure(key)}>Configure prices</Button><Button variant="outline" size="sm" disabled={key.status !== "active"} onClick={() => onDisable(key.id)}>Disable Key</Button></div></CardContent></Card>
        ))}
        <button onClick={onAdd} className="grid min-h-52 place-items-center rounded-2xl border border-dashed border-border bg-card p-8 text-center transition-colors hover:border-brand/40 hover:bg-brand-soft"><div><div className="mx-auto grid size-11 place-items-center rounded-xl bg-muted"><Plus className="size-5 text-muted-foreground" /></div><div className="mt-3 text-sm font-semibold">Add another Provider Key</div><div className="mt-1 text-xs text-muted-foreground">OpenAI, Anthropic, DeepSeek, Google and more</div></div></button>
      </div>
    </>
  );
}

function Credits({ pay, onTopUp }: { pay: GizPaySnapshot; onTopUp: () => void }) {
  return (
    <>
      <PageHeading eyebrow="GizPay account" title="Credits" description="Add Credit and review top-ups, ledger transactions, Charges and Commissions separately." action={<Button variant="primary" onClick={onTopUp}><Plus className="size-4" /> Add credit</Button>} />
      <div className="grid gap-5 xl:grid-cols-[380px_1fr]">
        <Card className="overflow-hidden bg-[#17231d] text-white"><CardContent className="flex min-h-56 flex-col justify-between py-6"><div><div className="text-xs font-medium uppercase tracking-[0.12em] text-white/50">Available balance</div><div className="mt-3 text-4xl font-semibold tracking-[-0.04em]">{credits(pay.balance.available)}</div><div className="mt-1 text-sm text-white/55">GIZ Credit</div></div><div className="flex items-center justify-between"><div className="text-xs text-white/45">Updated {timestamp(pay.balance.updatedAt)}</div><Button className="bg-white text-[#17231d] hover:bg-white/90" size="sm" onClick={onTopUp}>Top up</Button></div></CardContent></Card>
        <Card><CardHeader><CardTitle>Ledger transactions</CardTitle><CardDescription>Posted movements in the GizPay ledger</CardDescription></CardHeader><CardContent className="space-y-1">{pay.ledger.map((item) => <div key={item.id} className="flex items-center gap-3 border-b border-border py-3 last:border-0"><div className={cn("grid size-9 place-items-center rounded-full", item.amount > 0 ? "bg-emerald-50 text-emerald-700" : "bg-muted text-muted-foreground")}>{item.amount > 0 ? <ArrowDownRight className="size-4" /> : <ArrowUpRight className="size-4" />}</div><div className="min-w-0 flex-1"><div className="text-sm font-medium">{item.label}</div><div className="text-xs text-muted-foreground">{timestamp(item.createdAt)}</div></div><div className={cn("text-sm font-semibold tabular-nums", item.amount > 0 && "text-emerald-700")}>{item.amount > 0 ? "+" : ""}{credits(item.amount)}</div></div>)}{pay.ledger.length === 0 && <EmptyRecord />}</CardContent></Card>
      </div>
      <div className="mt-5 grid gap-5 lg:grid-cols-3">
        <RecordCard title="Top-ups" description="Fake Channel credit additions" rows={pay.topUps.map((item) => ({ id: item.id, primary: `${credits(item.amount)} Credit`, secondary: `${item.channel} · ${item.status} · ${timestamp(item.createdAt)}` }))} />
        <RecordCard title="Charges" description="AI Order Credit charges" rows={pay.charges.map((item) => ({ id: item.id, primary: `${credits(item.amount)} Credit`, secondary: `${item.externalOrderId} · ${timestamp(item.createdAt)}` }))} />
        <RecordCard title="Commissions" description="Settled Provider earnings" rows={pay.commissions.map((item) => ({ id: item.id, primary: `+${credits(item.amount)} Credit`, secondary: `${item.chargeId} · ${timestamp(item.createdAt)}` }))} />
      </div>
    </>
  );
}

function RecordCard({ title, description, rows }: { title: string; description: string; rows: Array<{ id: string; primary: string; secondary: string }> }) {
  return <Card><CardHeader><CardTitle>{title}</CardTitle><CardDescription>{description}</CardDescription></CardHeader><CardContent className="space-y-3">{rows.map((row) => <div key={row.id} className="border-b border-border pb-3 last:border-0"><div className="text-sm font-semibold">{row.primary}</div><div className="mt-1 break-all text-xs text-muted-foreground">{row.secondary}</div></div>)}{rows.length === 0 && <EmptyRecord />}</CardContent></Card>;
}

function EmptyRecord() { return <div className="py-4 text-sm text-muted-foreground">No records yet.</div>; }

function Settings({ pay, onLogout }: { pay: GizPaySnapshot; onLogout?: () => void }) {
  return <><PageHeading eyebrow="Account" title="Settings" description="Your authenticated GizPay identity and default Merchant." />
    <div className="grid gap-5 lg:grid-cols-2"><Card><CardHeader><CardTitle>User identity</CardTitle><CardDescription>Signed in through ZITADEL</CardDescription></CardHeader><CardContent className="space-y-4"><SettingRow label="Display name" value={pay.user.name} /><SettingRow label="Email" value={pay.user.email} /><SettingRow label="User ID" value={pay.user.id} /><SettingRow label="Login status" value="Authenticated" />{onLogout && <Button variant="outline" onClick={onLogout}><LogOut className="size-4" /> Sign out</Button>}</CardContent></Card><Card><CardHeader><CardTitle>Default Merchant</CardTitle><CardDescription>Provider earnings settle to this Merchant</CardDescription></CardHeader><CardContent className="space-y-4"><SettingRow label="Public name" value={pay.user.merchantName} /><SettingRow label="Merchant ID" value={pay.user.merchantId} /><SettingRow label="Status" value={pay.user.merchantStatus} /></CardContent></Card></div>
  </>;
}

function SettingRow({ label, value }: { label: string; value: string }) { return <div><div className="text-xs font-semibold text-muted-foreground">{label}</div><div className="mt-1 break-all text-sm font-medium">{value || "Not available"}</div></div>; }

function TopUpDialog({ onClose, onTopUp }: { onClose: () => void; onTopUp: (amount: number) => void }) {
  const [amount, setAmount] = useState(100_000);
  const dialog = useDialogFocus(onClose);
  return (
    <div className="fixed inset-0 z-[70] grid place-items-center bg-black/35 p-4 backdrop-blur-sm">
      <button className="absolute inset-0" onClick={onClose} aria-label="Close top-up dialog" />
      <Card id={dialog.id} onKeyDown={dialog.onKeyDown} role="dialog" aria-modal="true" aria-labelledby="topup-title" tabIndex={-1} className="relative z-10 w-full max-w-md shadow-2xl">
        <CardHeader className="flex-row items-start justify-between"><div><CardTitle id="topup-title" className="text-lg">Add GizWay credit</CardTitle><CardDescription className="mt-1">Fake Channel · no real payment is made</CardDescription></div><Button aria-label="Close top-up dialog" variant="ghost" size="icon" onClick={onClose}><X className="size-4" /></Button></CardHeader>
        <CardContent>
          <div className="grid grid-cols-3 gap-2">{[50_000, 100_000, 250_000].map((value) => <button key={value} onClick={() => setAmount(value)} className={cn("rounded-xl border px-3 py-3 text-sm font-semibold", amount === value ? "border-brand bg-brand-soft text-brand" : "border-border hover:bg-muted")}>{credits(value)}</button>)}</div>
          <label className="mt-4 block text-xs font-semibold text-muted-foreground">Credit amount<input data-autofocus type="number" min={1} value={amount} onChange={(event) => setAmount(Number(event.target.value))} className="mt-2 h-11 w-full rounded-lg border border-border px-3 text-base font-semibold text-foreground outline-none focus:border-brand/40" /></label>
          <div className="mt-5 rounded-xl bg-muted p-4 text-sm"><div className="flex justify-between"><span className="text-muted-foreground">You receive</span><strong>{credits(amount)} GIZ Credit</strong></div><div className="mt-2 flex justify-between"><span className="text-muted-foreground">Channel</span><span>Fake Channel</span></div></div>
          <Button variant="primary" className="mt-5 w-full" size="lg" onClick={() => onTopUp(amount)}>Confirm top-up</Button>
        </CardContent>
      </Card>
    </div>
  );
}
