import { describe, expect, test } from "vitest";
import { readFile } from "node:fs/promises";

describe("Caddy health contract", () => {
  test("serves a process-only health endpoint without a Node handler", async () => {
    const source = await readFile(new URL("../../../../docker/gizway-web/Caddyfile", import.meta.url), "utf8");
    expect(source).toContain("respond /healthz 200");
    expect(source).not.toContain("reverse_proxy");
  });
});
