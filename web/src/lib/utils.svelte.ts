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

export function humanReadableStatus(status: string, summary: string): string {
  switch (status) {
    case "pending":
      return "Task is queued and waiting for a runner";
    case "running":
      if (summary) return summary;
      return "Task is actively being worked on";
    case "done":
      return summary || "Task completed successfully";
    case "error":
      return summary || "Task encountered an error";
    case "cancelled":
      return summary || "Task was cancelled";
    case "claimed":
      return "Task has been claimed by a runner";
    case "submitted":
      return "Task was submitted to the runner";
    case "lease_renewed":
      return "Runner renewed lease on task";
    default:
      return summary || status;
  }
}
