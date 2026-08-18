import { afterEach, describe, expect, test, vi } from "vitest";
import { GET } from "@/app/healthz/route";

describe("release health endpoint", () => {
  afterEach(() => vi.useRealTimers());

  test("returns process-only immutable build metadata", async () => {
    vi.useFakeTimers();
    vi.setSystemTime(new Date("2026-08-18T12:34:56.000Z"));
    const response = GET();
    expect(response.status).toBe(200);
    await expect(response.json()).resolves.toEqual({
      status: "healthy",
      service: "gizway-web",
      version: "devel",
      revision: "unknown",
      build_time: "unknown",
      server_time: "2026-08-18T12:34:56.000Z",
    });
  });
});
