import { writable } from "svelte/store";
import { createClient } from "@connectrpc/connect";
import { TaskService, EventService } from "$gen/proto/api/v1/api_pb";
import type { TaskEvent, TaskProgressEntry } from "$gen/proto/api/v1/api_pb";
import { getTransport } from "$lib/api/client";

export const taskEvents = writable<TaskEvent[]>([]);
export const taskProgress = writable<TaskProgressEntry[]>([]);
export const taskProgressHasMore = writable(false);
export const streamConnected = writable(false);

let abortController: AbortController | null = null;
let progressOffset = 0;
const progressPageSize = 50;

export async function loadTaskEvents(taskId: string, limit = 100) {
  try {
    const client = createClient(EventService, getTransport());
    const resp = await client.getTaskEvents({ taskId, limit });
    taskEvents.set(resp.events);
    return resp.events;
  } catch (e) {
    console.error("Failed to load task events:", e);
    return [];
  }
}

export async function loadTaskProgress(taskId: string) {
  try {
    const client = createClient(EventService, getTransport());
    const resp = await client.getTaskProgress({ taskId, limit: progressPageSize });
    taskProgress.set(resp.entries);
    taskProgressHasMore.set(resp.hasMore);
    progressOffset = resp.nextOffset;
  } catch (e) {
    console.error("Failed to load task progress:", e);
  }
}

export async function loadOlderTaskProgress(taskId: string) {
  try {
    const client = createClient(EventService, getTransport());
    const resp = await client.getTaskProgress({ taskId, limit: progressPageSize, offset: progressOffset });
    taskProgress.update((entries) => [...resp.entries, ...entries]);
    taskProgressHasMore.set(resp.hasMore);
    progressOffset = resp.nextOffset;
  } catch (e) {
    console.error("Failed to load older task progress:", e);
  }
}

export async function refreshTaskProgress(taskId: string) {
  try {
    const client = createClient(EventService, getTransport());
    const resp = await client.getTaskProgress({ taskId, limit: Math.max(progressOffset, progressPageSize) });
    taskProgress.set(resp.entries);
    taskProgressHasMore.set(resp.hasMore);
    progressOffset = resp.nextOffset;
  } catch (e) {
    console.error("Failed to refresh task progress:", e);
  }
}

export function subscribeToTaskEvents(taskId: string, since: string, onTerminal?: () => void) {
  if (abortController) {
    abortController.abort();
  }

  abortController = new AbortController();

  const terminalStatuses = new Set(["done", "error", "cancelled"]);

  (async () => {
    try {
      streamConnected.set(true);
      const client = createClient(TaskService, getTransport());
      const stream = await client.subscribeTaskEvents({
        taskId,
        since,
      }, { signal: abortController.signal });

      for await (const event of stream) {
        if (event.status === "keepalive") continue;
        taskEvents.update((events) => {
          if (event.id && events.some((existing) => existing.id === event.id)) {
            return events;
          }
          return [...events, event];
        });
        if (terminalStatuses.has(event.status)) {
          onTerminal?.();
          break;
        }
      }
    } catch (e) {
      if (e instanceof Error && e.name !== "AbortError") {
        console.error("Stream error:", e);
      }
    } finally {
      streamConnected.set(false);
    }
  })();

  return () => {
    if (abortController) {
      abortController.abort();
      abortController = null;
    }
    streamConnected.set(false);
  };
}

export function clearTaskDetail() {
  taskEvents.set([]);
  taskProgress.set([]);
  taskProgressHasMore.set(false);
  progressOffset = 0;
  streamConnected.set(false);
  if (abortController) {
    abortController.abort();
    abortController = null;
  }
}
