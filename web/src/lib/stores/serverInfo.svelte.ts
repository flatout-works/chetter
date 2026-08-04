let quotaExhausted = $state(false);
let gitHash = $state<string | null>(null);
let oidcEnabled = $state(false);

let interval: ReturnType<typeof setInterval> | null = null;
let loading: Promise<void> | null = null;

export function fetchServerInfo(): Promise<void> {
  if (!loading) {
    loading = (async () => {
      try {
        const res = await fetch("/api/server-info");
        const info = await res.json();
        if (info.gitHash && info.gitHash !== "unknown") {
          gitHash = info.gitHash;
        }
        quotaExhausted = !!info.quotaExhausted;
        oidcEnabled = !!info.oidcEnabled;
      } catch {
        // server unreachable — leave previous state
      } finally {
        loading = null;
      }
    })();
  }
  return loading;
}

/**
 * Fetches /api/server-info exactly once and resolves when it has completed.
 * Used by initAuth so the OIDC decision is never raced against an in-flight
 * server-info request.
 */
export function ensureServerInfoLoaded(): Promise<void> {
  return fetchServerInfo();
}

export function startServerInfoPolling() {
  fetchServerInfo();
  interval = setInterval(fetchServerInfo, 30_000);
}

export function stopServerInfoPolling() {
  if (interval) {
    clearInterval(interval);
    interval = null;
  }
}

export function getServerInfo() {
  return {
    get gitHash() { return gitHash; },
    get quotaExhausted() { return quotaExhausted; },
  };
}

export function getOIDCEnabled(): boolean {
  return oidcEnabled;
}
