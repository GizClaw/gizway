import assert from "node:assert/strict";
import { spawn } from "node:child_process";
import { readFile } from "node:fs/promises";
import { createServer } from "node:net";
import test from "node:test";
import { fileURLToPath } from "node:url";

async function availablePort() {
  const server = createServer();
  await new Promise((resolve, reject) => {
    server.once("error", reject);
    server.listen(0, "127.0.0.1", resolve);
  });
  const address = server.address();
  const port = typeof address === "object" && address ? address.port : 0;
  await new Promise((resolve, reject) => server.close((error) => error ? reject(error) : resolve()));
  return port;
}

async function withProductionServer(run) {
  const port = await availablePort();
  const executable = fileURLToPath(new URL("../node_modules/.bin/vinext", import.meta.url));
  const child = spawn(executable, ["start", "--port", String(port)], {
    cwd: new URL("../", import.meta.url),
    env: { ...process.env, GIZWAY_WEB_MODE: "fake" },
    stdio: ["ignore", "pipe", "pipe"],
  });

  try {
    await new Promise((resolve, reject) => {
      const timeout = setTimeout(() => reject(new Error("production server did not start")), 10_000);
      child.once("error", reject);
      child.stdout.on("data", (chunk) => {
        if (String(chunk).includes("Production server running")) {
          clearTimeout(timeout);
          resolve();
        }
      });
    });
    await run(port);
  } finally {
    child.kill("SIGTERM");
  }
}

test("serves independent Global and CN sites from their hostnames", async () => {
  await withProductionServer(async (port) => {
    const globalResponse = await fetch(`http://global.localhost:${port}/`, {
      headers: { accept: "text/html" },
    });
    const cnResponse = await fetch(`http://cn.localhost:${port}/`, {
      headers: { accept: "text/html" },
    });

    assert.equal(globalResponse.status, 200);
    assert.equal(cnResponse.status, 200);
    assert.match(globalResponse.headers.get("content-type") ?? "", /^text\/html\b/i);

    const globalHtml = await globalResponse.text();
    const cnHtml = await cnResponse.text();
    assert.match(globalHtml, /<title>GizWay — AI Network<\/title>/i);
    assert.match(globalHtml, /data-state="first_sync"/);
    assert.match(globalHtml, />Loading the local PowerSync Catalog/);
    assert.match(cnHtml, /<title>GizWay — AI Network<\/title>/i);
    assert.match(cnHtml, /data-state="first_sync"/);
    assert.match(cnHtml, />Loading the local PowerSync Catalog/);
    assert.doesNotMatch(globalHtml, />Global (?:site|catalog)</);
    assert.doesNotMatch(cnHtml, />(?:CN|China) (?:site|catalog)</);
    assert.doesNotMatch(globalHtml + cnHtml, /RegionSwitch|Global site|CN site|Your site is taking shape/);
  });
});

test("uses two real PowerSync databases with fake seed data behind repository contracts", async () => {
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
  assert.match(payAdapter, /disconnectAndClear/);
  assert.match(wayAdapter, /disconnectAndClear/);
});
