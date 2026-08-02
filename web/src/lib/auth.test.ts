// @vitest-environment jsdom

import { describe, expect, it, vi, beforeEach, afterEach } from "vitest";
import { checkSession, SESSION_ENDPOINT } from "./auth";

describe("checkSession", () => {
  const originalFetch = globalThis.fetch;

  beforeEach(() => {
    globalThis.fetch = vi.fn();
  });

  afterEach(() => {
    globalThis.fetch = originalFetch;
    vi.restoreAllMocks();
  });

  it("returns true when the session endpoint reports an active session", async () => {
    (globalThis.fetch as ReturnType<typeof vi.fn>).mockResolvedValue({
      ok: true,
      json: async () => ({ authenticated: true }),
    });

    await expect(checkSession()).resolves.toBe(true);
    expect(globalThis.fetch).toHaveBeenCalledWith(SESSION_ENDPOINT, { credentials: "same-origin" });
  });

  it("returns false when the session endpoint reports no session", async () => {
    (globalThis.fetch as ReturnType<typeof vi.fn>).mockResolvedValue({
      ok: true,
      json: async () => ({ authenticated: false }),
    });

    await expect(checkSession()).resolves.toBe(false);
  });

  it("returns false when the session endpoint returns 401", async () => {
    (globalThis.fetch as ReturnType<typeof vi.fn>).mockResolvedValue({
      ok: false,
      status: 401,
      json: async () => ({}),
    });

    await expect(checkSession()).resolves.toBe(false);
  });

  it("returns false when the server is unreachable", async () => {
    (globalThis.fetch as ReturnType<typeof vi.fn>).mockRejectedValue(new Error("network down"));

    await expect(checkSession()).resolves.toBe(false);
  });
});
