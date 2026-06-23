let quotaExhausted = $state(false);
let gitHash = $state<string | null>(null);

let interval: ReturnType<typeof setInterval> | null = null;

async function fetchServerInfo() {
  try {
    const res = await fetch("/api/server-info");
    const info = await res.json();
    if (info.gitHash && info.gitHash !== "unknown") {
      gitHash = info.gitHash;
    }
    quotaExhausted = !!info.quotaExhausted;
  } catch {
    // server unreachable — leave previous state
  }
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
