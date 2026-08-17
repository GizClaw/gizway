import type { GizPayRepository } from "@/data/contracts/gizpay";
import type { GizWayRepository, Region } from "@/data/contracts/gizway";
import type { FakeScenario } from "@/data/fake/scenarios";
import { getGizPayDatabase, getGizWayDatabase } from "@/data/powersync/database";
import { PowerSyncGizPayRepository } from "@/data/powersync/gizpay/repository";
import { PowerSyncGizWayRepository } from "@/data/powersync/gizway/repository";

export type PrototypeDataProvider = {
  gizpay: GizPayRepository;
  gizway: GizWayRepository;
  subscribe?: (listener: () => void) => () => void;
  syncStates?: () => ServiceSyncStates;
};

export type ServiceSyncState = "first_sync" | "ready" | "offline" | "denied" | "sync_error";
export type ServiceSyncStates = { gizpay: ServiceSyncState; gizway: ServiceSyncState };

let initializationQueue = Promise.resolve();

export async function createPrototypeDataProvider(scenario: FakeScenario, region: Region): Promise<PrototypeDataProvider> {
  let provider: PrototypeDataProvider | undefined;
  const initialization = initializationQueue.then(async () => {
    const [gizPayDatabase, gizWayDatabase] = await Promise.all([
      getGizPayDatabase(),
      getGizWayDatabase(region),
    ]);
    const gizpay = new PowerSyncGizPayRepository(gizPayDatabase);
    const gizway = new PowerSyncGizWayRepository(gizWayDatabase);
    await Promise.all([gizpay.seed(scenario), gizway.seed(region, scenario)]);
    provider = { gizpay, gizway };
  });

  initializationQueue = initialization.then(() => undefined, () => undefined);
  await initialization;
  if (!provider) throw new Error("PowerSync prototype data provider did not initialize");
  return provider;
}
