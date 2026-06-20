<script lang="ts">
  import { onMount } from "svelte";
  import { createClient } from "@connectrpc/connect";
  import { FleetService } from "$gen/proto/api/v1/api_pb";
  import type { RunnerFleetHealth } from "$gen/proto/api/v1/api_pb";
  import { getTransport } from "$lib/api/client";

  let health = $state<RunnerFleetHealth | null>(null);
  let loading = $state(true);

  onMount(async () => {
    try {
      const client = createClient(FleetService, getTransport());
      const resp = await client.getRunnerHealth({ includeTasks: true });
      health = resp.health ?? null;
    } catch (e) {
      console.error(e);
    } finally {
      loading = false;
    }
  });

  const statusColors: Record<string, string> = {
    running: "bg-green-100 text-green-800 dark:bg-green-900/30 dark:text-green-400",
    pending: "bg-yellow-100 text-yellow-800 dark:bg-yellow-900/30 dark:text-yellow-400",
    done: "bg-blue-100 text-blue-800 dark:bg-blue-900/30 dark:text-blue-400",
    error: "bg-red-100 text-red-800 dark:bg-red-900/30 dark:text-red-400",
    cancelled: "bg-gray-100 text-gray-800 dark:bg-gray-700 dark:text-gray-400",
  };
</script>

<svelte:head>
  <title>Runners — Chetter</title>
</svelte:head>

<div class="p-6">
  <h1 class="text-2xl font-bold text-gray-900 dark:text-white mb-6">Runner Fleet</h1>

  {#if loading}
    <p class="text-gray-500 dark:text-gray-400">Loading…</p>
  {:else if health}
    <!-- Summary cards -->
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

    <!-- Runner list -->
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
                <span class={`px-2 py-0.5 rounded text-xs font-medium ${
                  runner.status === "running" ? statusColors.running : statusColors.pending
                }`}>
                  {runner.status}
                </span>
              </div>
            </div>
          </div>
        {:else}
          <p class="px-4 py-8 text-center text-gray-500 dark:text-gray-400">No active runners</p>
        {/each}
      </div>
    </div>

    <!-- Running task details -->
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
