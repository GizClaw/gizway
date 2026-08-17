"use client";

import { useEffect, useState } from "react";
import { beginLogin } from "@/data/runtime/auth";
import { loadRuntimeConfig } from "@/data/runtime/config";

export default function LoginPage() {
  const [error, setError] = useState("");
  useEffect(() => {
    void loadRuntimeConfig().then(beginLogin).catch((reason) => setError(reason instanceof Error ? reason.message : "Authentication failed"));
  }, []);
  return <main className="grid min-h-screen place-items-center bg-app p-6"><section data-testid="runtime-state" data-state={error ? "sync_error" : "authenticating"} className="max-w-md rounded-2xl border border-border bg-card p-8 text-center"><h1 className="text-xl font-bold">{error ? "Unable to sign in" : "Redirecting to ZITADEL"}</h1><p className="mt-2 text-sm text-muted-foreground">{error || "Preparing a secure PKCE login…"}</p></section></main>;
}
