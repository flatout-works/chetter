<script lang="ts">
  import { resolve } from "$app/paths";
  import { createClient } from "@connectrpc/connect";
  import { TaskService } from "$gen/proto/api/v1/api_pb";
  import { getTransport } from "$lib/api/client";
  import { refreshTasks, tasks } from "$lib/stores/tasks.svelte";

  let statusFilter = $state("");
  let taskList = $derived($tasks);
  let showSubmitForm = $state(false);
  let submitting = $state(false);
  let formError = $state<string | null>(null);
  let prompt = $state("");
  let gitUrl = $state("");
  let gitRef = $state("");
  let agent = $state("");
  let modelId = $state("");

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

  async function submitTask(e: Event) {
    e.preventDefault();
    formError = null;
    if (!prompt.trim()) {
      formError = "Prompt is required.";
      return;
    }
    submitting = true;
    try {
      const client = createClient(TaskService, getTransport());
      await client.submitTask({
        prompt: prompt.trim(),
        gitUrl: gitUrl.trim(),
        gitRef: gitRef.trim(),
        agent: agent.trim(),
        modelId: modelId.trim(),
      });
      prompt = "";
      gitUrl = "";
      gitRef = "";
      agent = "";
      modelId = "";
      showSubmitForm = false;
      await refreshTasks(statusFilter, 100);
    } catch (err) {
      formError = err instanceof Error ? err.message : "Failed to submit task.";
    } finally {
      submitting = false;
    }
  }
</script>

<svelte:head>
  <title>Tasks — Chetter</title>
</svelte:head>

<div class="p-6">
  <div class="flex items-center justify-between mb-6">
    <h1 class="text-2xl font-bold text-gray-900 dark:text-white">Tasks</h1>
    <div class="flex items-center gap-3">
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
      <button
        onclick={() => { showSubmitForm = !showSubmitForm; formError = null; }}
        class="px-4 py-2 bg-blue-600 hover:bg-blue-700 text-white text-sm font-medium rounded-lg"
      >
        {showSubmitForm ? "Cancel" : "Submit Task"}
      </button>
    </div>
  </div>

  {#if showSubmitForm}
    <form onsubmit={submitTask} class="mb-6 bg-white dark:bg-gray-800 rounded-lg shadow-sm border border-gray-200 dark:border-gray-700 p-4 space-y-4">
      <div>
        <label for="task-prompt" class="block text-sm font-medium text-gray-700 dark:text-gray-300 mb-1">Prompt</label>
        <textarea
          id="task-prompt"
          bind:value={prompt}
          rows="4"
          placeholder="Describe the task for the agent"
          class="w-full px-3 py-2 border border-gray-300 dark:border-gray-600 rounded-lg bg-white dark:bg-gray-700 text-gray-900 dark:text-white text-sm"
        ></textarea>
      </div>
      <div class="grid grid-cols-1 md:grid-cols-2 gap-3">
        <input bind:value={gitUrl} placeholder="Git URL (optional)" class="px-3 py-2 border border-gray-300 dark:border-gray-600 rounded-lg bg-white dark:bg-gray-700 text-gray-900 dark:text-white text-sm" />
        <input bind:value={gitRef} placeholder="Git ref (optional)" class="px-3 py-2 border border-gray-300 dark:border-gray-600 rounded-lg bg-white dark:bg-gray-700 text-gray-900 dark:text-white text-sm" />
        <input bind:value={agent} placeholder="Agent (optional)" class="px-3 py-2 border border-gray-300 dark:border-gray-600 rounded-lg bg-white dark:bg-gray-700 text-gray-900 dark:text-white text-sm" />
        <input bind:value={modelId} placeholder="Model ID (optional)" class="px-3 py-2 border border-gray-300 dark:border-gray-600 rounded-lg bg-white dark:bg-gray-700 text-gray-900 dark:text-white text-sm" />
      </div>
      {#if formError}
        <p class="text-sm text-red-600 dark:text-red-400">{formError}</p>
      {/if}
      <button
        type="submit"
        disabled={submitting}
        class="px-4 py-2 bg-blue-600 hover:bg-blue-700 disabled:bg-blue-400 text-white text-sm font-medium rounded-lg"
      >
        {submitting ? "Submitting…" : "Submit"}
      </button>
    </form>
  {/if}

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
              <a href={resolve("/tasks/[id]", { id: task.id })} class="text-sm font-mono text-blue-600 dark:text-blue-400 hover:underline">
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
