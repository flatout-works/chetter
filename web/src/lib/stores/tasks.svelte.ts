import { writable, get } from "svelte/store";
import { createClient } from "@connectrpc/connect";
import { TaskService, FleetService } from "$gen/proto/api/v1/api_pb";
import type { Task } from "$gen/proto/api/v1/api_pb";
import { getTransport } from "$lib/api/client";
import { effectiveTeamIDs, effectiveRepos } from "$lib/stores/filter.svelte";

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

let pollTimeout: ReturnType<typeof setTimeout> | null = null;
let fleetStream: AbortController | null = null;
let taskRefreshGeneration = 0;
let fleetHealthRefreshGeneration = 0;
let liveUpdateGeneration = 0;

export async function refreshTasks(status = "", limit = 100, search = "") {
  const generation = ++taskRefreshGeneration;
  try {
    const client = createClient(TaskService, getTransport());
    const teamIds = effectiveTeamIDs();
    const repos = effectiveRepos();
    const resp = await client.listTasks({
      status, limit, ...(search ? { search } : {}),
      ...(teamIds.length > 0 ? { teamIds } : {}),
      ...(repos.length > 0 ? { repos } : {}),
    });
    if (generation === taskRefreshGeneration) {
      tasks.set(resp.tasks);
    }
  } catch (e) {
    console.error("Failed to refresh tasks:", e);
  }
}

export async function refreshFleetHealth() {
  const generation = ++fleetHealthRefreshGeneration;
  try {
    const client = createClient(FleetService, getTransport());
    const resp = await client.getRunnerHealth({ includeTasks: false });
    const h = resp.health;
    if (h && generation === fleetHealthRefreshGeneration) {
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
  const generation = liveUpdateGeneration;
  const refresh = async () => {
    if (generation !== liveUpdateGeneration) return;
    const started = Date.now();
    await Promise.all([refreshTasks(get(statusFilter)), refreshFleetHealth()]);
    if (generation === liveUpdateGeneration) {
      pollTimeout = setTimeout(refresh, Math.max(0, 5000 - (Date.now() - started)));
    }
  };
  void refresh();
}

export function stopLiveUpdates() {
  liveUpdateGeneration++;
  taskRefreshGeneration++;
  fleetHealthRefreshGeneration++;
  if (pollTimeout) clearTimeout(pollTimeout);
  pollTimeout = null;
  if (fleetStream) {
    fleetStream.abort();
    fleetStream = null;
  }
}
