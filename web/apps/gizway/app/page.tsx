import type { Metadata } from "next";
import { headers } from "next/headers";
import { GizWayDashboard } from "@/components/prototype/gizway-dashboard";
import type { Region } from "@/data/contracts/gizway";
import { RealApp } from "@/components/runtime/real-app";
import { FakePublicApp } from "@/components/runtime/fake-public-app";
import { fakeScenarios, type FakeScenario } from "@/data/fake/scenarios";

async function siteRegion(): Promise<Region> {
  const requestHeaders = await headers();
  const hostname = (requestHeaders.get("host") ?? "global.localhost")
    .split(":", 1)[0]
    .toLowerCase();

  return hostname === "cn.gizway.com" || hostname.startsWith("cn.")
    ? "cn"
    : "global";
}

export async function generateMetadata(): Promise<Metadata> {
  return {
    title: { absolute: "GizWay — AI Network" },
    description: "Use, supply and settle AI services through GizWay.",
  };
}

export default async function Home({ searchParams }: { searchParams: Promise<Record<string, string | string[] | undefined>> }) {
  const params = await searchParams;
  const region = await siteRegion();
	const scenario = typeof params.scenario === "string" && fakeScenarios.some((item) => item.id === params.scenario) ? params.scenario as FakeScenario : "active-payg";
	if (process.env.GIZWAY_WEB_MODE !== "fake") return <RealApp region={region} />;
	if (typeof params.state === "string") {
		const label = params.state.replaceAll("_", " ");
		return <main className="grid min-h-screen place-items-center bg-app p-6"><section data-testid="runtime-state" data-state={params.state} className="max-w-md rounded-2xl border border-border bg-card p-8 text-center"><h1 className="text-xl font-bold capitalize">{label}</h1><p className="mt-2 text-sm text-muted-foreground">The application is {label}. Account data will appear when this state completes.</p></section></main>;
	}
  return params.authenticated === "1"
    ? <GizWayDashboard siteRegion={region} />
    : <FakePublicApp region={region} scenario={scenario} />;
}
