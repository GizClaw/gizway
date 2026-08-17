"use client";

import { useEffect, useState } from "react";
import { PublicHome, type PublicCatalogModel, type PublicCatalogProduct } from "@/components/prototype/public-home";
import type { Region } from "@/data/contracts/gizway";
import type { FakeScenario } from "@/data/fake/scenarios";
import { createPrototypeDataProvider } from "@/data/provider";

export function FakePublicApp({ region, scenario }: { region: Region; scenario: FakeScenario }) {
  const [models, setModels] = useState<PublicCatalogModel[]>();
  const [products, setProducts] = useState<PublicCatalogProduct[]>();
  const [error, setError] = useState("");

  useEffect(() => {
    let cancelled = false;
    void createPrototypeDataProvider(scenario, region).then(async (provider) => {
      const [pay, way] = await Promise.all([provider.gizpay.getSnapshot(), provider.gizway.getSnapshot(region)]);
      if (cancelled) return;
      setModels(way.models.map((model) => ({
        name: model.name,
        family: model.family,
        description: model.description,
        price: model.rates[0] ? `From ${model.rates[0].credits.toLocaleString()} credits / ${model.rates[0].unit}` : "Price synchronizing",
      })));
      setProducts(pay.products.filter((product) => product.active).map((product) => ({ title: product.name, description: product.description, price: product.priceLabel })));
    }).catch((reason: unknown) => {
      if (!cancelled) setError(reason instanceof Error ? reason.message : "Fake PowerSync Catalog failed");
    });
    return () => { cancelled = true; };
  }, [region, scenario]);

  if (error) return <RuntimeState state="sync_error" detail={error} />;
  if (!models || !products) return <RuntimeState state="first_sync" detail="Loading the local PowerSync Catalog…" />;
  return <PublicHome region={region} models={models} products={products} />;
}

function RuntimeState({ state, detail }: { state: "first_sync" | "sync_error"; detail: string }) {
  return <main className="grid min-h-screen place-items-center bg-app p-6"><section data-testid="runtime-state" data-state={state} className="max-w-md rounded-2xl border border-border bg-card p-8 text-center"><h1 className="text-xl font-bold">{state.replaceAll("_", " ")}</h1><p className="mt-2 text-sm text-muted-foreground">{detail}</p></section></main>;
}
