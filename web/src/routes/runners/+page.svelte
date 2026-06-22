<script lang="ts">
  import { onMount } from "svelte";
  import { createClient } from "@connectrpc/connect";
  import { FleetService, TaskService } from "$gen/proto/api/v1/api_pb";
  import type { RunnerFleetHealth } from "$gen/proto/api/v1/api_pb";
  import { getTransport } from "$lib/api/client";
  import { addToast } from "$lib/stores/toast.svelte";
  import { confirm } from "$lib/stores/confirm.svelte";
  import { Button, Spinner, Badge } from "flowbite-svelte";

  let health = $state<RunnerFleetHealth | null>(null);
  let loading = $state(true);
  let clearing = $state(false);
  let clearError = $state<string | null>(null);

  async function clearQueue() {
    const ok = await confirm({
      title: "Clear Queue",
      message: `Cancel all ${health?.pendingTasks ?? ""} pending tasks? This cannot be undone.`,
      confirmLabel: "Clear Queue",
    });
    if (!ok) return;
    clearing = true;
    clearError = null;
    try {
      const client = createClient(TaskService, getTransport());
      await client.clearQueue({ confirm: true });
      addToast("Queue cleared", "success");
      await load();
    } catch (e) {
      clearError = e instanceof Error ? e.message : "Failed to clear queue.";
    } finally {
      clearing = false;
    }
  }

  async function load() {
    try {
      const client = createClient(FleetService, getTransport());
      const resp = await client.getRunnerHealth({ includeTasks: true });
      health = resp.health ?? null;
    } catch (e) {
      console.error(e);
    } finally {
      loading = false;
    }
  }

  onMount(load);

  function statusColor(status: string): "green" | "red" | "yellow" | "blue" | "gray" {
    switch (status) {
      case "running": return "green";
      case "pending": return "yellow";
      case "done": return "blue";
      case "error": return "red";
      case "cancelled": return "gray";
      default: return "gray";
    }
  }
</script>

<svelte:head>
  <title>Runners — Chetter</title>
</svelte:head>

<div class="p-6">
  <div class="flex items-center justify-between mb-6">
    <h1 class="text-2xl font-bold text-gray-900 dark:text-white">Runner Fleet</h1>
    {#if health && health.pendingTasks > 0}
      <Button color="red" disabled={clearing} onclick={clearQueue}>
        {clearing ? "Clearing…" : `Clear Queue (${health.pendingTasks} pending)`}
      </Button>
    {/if}
  </div>
  {#if clearError}
    <div class="mb-4 p-3 bg-red-50 dark:bg-red-900/20 text-red-700 dark:text-red-400 rounded-lg text-sm">{clearError}</div>
  {/if}

  {#if loading}
    <div class="flex items-center gap-2 text-gray-500 dark:text-gray-400">
      <Spinner size="4" /> Loading…
    </div>
  {:else if health}
    <div class="grid grid-cols-3 md:grid-cols-6 gap-4 mb-8">
      <div class="bg-white dark:bg-gray-800 rounded-lg p-4 border border-gray-200 dark:border-gray-700">
        <p class="text-sm text-gray-500 dark:text-gray-400">Total</p>
        <p class="text-2xl font-bold text-gray-900 dark:text-white">{health.totalTasks}</p>
      </div>
      <div class="bg-white dark:bg-gray-800 rounded-lg p-4 border border-gray-200 dark:border-gray-700">
        <p class="text-sm text-gray-500 dark:text-gray-400">Running</p>
        <p class="text-2xl font-bold text-green-600 dark:text-green-400">{health.runningTasks}</p>
      </div>
      <div class="bg-white dark:bg-gray-800 rounded-lg p-4 border border-gray-200 dark:border-gray-700">
        <p class="text-sm text-gray-500 dark:text-gray-400">Pending</p>
        <p class="text-2xl font-bold text-yellow-600 dark:text-yellow-400">{health.pendingTasks}</p>
      </div>
      <div class="bg-white dark:bg-gray-800 rounded-lg p-4 border border-gray-200 dark:border-gray-700">
        <p class="text-sm text-gray-500 dark:text-gray-400">Done</p>
        <p class="text-2xl font-bold text-blue-600 dark:text-blue-400">{health.doneTasks}</p>
      </div>
      <div class="bg-white dark:bg-gray-800 rounded-lg p-4 border border-gray-200 dark:border-gray-700">
        <p class="text-sm text-gray-500 dark:text-gray-400">Error</p>
        <p class="text-2xl font-bold text-red-600 dark:text-red-400">{health.errorTasks}</p>
      </div>
      <div class="bg-white dark:bg-gray-800 rounded-lg p-4 border border-gray-200 dark:border-gray-700">
        <p class="text-sm text-gray-500 dark:text-gray-400">Stale</p>
        <p class="text-2xl font-bold text-orange-600 dark:text-orange-400">{health.staleTasks}</p>
      </div>
    </div>

    <div class="bg-white dark:bg-gray-800 rounded-lg shadow-sm border border-gray-200 dark:border-gray-700 overflow-hidden mb-8">
      <div class="px-4 py-3 border-b border-gray-200 dark:border-gray-700">
        <h2 class="font-semibold text-gray-900 dark:text-white">Active Runners</h2>
      </div>
      <div class="divide-y divide-gray-200 dark:divide-gray-700">
        {#each health.runners as runner (runner.runnerId)}
          <div class="px-4 py-3">
            <div class="flex items-center justify-between">
              <div>
                <p class="text-sm font-mono font-medium text-gray-900 dark:text-white">{runner.runnerId}</p>
                <p class="text-xs text-gray-500 dark:text-gray-400 mt-0.5">
                  {runner.imageRef || "—"} · v{runner.version || "?"} · {runner.runningTasks}/{runner.maxConcurrent} tasks
                </p>
              </div>
              <div class="flex items-center gap-3">
                {#if runner.currentTaskIds?.length}
                  <div class="text-right">
                    {#each runner.currentTaskIds as tid}
                      <a href={`/tasks/${tid}`} class="block text-xs font-mono text-blue-600 dark:text-blue-400 hover:underline">
                        {tid.slice(0, 20)}…
                      </a>
                    {/each}
                  </div>
                {/if}
                <Badge color={statusColor(runner.status)}>{runner.status}</Badge>
              </div>
            </div>
          </div>
        {:else}
          <p class="px-4 py-8 text-center text-gray-500 dark:text-gray-400">No active runners</p>
        {/each}
      </div>
    </div>

    {#if health.runningTaskInfos?.length}
      <div class="bg-white dark:bg-gray-800 rounded-lg shadow-sm border border-gray-200 dark:border-gray-700 overflow-hidden">
        <div class="px-4 py-3 border-b border-gray-200 dark:border-gray-700">
          <h2 class="font-semibold text-gray-900 dark:text-white">Running Task Details</h2>
        </div>
        <div class="divide-y divide-gray-200 dark:divide-gray-700">
          {#each health.runningTaskInfos as info (info.taskId)}
            <a href={`/tasks/${info.taskId}`} class="block px-4 py-3 hover:bg-gray-50 dark:hover:bg-gray-700/50">
              <div class="flex items-center justify-between">
                <span class="text-sm font-mono text-blue-600 dark:text-blue-400">{info.taskId.slice(0, 24)}…</span>
                <div class="flex items-center gap-3">
                  {#if info.isStale}
                    <span class="text-xs text-orange-600 dark:text-orange-400">⚠ Stale</span>
                  {/if}
                  <span class="text-xs text-gray-500 dark:text-gray-400 truncate max-w-xs">{info.summary}</span>
                </div>
              </div>
            </a>
          {/each}
        </div>
      </div>
    {/if}
  {:else}
    <p class="text-gray-500 dark:text-gray-400">Failed to load fleet health</p>
  {/if}
</div>
