import type { GizPaySnapshot } from "@/data/contracts/gizpay";
import type { GizWaySnapshot, Region } from "@/data/contracts/gizway";
import type { PrototypeDataProvider } from "@/data/provider";

export type DashboardSnapshotResult = {
  pay?: GizPaySnapshot;
  way?: GizWaySnapshot;
  errors: Partial<Record<"gizpay" | "gizway", string>>;
};

export async function readDashboardSnapshots(provider: PrototypeDataProvider, region: Region): Promise<DashboardSnapshotResult> {
  const [pay, way] = await Promise.allSettled([
    provider.gizpay.getSnapshot(),
    provider.gizway.getSnapshot(region),
  ]);
  return {
    pay: pay.status === "fulfilled" ? pay.value : undefined,
    way: way.status === "fulfilled" ? way.value : undefined,
    errors: {
      ...(pay.status === "rejected" ? { gizpay: errorMessage(pay.reason, "GizPay local query failed") } : {}),
      ...(way.status === "rejected" ? { gizway: errorMessage(way.reason, "GizWay local query failed") } : {}),
    },
  };
}

function errorMessage(reason: unknown, fallback: string): string {
  return reason instanceof Error ? reason.message : fallback;
}
