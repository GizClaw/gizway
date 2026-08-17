"use client";

import { useEffect, useState } from "react";
import type { Region } from "@/data/contracts/gizway";
import { GizWayDashboard } from "@/components/prototype/gizway-dashboard";
import { PublicHome, type PublicCatalogModel, type PublicCatalogNotice, type PublicCatalogProduct } from "@/components/prototype/public-home";
import { loadRuntimeConfig, publicCatalogToken, type PublicRuntimeConfig } from "@/data/runtime/config";
import { beginLogin, clearSession, humanToken, logoutURL, subjectFromToken } from "@/data/runtime/auth";
import { connectHumanDatabases, createDatabaseCloser, maintainPAYGSubscription, maintainPowerSyncService, openDatabasePair, ReadOnlyCatalogConnector, serviceSyncState, watchCatalogDatabases, type ConnectedProvider } from "@/data/runtime/real-provider";
import type { ServiceSyncStates } from "@/data/provider";
import { openRuntimeGizPayDatabase, openRuntimeGizWayDatabase } from "@/data/powersync/database";

type RuntimeState = "opening_local_db" | "first_sync" | "activating_subscription" | "ready" | "offline" | "denied" | "sync_error";

export function RealApp({ region }: { region: Region }) {
  const [state, setState] = useState<RuntimeState>("opening_local_db");
  const [provider, setProvider] = useState<ConnectedProvider>();
  const [catalog, setCatalog] = useState<PublicCatalogModel[]>([]);
  const [products, setProducts] = useState<PublicCatalogProduct[]>([]);
  const [catalogStates, setCatalogStates] = useState<ServiceSyncStates>();
  const [authenticated, setAuthenticated] = useState<boolean>();
  const [error, setError] = useState("");

  async function signOut() {
    const config = await loadRuntimeConfig();
    const redirect = logoutURL(config);
    try {
      await provider?.close(true);
    } finally {
      clearSession();
      location.assign(redirect);
    }
  }

  useEffect(() => {
    let cancelled = false;
    const controller = new AbortController();
    let closeActive: (() => Promise<void>) | undefined;
    let unsubscribeActive: (() => void) | undefined;
    void (async () => {
      try {
        const config = await loadRuntimeConfig();
        const token = await humanToken(config);
        if (!token) {
          setAuthenticated(false);
          const publicCatalog = await openPublicCatalog(config, region, controller.signal);
          closeActive = publicCatalog.close;
          if (cancelled) {
            await closeActive();
            return;
          }
          const refreshCatalog = async () => {
            const snapshot = await publicCatalog.getSnapshot();
            if (!cancelled) {
              setCatalog(snapshot.models);
              setProducts(snapshot.products);
              setCatalogStates(publicCatalog.syncStates());
            }
          };
          unsubscribeActive = publicCatalog.subscribe(() => { void refreshCatalog().catch((reason: unknown) => {
            if (!cancelled) setError(reason instanceof Error ? reason.message : "Failed to refresh Public Catalog");
          }); });
          await refreshCatalog();
          if (!cancelled) setState("ready");
          return;
        }
        setAuthenticated(true);
        setState("first_sync");
        const tokenProvider = async () => {
          const current = await humanToken(config);
          if (!current) throw new Error("Human session expired");
          return current;
        };
        const connected = await connectHumanDatabases(config, region, subjectFromToken(token), tokenProvider, setError, controller.signal);
        closeActive = () => connected.close();
        if (cancelled) {
          await closeActive();
          return;
        }
        setProvider(connected);
        unsubscribeActive = maintainPAYGSubscription(connected, config.site.hostname, (next, reason) => {
          if (cancelled) return;
          if (reason) setError(reason instanceof Error ? reason.message : "PAYG activation is waiting for GizPay");
          setState(next === "ready" ? "ready" : next === "activating" ? "activating_subscription" : reason ? "sync_error" : "first_sync");
        });
      } catch (reason) {
        await closeActive?.().catch(() => undefined);
        if (!cancelled) {
          setError(reason instanceof Error ? reason.message : "Unknown synchronization failure");
          setState("sync_error");
        }
      }
    })();
    return () => {
      cancelled = true;
      unsubscribeActive?.();
      controller.abort();
      void closeActive?.().catch(() => undefined);
    };
  }, [region]);

  if (authenticated === false && state === "ready") {
    const serviceStates = Object.values(catalogStates ?? {});
    const catalogState = serviceStates.includes("denied") ? "denied" : serviceStates.includes("sync_error") ? "sync_error" : serviceStates.includes("first_sync") ? "first_sync" : serviceStates.includes("offline") ? "offline" : catalog.length === 0 && products.length === 0 ? "empty" : "ready";
    const statusMarkers = <><span className="sr-only" data-testid="public-catalog-state" data-state={catalogState} /><span className="sr-only" data-testid="public-model-catalog-state" data-state={catalogStates?.gizway === "ready" && catalog.length === 0 ? "empty" : catalogStates?.gizway ?? "first_sync"}>{catalog.length}</span><span className="sr-only" data-testid="public-product-catalog-state" data-state={catalogStates?.gizpay === "ready" && products.length === 0 ? "empty" : catalogStates?.gizpay ?? "first_sync"}>{products.length}</span><span className="sr-only" data-testid="public-gizpay-sync-state" data-state={catalogStates?.gizpay ?? "first_sync"} /><span className="sr-only" data-testid="public-gizway-sync-state" data-state={catalogStates?.gizway ?? "first_sync"} /></>;
    if (catalog.length === 0 && products.length === 0 && catalogState !== "empty" && catalogState !== "ready") {
      return <div>{statusMarkers}<RuntimeStateView state={catalogState as RuntimeState} detail={error || "Connecting to the Public Catalog PowerSync services…"} /></div>;
    }
    const notices = [
      publicCatalogNotice("gizpay", catalogStates?.gizpay ?? "first_sync", products.length),
      publicCatalogNotice("gizway", catalogStates?.gizway ?? "first_sync", catalog.length),
    ].filter((notice): notice is PublicCatalogNotice => Boolean(notice));
    return <div>{statusMarkers}<PublicHome region={region} models={catalog} products={products} notices={notices} onSignIn={async () => beginLogin(await loadRuntimeConfig())} /></div>;
  }
  if (authenticated && provider && state === "ready") {
    return <GizWayDashboard siteRegion={region} dataProvider={provider} showScenarioSelector={false} onLogout={() => void signOut()} />;
  }
  return <RuntimeStateView state={state} detail={error || "Preparing your GizWay workspace…"} />;
}

function publicCatalogNotice(id: "gizpay" | "gizway", state: ServiceSyncStates["gizpay"], count: number): PublicCatalogNotice | undefined {
  if (state === "ready" && count > 0) return undefined;
  const service = id === "gizpay" ? "GizPay Catalog" : "Regional GizWay Catalog";
  if (state === "ready") return { id, service, state: "empty", message: `No published ${id === "gizpay" ? "products" : "models"} are currently available.` };
  const messages = {
    first_sync: "The first synchronization is still in progress.",
    offline: "The service is offline. Locally cached data may be stale.",
    denied: "Catalog access was denied.",
    sync_error: "Catalog synchronization failed.",
  } as const;
  return { id, service, state, message: messages[state] };
}

function RuntimeStateView({ state, detail }: { state: RuntimeState; detail: string }) {
  return <main className="grid min-h-screen place-items-center bg-app p-6"><section data-testid="runtime-state" data-state={state} className="max-w-md rounded-2xl border border-border bg-card p-8 text-center"><h1 className="text-xl font-bold">{state.replaceAll("_", " ")}</h1><p className="mt-2 text-sm text-muted-foreground">{detail}</p></section></main>;
}

async function openPublicCatalog(config: PublicRuntimeConfig, region: Region, signal?: AbortSignal): Promise<{ getSnapshot: () => Promise<{ models: PublicCatalogModel[]; products: PublicCatalogProduct[] }>; subscribe: (listener: () => void) => () => void; syncStates: () => ServiceSyncStates; close: () => Promise<void> }> {
  const [pay, way] = await openDatabasePair(() => openRuntimeGizPayDatabase(`${region}-public`), () => openRuntimeGizWayDatabase(region, "public"));
  const close = createDatabaseCloser(pay, way);
  const closeOnAbort = () => { void close().catch(() => undefined); };
  signal?.addEventListener("abort", closeOnAbort, { once: true });
  const token = () => publicCatalogToken(config);
  try {
    signal?.throwIfAborted();
    const listeners = new Set<() => void>();
    const changed = () => { for (const listener of listeners) listener(); };
    const payConnection = maintainPowerSyncService(pay, new ReadOnlyCatalogConnector(config.services.gizpay_powersync_url, token), changed, signal);
    const wayConnection = maintainPowerSyncService(way, new ReadOnlyCatalogConnector(config.services.gizway_powersync_url, token), changed, signal);
    signal?.throwIfAborted();
    return {
      getSnapshot: async () => {
        const [rows, priceRows, productRows] = await Promise.all([
          way.getAll<{ id: string; title: string; family: string; description: string }>("SELECT model_id id,title,family,description FROM model_listings WHERE availability='available' ORDER BY display_order,id LIMIT 6"),
          way.getAll<{ model_id: string; metric: string; unit_size: number; price_microcredits: number }>("SELECT model_id,metric,unit_size,price_microcredits FROM model_customer_prices ORDER BY model_id,metric"),
          pay.getAll<{ title: string; description: string; price_text: string }>("SELECT title,description,price_text FROM product_listings WHERE site=? AND status='active' ORDER BY display_order,id LIMIT 3", [config.site.hostname]),
        ]);
        const pricesByModel = new Map<string, typeof priceRows>();
        for (const price of priceRows) pricesByModel.set(price.model_id, [...(pricesByModel.get(price.model_id) ?? []), price]);
        return {
          models: rows.map((row) => ({ name: row.title, family: row.family, description: row.description, price: formatCatalogPrices(pricesByModel.get(row.id) ?? []) })),
          products: productRows.map((row) => ({ title: row.title, description: row.description, price: row.price_text })),
        };
      },
      subscribe: (listener) => {
        listeners.add(listener);
        const stopWatch = watchCatalogDatabases(pay, way, listener, (message) => { if (!signal?.aborted) console.error("Public Catalog PowerSync", message); });
        return () => { listeners.delete(listener); stopWatch(); };
      },
      syncStates: () => ({ gizpay: serviceSyncState(pay, payConnection.state()), gizway: serviceSyncState(way, wayConnection.state()) }),
      close: async () => { payConnection.stop(); wayConnection.stop(); await close(); },
    };
  } catch (error) {
    await close().catch(() => undefined);
    throw error;
  } finally {
    signal?.removeEventListener("abort", closeOnAbort);
  }
}

function formatCatalogPrices(prices: Array<{ metric: string; unit_size: number; price_microcredits: number }>): string {
  if (prices.length === 0) return "Pricing is synchronizing";
  const number = new Intl.NumberFormat("en-US");
  return prices.map((price) => `${price.metric}: ${number.format(price.price_microcredits)} GIZ Credit / ${price.unit_size === 1_000_000 ? "1M" : number.format(price.unit_size)}`).join(" · ");
}
