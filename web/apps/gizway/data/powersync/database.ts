import { PowerSyncDatabase, type Schema } from "@powersync/web";
import type { Region } from "@/data/contracts/gizway";
import { gizPaySchema } from "@/data/powersync/gizpay/schema";
import { gizWaySchema } from "@/data/powersync/gizway/schema";

let gizPayDatabasePromise: Promise<PowerSyncDatabase> | undefined;
const gizWayDatabasePromises = new Map<Region, Promise<PowerSyncDatabase>>();

async function openDatabase(schema: Schema, dbFilename: string) {
  const database = new PowerSyncDatabase({
    schema,
    database: { dbFilename },
  });
  await database.init();
  return database;
}

export function openRuntimeGizPayDatabase(namespace: string) {
  return openDatabase(gizPaySchema, `gizpay-${namespace}.db`);
}

export function openRuntimeGizWayDatabase(region: Region, namespace: string) {
  return openDatabase(gizWaySchema, `gizway-${region}-${namespace}.db`);
}

export function getGizPayDatabase() {
  gizPayDatabasePromise ??= openDatabase(gizPaySchema, "gizpay-prototype.db");
  return gizPayDatabasePromise;
}

export function getGizWayDatabase(region: Region) {
  let database = gizWayDatabasePromises.get(region);
  if (!database) {
    database = openDatabase(gizWaySchema, `gizway-${region}-prototype.db`);
    gizWayDatabasePromises.set(region, database);
  }
  return database;
}
