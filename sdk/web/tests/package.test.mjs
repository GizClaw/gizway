import assert from "node:assert/strict";
import { execFileSync } from "node:child_process";
import { mkdtempSync, readFileSync, rmSync, writeFileSync } from "node:fs";
import { tmpdir } from "node:os";
import { join } from "node:path";
import test from "node:test";

test("packed SDK installs, typechecks, and bundles from root exports", () => {
  const root = new URL("..", import.meta.url).pathname;
  const temporary = mkdtempSync(join(tmpdir(), "gizway-sdk-consumer-"));
  try {
    const tarball = execFileSync("npm", ["pack", "--ignore-scripts", "--json", "--pack-destination", temporary], { cwd: root, encoding: "utf8" });
    const filename = JSON.parse(tarball)[0].filename;
    writeFileSync(join(temporary, "package.json"), JSON.stringify({ type: "module", scripts: { check: "tsc --noEmit && vite build" }, dependencies: { "@idy/gizway-browser-sdk": `file:./${filename}`, typescript: "5.9.3", vite: "8.2.2" } }));
    writeFileSync(join(temporary, "index.html"), '<script type="module" src="/src.ts"></script>\n');
    writeFileSync(join(temporary, "src.ts"), 'import { createGizWayBrowserClient, type Region } from "@idy/gizway-browser-sdk"; const region: Region = "global"; void createGizWayBrowserClient({entryOrigin:"https://entry.example.test", region});\n');
    writeFileSync(join(temporary, "tsconfig.json"), JSON.stringify({ compilerOptions: { target: "ES2022", module: "ES2022", moduleResolution: "Bundler", strict: true, lib: ["ES2023", "DOM"] }, include: ["src.ts"] }));
    execFileSync("npm", ["install", "--ignore-scripts", "--no-audit", "--no-fund"], { cwd: temporary, stdio: "pipe" });
    execFileSync("npm", ["run", "check"], { cwd: temporary, stdio: "pipe" });
    const packageJSON = JSON.parse(readFileSync(join(temporary, "node_modules/@idy/gizway-browser-sdk/package.json"), "utf8"));
    assert.equal(packageJSON.name, "@idy/gizway-browser-sdk");
    assert.equal(packageJSON.private, undefined);
    assert.deepEqual(packageJSON.files, ["dist", "package.json"]);
    assert.equal(packageJSON.publishConfig.registry, "https://npm.pkg.github.com");
    assert.equal(packageJSON.repository.url, "git+https://github.com/idy/gizway.git");
  } finally { rmSync(temporary, { recursive: true, force: true }); }
});
