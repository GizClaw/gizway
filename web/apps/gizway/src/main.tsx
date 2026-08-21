import { StrictMode, useEffect, useState } from "react";
import { createRoot } from "react-dom/client";
import { GizWayDashboard } from "@/components/prototype/gizway-dashboard";
import { FakePublicApp } from "@/components/runtime/fake-public-app";
import { RealApp } from "@/components/runtime/real-app";
import type { Region } from "@/data/contracts/gizway";
import { fakeScenarios, type FakeScenario } from "@/data/fake/scenarios";
import { beginLogin, completeLogin } from "@/data/runtime/auth";
import { loadRuntimeConfig } from "@/data/runtime/config";
import "@fontsource-variable/geist";
import "@fontsource-variable/geist-mono";
import "./globals.css";

function regionForHost(hostname: string): Region {
  return hostname === "cn.gizclaw.com" || hostname.startsWith("cn.") ? "cn" : "global";
}

function RuntimeState({ state, title, message }: { state: string; title: string; message: string }) {
  return <main className="grid min-h-screen place-items-center bg-app p-6"><section data-testid="runtime-state" data-state={state} className="max-w-md rounded-2xl border border-border bg-card p-8 text-center"><h1 className="text-xl font-bold">{title}</h1><p className="mt-2 text-sm text-muted-foreground">{message}</p></section></main>;
}

function Login() {
  const [error, setError] = useState("");
  useEffect(() => {
    void loadRuntimeConfig().then(beginLogin).catch((reason) => setError(reason instanceof Error ? reason.message : "Authentication failed"));
  }, []);
  return <RuntimeState state={error ? "sync_error" : "authenticating"} title={error ? "Unable to sign in" : "Redirecting to ZITADEL"} message={error || "Preparing a secure PKCE login…"} />;
}

function Callback() {
  const [error, setError] = useState("");
  useEffect(() => {
    void (async () => {
      try {
        await completeLogin(await loadRuntimeConfig(), new URL(location.href));
        location.replace("/");
      } catch (reason) {
        setError(reason instanceof Error ? reason.message : "Authentication failed");
      }
    })();
  }, []);
  return <RuntimeState state={error ? "denied" : "authenticating"} title={error ? "Authentication failed" : "Completing sign in"} message={error || "Verifying your ZITADEL session…"} />;
}

function App() {
  if (location.pathname === "/auth/login") return <Login />;
  if (location.pathname === "/auth/callback") return <Callback />;

  const params = new URLSearchParams(location.search);
  const region = regionForHost(location.hostname);
  const requestedScenario = params.get("scenario");
  const scenario: FakeScenario = requestedScenario && fakeScenarios.some((item) => item.id === requestedScenario)
    ? requestedScenario as FakeScenario
    : "active-payg";

  if (import.meta.env.VITE_GIZWAY_WEB_MODE !== "fake") return <RealApp region={region} />;
  const state = params.get("state");
  if (state) {
    const label = state.replaceAll("_", " ");
    return <RuntimeState state={state} title={label} message={`The application is ${label}. Account data will appear when this state completes.`} />;
  }
  return params.get("authenticated") === "1"
    ? <GizWayDashboard siteRegion={region} />
    : <FakePublicApp region={region} scenario={scenario} />;
}

const root = document.getElementById("root");
if (!root) throw new Error("missing application root");
createRoot(root).render(<StrictMode><App /></StrictMode>);
