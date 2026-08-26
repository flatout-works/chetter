let quotaExhausted = $state(false);
let gitHash = $state<string | null>(null);
let serverVersion = $state<string | null>(null);
let startedAt = $state<string | null>(null);
let uptimeSeconds = $state<number | null>(null);
let oidcEnabled = $state(false);
// Preserve compatibility with older servers that do not return the field.
let allowTokenLogin = $state(true);

let interval: ReturnType<typeof setInterval> | null = null;
let loading: Promise<void> | null = null;

export function fetchServerInfo(): Promise<void> {
  if (!loading) {
    loading = (async () => {
      try {
        const res = await fetch("/api/server-info", {
          credentials: "same-origin",
          cache: "no-store",
        });
		if (!res.ok) return;
        const info = await res.json();
        if (info.gitHash && info.gitHash !== "unknown") {
          gitHash = info.gitHash;
        }
        if (info.serverVersion && info.serverVersion !== "dev") {
          serverVersion = info.serverVersion;
        }
        if (info.startedAt) {
          startedAt = info.startedAt;
        }
        if (typeof info.uptimeSeconds === "number") {
          uptimeSeconds = info.uptimeSeconds;
        }
        quotaExhausted = !!info.quotaExhausted;
        oidcEnabled = !!info.oidcEnabled;
		if (typeof info.allowTokenLogin === "boolean") {
			allowTokenLogin = info.allowTokenLogin;
		}
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
    get serverVersion() { return serverVersion; },
    get startedAt() { return startedAt; },
    get uptimeSeconds() { return uptimeSeconds; },
    get quotaExhausted() { return quotaExhausted; },
  };
}

export function getOIDCEnabled(): boolean {
  return oidcEnabled;
}

export function getAllowTokenLogin(): boolean {
  return allowTokenLogin;
}
