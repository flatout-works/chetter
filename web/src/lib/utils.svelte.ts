export function formatDuration(startedAt?: string | null, endedAt?: string | null): string {
  if (!startedAt) return "—";
  const start = new Date(startedAt).getTime();
  const end = endedAt ? new Date(endedAt).getTime() : Date.now();
  const diffMs = end - start;
  if (diffMs < 0) return "—";
  const seconds = Math.floor(diffMs / 1000);
  const minutes = Math.floor(seconds / 60);
  const hours = Math.floor(minutes / 60);
  if (hours > 0) return `${hours}h ${minutes % 60}m ${seconds % 60}s`;
  if (minutes > 0) return `${minutes}m ${seconds % 60}s`;
  return `${seconds}s`;
}

export function formatAge(ts: string): string {
  if (!ts) return "—";
  const diffMs = Date.now() - new Date(ts).getTime();
  if (diffMs < 0) return "—";
  const seconds = Math.floor(diffMs / 1000);
  const minutes = Math.floor(seconds / 60);
  const hours = Math.floor(minutes / 60);
  const days = Math.floor(hours / 24);
  if (days > 0) return `${days}d ${hours % 24}h`;
  if (hours > 0) return `${hours}h ${minutes % 60}m`;
  if (minutes > 0) return `${minutes}m ${seconds % 60}s`;
  return `${seconds}s`;
}

export function formatTime(ts: string): string {
  if (!ts) return "—";
  try {
    return new Date(ts).toLocaleString();
  } catch {
    return ts;
  }
}

export function formatTimeShort(ts: string): string {
  if (!ts) return "—";
  try {
    const d = new Date(ts);
    return d.toLocaleTimeString(undefined, { hour: "2-digit", minute: "2-digit", second: "2-digit" });
  } catch {
    return ts;
  }
}

export function humanReadableStatus(status: string, summary: string): string {
  if (summary && summary !== status) return summary;
  switch (status) {
    case "pending":
      return "Queued — waiting for an available runner to pick up the task";
    case "running":
      return "Running — agent is actively working";
    case "done":
      return "Completed successfully";
    case "error":
      return "Failed with an error";
    case "cancelled":
      return "Cancelled by user or trigger";
    case "claimed":
      return "Runner claimed the task and is preparing the environment";
    case "submitted":
      return "Task submitted to the runner harness";
    case "lease_renewed":
      return "Runner renewed its lease, still alive";
    case "tool_use":
      return "Agent is using a tool";
    case "model_response":
      return "Agent received a model response";
    case "progress":
      return summary || "Agent updated progress";
    case "opencode: server.heartbeat":
      return "Runner heartbeat — connection alive";
    default:
      return summary || status;
  }
}
