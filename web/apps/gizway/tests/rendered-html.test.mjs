import assert from "node:assert/strict";
import { readFile } from "node:fs/promises";
import test from "node:test";

test("builds a browser-only static application", async () => {
  const [html, packageManifest, dockerfile, caddyfile] = await Promise.all([
    readFile(new URL("../dist/index.html", import.meta.url), "utf8"),
    readFile(new URL("../package.json", import.meta.url), "utf8"),
    readFile(new URL("../../../../docker/gizway-web/Dockerfile", import.meta.url), "utf8"),
    readFile(new URL("../../../../docker/gizway-web/Caddyfile", import.meta.url), "utf8"),
  ]);

  assert.match(html, /<title>GizWay — AI Network<\/title>/i);
  assert.match(html, /<div id="root"><\/div>/);
  assert.doesNotMatch(packageManifest, /vinext|wrangler|@cloudflare\/vite-plugin|next/);
  assert.match(dockerfile, /FROM caddy:/);
  assert.doesNotMatch(dockerfile, /CMD \["node"/);
  assert.match(caddyfile, /try_files \{path\} \/index\.html/);
  assert.match(caddyfile, /respond \/healthz 200/);
});

test("keeps two PowerSync databases behind repository contracts", async () => {
  const [provider, database, payContract, wayContract, payAdapter, wayAdapter, packageManifest] = await Promise.all([
    readFile(new URL("../data/provider.ts", import.meta.url), "utf8"),
    readFile(new URL("../data/powersync/database.ts", import.meta.url), "utf8"),
    readFile(new URL("../data/contracts/gizpay.ts", import.meta.url), "utf8"),
    readFile(new URL("../data/contracts/gizway.ts", import.meta.url), "utf8"),
    readFile(new URL("../data/powersync/gizpay/repository.ts", import.meta.url), "utf8"),
    readFile(new URL("../data/powersync/gizway/repository.ts", import.meta.url), "utf8"),
    readFile(new URL("../package.json", import.meta.url), "utf8"),
  ]);

  assert.match(packageManifest, /"@powersync\/web"/);
  assert.match(database, /new PowerSyncDatabase/);
  assert.match(database, /gizpay-prototype\.db/);
  assert.match(database, /gizway-\$\{region\}-prototype\.db/);
  assert.match(provider, /createPrototypeDataProvider/);
  assert.doesNotMatch(provider, /FakeGizPayRepository|FakeGizWayRepository/);
  assert.match(payContract, /interface GizPayRepository/);
  assert.match(wayContract, /interface GizWayRepository/);
  assert.match(payAdapter, /class PowerSyncGizPayRepository implements GizPayRepository/);
  assert.match(wayAdapter, /class PowerSyncGizWayRepository implements GizWayRepository/);
});
