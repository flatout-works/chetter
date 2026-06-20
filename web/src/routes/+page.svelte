<script lang="ts">
  import { onMount, onDestroy } from "svelte";
  import { tasks, fleetHealth } from "$lib/stores/tasks.svelte";

  let health = $derived($fleetHealth);
  let taskList = $derived($tasks);

  const statusColors: Record<string, string> = {
    running: "bg-green-100 text-green-800 dark:bg-green-900/30 dark:text-green-400",
    pending: "bg-yellow-100 text-yellow-800 dark:bg-yellow-900/30 dark:text-yellow-400",
    done: "bg-blue-100 text-blue-800 dark:bg-blue-900/30 dark:text-blue-400",
    error: "bg-red-100 text-red-800 dark:bg-red-900/30 dark:text-red-400",
    cancelled: "bg-gray-100 text-gray-800 dark:bg-gray-700 dark:text-gray-400",
  };

  const cards = $derived([
    { label: "Running", value: health.runningTasks, color: "text-green-600 dark:text-green-400" },
    { label: "Pending", value: health.pendingTasks, color: "text-yellow-600 dark:text-yellow-400" },
    { label: "Done", value: health.doneTasks, color: "text-blue-600 dark:text-blue-400" },
    { label: "Error", value: health.errorTasks, color: "text-red-600 dark:text-red-400" },
    { label: "Stale", value: health.staleTasks, color: "text-orange-600 dark:text-orange-400" },
    { label: "Fleet Active", value: health.fleetActive ? "Yes" : "No", color: health.fleetActive ? "text-green-600 dark:text-green-400" : "text-red-600 dark:text-red-400" },
  ]);
</script>

<svelte:head>
  <title>Dashboard — Chetter</title>
</svelte:head>

<div class="p-6">
  <h1 class="text-2xl font-bold text-gray-900 dark:text-white mb-6">Dashboard</h1>

  <!-- Fleet health cards -->
  <div class="grid grid-cols-2 md:grid-cols-3 lg:grid-cols-6 gap-4 mb-8">
    {#each cards as card}
      <div class="bg-white dark:bg-gray-800 rounded-lg p-4 shadow-sm border border-gray-200 dark:border-gray-700">
        <p class="text-sm text-gray-500 dark:text-gray-400 mb-1">{card.label}</p>
        <p class={`text-2xl font-bold ${card.color}`}>{card.value}</p>
      </div>
    {/each}
  </div>

  <!-- Recent tasks -->
  <div class="bg-white dark:bg-gray-800 rounded-lg shadow-sm border border-gray-200 dark:border-gray-700 overflow-hidden">
    <div class="px-4 py-3 border-b border-gray-200 dark:border-gray-700">
      <h2 class="font-semibold text-gray-900 dark:text-white">Recent Tasks</h2>
    </div>
    <div class="divide-y divide-gray-200 dark:divide-gray-700">
      {#each taskList.slice(0, 15) as task (task.id)}
        <a href={`/tasks/${task.id}`} class="block px-4 py-3 hover:bg-gray-50 dark:hover:bg-gray-700/50">
          <div class="flex items-center justify-between gap-4">
            <div class="flex-1 min-w-0">
              <p class="text-sm font-medium text-gray-900 dark:text-white truncate">
                {task.prompt.slice(0, 80)}{task.prompt.length > 80 ? "…" : ""}
              </p>
              <p class="text-xs text-gray-500 dark:text-gray-400 mt-0.5">
                {task.id} · {task.agent || "default"} · {task.modelId || "default"}
              </p>
            </div>
            <span class={`px-2 py-0.5 rounded text-xs font-medium ${statusColors[task.status] || statusColors.pending}`}>
              {task.status}
            </span>
          </div>
        </a>
      {:else}
        <p class="px-4 py-8 text-center text-gray-500 dark:text-gray-400">No tasks found</p>
      {/each}
    </div>
  </div>
</div>
