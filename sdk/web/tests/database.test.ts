import { expect, it } from "vitest";
import { authenticatedDatabaseNames, publicDatabaseNames } from "../src/powersync/database";

it("uses collision-resistant identity namespaces distinct from public databases", async () => {
  const left = await authenticatedDatabaseNames("global", "https://issuer", "client", "a/b");
  const right = await authenticatedDatabaseNames("global", "https://issuer", "client", "a_b");
  const cn = await authenticatedDatabaseNames("cn", "https://issuer", "client", "a/b");
  expect(left).not.toEqual(right);
  expect(left).not.toEqual(cn);
  expect(left.gizpay).toMatch(/^gizpay-auth-[0-9a-f]{64}\.db$/);
  expect(publicDatabaseNames("global")).not.toEqual(left);
});
