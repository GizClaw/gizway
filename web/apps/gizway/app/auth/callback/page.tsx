"use client";

import { useEffect, useState } from "react";
import { completeLogin } from "@/data/runtime/auth";
import { loadRuntimeConfig } from "@/data/runtime/config";

export default function CallbackPage() {
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
  return <main className="grid min-h-screen place-items-center bg-app p-6"><section data-testid="runtime-state" data-state={error ? "denied" : "authenticating"} className="max-w-md rounded-2xl border border-border bg-card p-8 text-center"><h1 className="text-xl font-bold">{error ? "Authentication failed" : "Completing sign in"}</h1><p className="mt-2 text-sm text-muted-foreground">{error || "Verifying your ZITADEL session…"}</p></section></main>;
}
