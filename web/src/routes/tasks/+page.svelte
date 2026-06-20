<script lang="ts">
  import { refreshTasks, tasks } from "$lib/stores/tasks.svelte";

  let statusFilter = $state("");
  let taskList = $derived($tasks);

  const statusColors: Record<string, string> = {
    running: "bg-green-100 text-green-800 dark:bg-green-900/30 dark:text-green-400",
    pending: "bg-yellow-100 text-yellow-800 dark:bg-yellow-900/30 dark:text-yellow-400",
    done: "bg-blue-100 text-blue-800 dark:bg-blue-900/30 dark:text-blue-400",
    error: "bg-red-100 text-red-800 dark:bg-red-900/30 dark:text-red-400",
    cancelled: "bg-gray-100 text-gray-800 dark:bg-gray-700 dark:text-gray-400",
  };

  function applyFilter() {
    refreshTasks(statusFilter, 100);
  }
</script>

<svelte:head>
  <title>Tasks — Chetter</title>
</svelte:head>

<div class="p-6">
  <div class="flex items-center justify-between mb-6">
    <h1 class="text-2xl font-bold text-gray-900 dark:text-white">Tasks</h1>
    <select
      bind:value={statusFilter}
      onchange={applyFilter}
      class="px-3 py-2 border border-gray-300 dark:border-gray-600 rounded-lg bg-white dark:bg-gray-700 text-gray-900 dark:text-white text-sm"
    >
      <option value="">All statuses</option>
      <option value="running">Running</option>
      <option value="pending">Pending</option>
      <option value="done">Done</option>
      <option value="error">Error</option>
      <option value="cancelled">Cancelled</option>
    </select>
  </div>

  <div class="bg-white dark:bg-gray-800 rounded-lg shadow-sm border border-gray-200 dark:border-gray-700 overflow-hidden">
    <table class="w-full">
      <thead class="bg-gray-50 dark:bg-gray-700/50">
        <tr>
          <th class="px-4 py-3 text-left text-xs font-medium text-gray-500 dark:text-gray-400 uppercase">Task ID</th>
          <th class="px-4 py-3 text-left text-xs font-medium text-gray-500 dark:text-gray-400 uppercase">Status</th>
          <th class="px-4 py-3 text-left text-xs font-medium text-gray-500 dark:text-gray-400 uppercase">Agent</th>
          <th class="px-4 py-3 text-left text-xs font-medium text-gray-500 dark:text-gray-400 uppercase">Model</th>
          <th class="px-4 py-3 text-left text-xs font-medium text-gray-500 dark:text-gray-400 uppercase">Prompt</th>
        </tr>
      </thead>
      <tbody class="divide-y divide-gray-200 dark:divide-gray-700">
        {#each taskList as task (task.id)}
          <tr class="hover:bg-gray-50 dark:hover:bg-gray-700/50">
            <td class="px-4 py-3">
              <a href={`/tasks/${task.id}`} class="text-sm font-mono text-blue-600 dark:text-blue-400 hover:underline">
                {task.id.slice(0, 20)}…
              </a>
            </td>
            <td class="px-4 py-3">
              <span class={`px-2 py-0.5 rounded text-xs font-medium ${statusColors[task.status] || statusColors.pending}`}>
                {task.status}
              </span>
            </td>
            <td class="px-4 py-3 text-sm text-gray-700 dark:text-gray-300">{task.agent || "—"}</td>
            <td class="px-4 py-3 text-sm text-gray-700 dark:text-gray-300">{task.modelId || "—"}</td>
            <td class="px-4 py-3 text-sm text-gray-500 dark:text-gray-400 max-w-md truncate">
              {task.prompt.slice(0, 60)}{task.prompt.length > 60 ? "…" : ""}
            </td>
          </tr>
        {:else}
          <tr>
            <td colspan="5" class="px-4 py-8 text-center text-gray-500 dark:text-gray-400">No tasks found</td>
          </tr>
        {/each}
      </tbody>
    </table>
  </div>
</div>
