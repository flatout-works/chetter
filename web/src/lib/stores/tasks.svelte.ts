import { writable, get } from "svelte/store";
import { createClient } from "@connectrpc/connect";
import { TaskService, FleetService } from "$gen/proto/api/v1/api_pb";
import type { Task } from "$gen/proto/api/v1/api_pb";
import { getTransport } from "$lib/api/client";

export const tasks = writable<Task[]>([]);
export const fleetHealth = writable<{
  totalTasks: number;
  pendingTasks: number;
  runningTasks: number;
  staleTasks: number;
  doneTasks: number;
  errorTasks: number;
  fleetActive: boolean;
  runnerCount: number;
}>({
  totalTasks: 0,
  pendingTasks: 0,
  runningTasks: 0,
  staleTasks: 0,
  doneTasks: 0,
  errorTasks: 0,
  fleetActive: false,
  runnerCount: 0,
});

export const statusFilter = writable("");

let pollInterval: ReturnType<typeof setInterval> | null = null;
let fleetStream: AbortController | null = null;

export async function refreshTasks(status = "", limit = 100, search = "") {
  try {
    const client = createClient(TaskService, getTransport());
    const resp = await client.listTasks({ status, limit, ...(search ? { search } : {}) });
    tasks.set(resp.tasks);
  } catch (e) {
    console.error("Failed to refresh tasks:", e);
  }
}

export async function refreshFleetHealth() {
  try {
    const client = createClient(FleetService, getTransport());
    const resp = await client.getRunnerHealth({ includeTasks: false });
    const h = resp.health;
    if (h) {
      fleetHealth.set({
        totalTasks: h.totalTasks,
        pendingTasks: h.pendingTasks,
        runningTasks: h.runningTasks,
        staleTasks: h.staleTasks,
        doneTasks: h.doneTasks,
        errorTasks: h.errorTasks,
        fleetActive: h.fleetActive,
        runnerCount: h.runners.length,
      });
    }
  } catch (e) {
    console.error("Failed to refresh fleet health:", e);
  }
}

export function startLiveUpdates() {
  stopLiveUpdates();
  refreshTasks(get(statusFilter));
  refreshFleetHealth();
  pollInterval = setInterval(() => {
    refreshTasks(get(statusFilter));
    refreshFleetHealth();
  }, 5000);
}

export function stopLiveUpdates() {
  if (pollInterval) {
    clearInterval(pollInterval);
    pollInterval = null;
  }
  if (fleetStream) {
    fleetStream.abort();
    fleetStream = null;
  }
}
