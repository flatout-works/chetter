<script lang="ts">
  import { onMount } from "svelte";
  import { resolve } from "$app/paths";
  import { createClient } from "@connectrpc/connect";
  import { AdminService } from "$gen/proto/api/v1/api_pb";
  import type { TaskArtifact } from "$gen/proto/api/v1/api_pb";
  import { getTransport } from "$lib/api/client";

  let artifacts = $state<TaskArtifact[]>([]);
  let loading = $state(true);
  let error = $state<string | null>(null);
  let taskId = $state("");
  let artifactType = $state("");
  let repo = $state("");
  let limit = $state(100);

  async function load() {
    loading = true;
    error = null;
    try {
      const client = createClient(AdminService, getTransport());
      const resp = await client.listTaskArtifacts({
        taskId: taskId.trim(),
        artifactType,
        repo: repo.trim(),
        limit,
      });
      artifacts = resp.artifacts ?? [];
    } catch (e) {
      error = e instanceof Error ? e.message : "Failed to load artifacts.";
      console.error(e);
    } finally {
      loading = false;
    }
  }

  onMount(load);

  function formatTime(ts: string): string {
    if (!ts) return "—";
    try {
      return new Date(ts).toLocaleString();
    } catch {
      return ts;
    }
  }
</script>

<svelte:head>
  <title>Artifacts — Chetter</title>
</svelte:head>

<div class="p-6">
  <div class="flex items-center justify-between mb-6">
    <h1 class="text-2xl font-bold text-gray-900 dark:text-white">Task Artifacts</h1>
    <button onclick={load} class="px-4 py-2 bg-blue-600 hover:bg-blue-700 text-white text-sm font-medium rounded-lg">Refresh</button>
  </div>

  <div class="mb-6 bg-white dark:bg-gray-800 rounded-lg shadow-sm border border-gray-200 dark:border-gray-700 p-4">
    <div class="grid grid-cols-1 md:grid-cols-4 gap-3">
      <input bind:value={taskId} placeholder="Task ID" class="px-3 py-2 border border-gray-300 dark:border-gray-600 rounded-lg bg-white dark:bg-gray-700 text-gray-900 dark:text-white text-sm" />
      <input bind:value={repo} placeholder="Repository" class="px-3 py-2 border border-gray-300 dark:border-gray-600 rounded-lg bg-white dark:bg-gray-700 text-gray-900 dark:text-white text-sm" />
      <select bind:value={artifactType} class="px-3 py-2 border border-gray-300 dark:border-gray-600 rounded-lg bg-white dark:bg-gray-700 text-gray-900 dark:text-white text-sm">
        <option value="">All artifact types</option>
        <option value="issue">Issue</option>
        <option value="pull_request">Pull Request</option>
        <option value="issue_comment">Issue Comment</option>
        <option value="pr_review">PR Review</option>
      </select>
      <input type="number" bind:value={limit} min="1" max="500" class="px-3 py-2 border border-gray-300 dark:border-gray-600 rounded-lg bg-white dark:bg-gray-700 text-gray-900 dark:text-white text-sm" />
    </div>
  </div>

  {#if error}
    <div class="mb-4 p-3 bg-red-50 dark:bg-red-900/20 text-red-700 dark:text-red-400 rounded-lg text-sm">{error}</div>
  {/if}

  {#if loading}
    <p class="text-gray-500 dark:text-gray-400">Loading…</p>
  {:else}
    <div class="bg-white dark:bg-gray-800 rounded-lg shadow-sm border border-gray-200 dark:border-gray-700 overflow-hidden">
      <table class="w-full">
        <thead class="bg-gray-50 dark:bg-gray-700/50">
          <tr>
            <th class="px-4 py-3 text-left text-xs font-medium text-gray-500 dark:text-gray-400 uppercase">Type</th>
            <th class="px-4 py-3 text-left text-xs font-medium text-gray-500 dark:text-gray-400 uppercase">Artifact</th>
            <th class="px-4 py-3 text-left text-xs font-medium text-gray-500 dark:text-gray-400 uppercase">Task</th>
            <th class="px-4 py-3 text-left text-xs font-medium text-gray-500 dark:text-gray-400 uppercase">Ref</th>
            <th class="px-4 py-3 text-left text-xs font-medium text-gray-500 dark:text-gray-400 uppercase">Discovered</th>
          </tr>
        </thead>
        <tbody class="divide-y divide-gray-200 dark:divide-gray-700">
          {#each artifacts as artifact (artifact.id)}
            <tr class="hover:bg-gray-50 dark:hover:bg-gray-700/50">
              <td class="px-4 py-3">
                <span class="px-2 py-0.5 rounded text-xs font-medium bg-purple-100 text-purple-800 dark:bg-purple-900/30 dark:text-purple-400">{artifact.artifactType}</span>
              </td>
              <td class="px-4 py-3 text-sm">
                {#if artifact.url}
                  <button onclick={() => window.open(artifact.url, "_blank", "noopener,noreferrer")} class="text-blue-600 dark:text-blue-400 hover:underline">
                    {artifact.repo}#{artifact.number || "?"}
                  </button>
                {:else}
                  <span class="text-gray-700 dark:text-gray-300">{artifact.repo}#{artifact.number || "?"}</span>
                {/if}
              </td>
              <td class="px-4 py-3">
                <a href={resolve("/tasks/[id]", { id: artifact.taskId })} class="text-sm font-mono text-blue-600 dark:text-blue-400 hover:underline">
                  {artifact.taskId.slice(0, 20)}…
                </a>
              </td>
              <td class="px-4 py-3 text-sm font-mono text-gray-500 dark:text-gray-400 max-w-xs truncate">{artifact.ref || artifact.sha || "—"}</td>
              <td class="px-4 py-3 text-sm text-gray-500 dark:text-gray-400 whitespace-nowrap">{formatTime(artifact.discoveredAt)}</td>
            </tr>
          {:else}
            <tr>
              <td colspan="5" class="px-4 py-8 text-center text-gray-500 dark:text-gray-400">No artifacts found</td>
            </tr>
          {/each}
        </tbody>
      </table>
    </div>
  {/if}
</div>
