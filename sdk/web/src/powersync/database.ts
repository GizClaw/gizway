import { PowerSyncDatabase, type Schema } from "@powersync/web";
import type { Region } from "../contracts/gizway";
import { gizPaySchema } from "./gizpay/schema";
import { gizWaySchema } from "./gizway/schema";

export type DatabaseKind = "gizpay" | "gizway";
export type DatabaseFactory = (schema: Schema, filename: string) => Promise<PowerSyncDatabase>;

export const defaultDatabaseFactory: DatabaseFactory = async (schema, dbFilename) => {
  const database = new PowerSyncDatabase({ schema, database: { dbFilename } });
  await database.init();
  return database;
};

export async function authenticatedDatabaseNames(region: Region, issuer: string, clientId: string, subject: string, crypto: Pick<Crypto, "subtle"> = globalThis.crypto): Promise<Record<DatabaseKind, string>> {
  if (!subject) throw new Error("authenticated database subject is required");
  const namespace = await sha256(`${region}\u0000${issuer}\u0000${clientId}\u0000${subject}`, crypto);
  return { gizpay: `gizpay-auth-${namespace}.db`, gizway: `gizway-${region}-auth-${namespace}.db` };
}

export function publicDatabaseNames(region: Region): Record<DatabaseKind, string> {
  return { gizpay: `gizpay-${region}-public.db`, gizway: `gizway-${region}-public.db` };
}

export async function openDatabasePair(names: Record<DatabaseKind, string>, factory: DatabaseFactory = defaultDatabaseFactory): Promise<[PowerSyncDatabase, PowerSyncDatabase]> {
  const pay = await factory(gizPaySchema, names.gizpay);
  try { return [pay, await factory(gizWaySchema, names.gizway)]; }
  catch (error) { await pay.close().catch(() => undefined); throw error; }
}

export async function sha256(value: string, crypto: Pick<Crypto, "subtle"> = globalThis.crypto): Promise<string> {
  const digest = await crypto.subtle.digest("SHA-256", new TextEncoder().encode(value));
  return Array.from(new Uint8Array(digest), (byte) => byte.toString(16).padStart(2, "0")).join("");
}
