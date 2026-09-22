// @vitest-environment jsdom

import { afterEach, beforeEach, describe, expect, it, vi } from "vitest";
import { clearToken, setToken } from "$lib/api/client";
import { fetchServerInfo } from "$lib/stores/serverInfo.svelte";

describe("fetchServerInfo", () => {
  const originalFetch = globalThis.fetch;

  beforeEach(() => {
    globalThis.fetch = vi.fn().mockResolvedValue({
      ok: true,
      json: async () => ({
        gitHash: "abc1234",
        serverVersion: "v1.2.3",
        startedAt: "2026-01-01T00:00:00Z",
        uptimeSeconds: 42,
      }),
    });
  });

  afterEach(() => {
    clearToken();
    globalThis.fetch = originalFetch;
    vi.restoreAllMocks();
  });

  it("sends the bearer token when loading authenticated server details", async () => {
    setToken("test-token");

    await fetchServerInfo();

    expect(globalThis.fetch).toHaveBeenCalledWith("/api/server-info", {
      credentials: "same-origin",
      cache: "no-store",
      headers: { Authorization: "Bearer test-token" },
    });
  });
});
