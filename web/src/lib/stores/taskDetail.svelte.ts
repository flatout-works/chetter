import { writable } from "svelte/store";
import { createClient } from "@connectrpc/connect";
import { TaskService, EventService } from "$gen/proto/api/v1/api_pb";
import type { TaskEvent, TaskProgressEntry } from "$gen/proto/api/v1/api_pb";
import { getTransport } from "$lib/api/client";

export const taskEvents = writable<TaskEvent[]>([]);
export const taskProgress = writable<TaskProgressEntry[]>([]);
export const streamConnected = writable(false);

let abortController: AbortController | null = null;

export async function loadTaskEvents(taskId: string, limit = 100) {
  try {
    const client = createClient(EventService, getTransport());
    const resp = await client.getTaskEvents({ taskId, limit });
    taskEvents.set(resp.events);
  } catch (e) {
    console.error("Failed to load task events:", e);
  }
}

export async function loadTaskProgress(taskId: string) {
  try {
    const client = createClient(EventService, getTransport());
    const resp = await client.getTaskProgress({ taskId });
    taskProgress.set(resp.entries);
  } catch (e) {
    console.error("Failed to load task progress:", e);
  }
}

export function subscribeToTaskEvents(taskId: string) {
  if (abortController) {
    abortController.abort();
  }

  abortController = new AbortController();

  (async () => {
    try {
      streamConnected.set(true);
      const client = createClient(TaskService, getTransport());
      const stream = await client.subscribeTaskEvents({
        taskId,
        since: new Date().toISOString(),
      });

      for await (const event of stream) {
        taskEvents.update((events) => [...events, event]);
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
  streamConnected.set(false);
  if (abortController) {
    abortController.abort();
    abortController = null;
  }
}
