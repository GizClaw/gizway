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
    const tarball = execFileSync("npm", ["pack", "--dry-run=false", "--ignore-scripts", "--json", "--pack-destination", temporary], { cwd: root, encoding: "utf8" });
    const packageMetadata = JSON.parse(tarball)[0];
    const filename = packageMetadata.filename;
    const sourcePackageJSON = JSON.parse(readFileSync(join(root, "package.json"), "utf8"));
    const lockfile = JSON.parse(readFileSync(join(root, "package-lock.json"), "utf8"));
    assert.equal(packageMetadata.version, sourcePackageJSON.version);
    assert.equal(lockfile.version, sourcePackageJSON.version);
    assert.equal(lockfile.packages[""].version, sourcePackageJSON.version);
    writeFileSync(join(temporary, "package.json"), JSON.stringify({ type: "module", scripts: { check: "tsc --noEmit && vite build" }, dependencies: { "@gizclaw/gizway": `file:./${filename}`, typescript: "5.9.3", vite: "8.2.2" } }));
    writeFileSync(join(temporary, "index.html"), '<script type="module" src="/src.ts"></script>\n');
    writeFileSync(join(temporary, "src.ts"), 'import { AuthenticationConfigurationError, createGizWayClient, type BrowserOAuthClient, type Region } from "@gizclaw/gizway"; const region: Region = "global"; const oauth: BrowserOAuthClient = {clientId:"consumer",redirectUri:"https://app.example.test/callback",postLogoutRedirectUri:"https://app.example.test/"}; void createGizWayClient({entryOrigin:"https://entry.example.test", region}); void createGizWayClient({entryOrigin:"https://entry.example.test", region, oauth}); void AuthenticationConfigurationError;\n');
    writeFileSync(join(temporary, "tsconfig.json"), JSON.stringify({ compilerOptions: { target: "ES2022", module: "ES2022", moduleResolution: "Bundler", strict: true, lib: ["ES2023", "DOM"] }, include: ["src.ts"] }));
    execFileSync("npm", ["install", "--dry-run=false", "--ignore-scripts", "--no-audit", "--no-fund"], { cwd: temporary, stdio: "pipe" });
    execFileSync("npm", ["run", "check"], { cwd: temporary, stdio: "pipe" });
    const packageJSON = JSON.parse(readFileSync(join(temporary, "node_modules/@gizclaw/gizway/package.json"), "utf8"));
    assert.equal(packageJSON.name, "@gizclaw/gizway");
    assert.equal(packageJSON.version, sourcePackageJSON.version);
    assert.equal(packageJSON.private, undefined);
    assert.deepEqual(packageJSON.files, ["dist", "package.json"]);
    assert.equal(packageJSON.publishConfig.access, "public");
    assert.equal(packageJSON.publishConfig.registry, "https://npm.pkg.github.com");
    assert.equal(packageJSON.repository.url, "git+https://github.com/GizClaw/gizway.git");
  } finally { rmSync(temporary, { recursive: true, force: true }); }
});
