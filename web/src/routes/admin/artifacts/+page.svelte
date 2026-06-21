<script lang="ts">
  import { onMount } from "svelte";
  import { resolve } from "$app/paths";
  import { createClient } from "@connectrpc/connect";
  import { AdminService } from "$gen/proto/api/v1/api_pb";
  import type { TaskArtifact } from "$gen/proto/api/v1/api_pb";
  import { getTransport } from "$lib/api/client";
  import { formatTime } from "$lib/utils.svelte";

  type SortColumn = "type" | "artifact" | "task" | "ref" | "discovered";
  let artifacts = $state<TaskArtifact[]>([]);
  let loading = $state(true);
  let error = $state<string | null>(null);
  let taskId = $state("");
  let artifactType = $state("");
  let repo = $state("");
  let limit = $state(100);
  let offset = $state(0);
  let sortColumn = $state<SortColumn>("discovered");
  let sortDirection = $state<"asc" | "desc">("desc");

  let sortedArtifacts = $derived.by(() => {
    return [...artifacts].sort((a, b) => {
      let cmp = 0;
      switch (sortColumn) {
        case "type": cmp = a.artifactType.localeCompare(b.artifactType); break;
        case "artifact": cmp = `${a.repo}#${a.number}`.localeCompare(`${b.repo}#${b.number}`); break;
        case "task": cmp = a.taskId.localeCompare(b.taskId); break;
        case "ref": cmp = (a.ref || a.sha || "").localeCompare(b.ref || b.sha || ""); break;
        case "discovered": cmp = a.discoveredAt.localeCompare(b.discoveredAt); break;
      }
      return sortDirection === "asc" ? cmp : -cmp;
    });
  });

  let nextOffset = $derived(offset + artifacts.length);
  let prevOffset = $derived(Math.max(0, offset - limit));

  function toggleSort(col: SortColumn) {
    if (sortColumn === col) {
      sortDirection = sortDirection === "asc" ? "desc" : "asc";
    } else {
      sortColumn = col;
      sortDirection = col === "discovered" ? "desc" : "asc";
    }
  }

  function sortIcon(col: SortColumn): string {
    if (sortColumn !== col) return "↕";
    return sortDirection === "asc" ? "↑" : "↓";
  }

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
        offset,
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
</script>

<svelte:head>
  <title>Artifacts — Chetter</title>
</svelte:head>

<div class="p-6">
  <div class="flex items-center justify-between mb-6">
    <h1 class="text-2xl font-bold text-gray-900 dark:text-white">Task Artifacts</h1>
    <button onclick={() => { offset = 0; load(); }} class="px-4 py-2 bg-blue-600 hover:bg-blue-700 text-white text-sm font-medium rounded-lg">Refresh</button>
  </div>

  <div class="mb-6 bg-white dark:bg-gray-800 rounded-lg shadow-sm border border-gray-200 dark:border-gray-700 p-4">
    <div class="grid grid-cols-1 md:grid-cols-5 gap-3">
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
      <button onclick={() => { offset = 0; load(); }} class="px-4 py-2 bg-blue-600 hover:bg-blue-700 text-white text-sm font-medium rounded-lg">Search</button>
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
            <th onclick={() => toggleSort("type")} class="px-4 py-3 text-left text-xs font-medium text-gray-500 dark:text-gray-400 uppercase cursor-pointer hover:text-gray-700 dark:hover:text-gray-200 select-none">Type {sortIcon("type")}</th>
            <th onclick={() => toggleSort("artifact")} class="px-4 py-3 text-left text-xs font-medium text-gray-500 dark:text-gray-400 uppercase cursor-pointer hover:text-gray-700 dark:hover:text-gray-200 select-none">Artifact {sortIcon("artifact")}</th>
            <th onclick={() => toggleSort("task")} class="px-4 py-3 text-left text-xs font-medium text-gray-500 dark:text-gray-400 uppercase cursor-pointer hover:text-gray-700 dark:hover:text-gray-200 select-none">Task {sortIcon("task")}</th>
            <th onclick={() => toggleSort("ref")} class="px-4 py-3 text-left text-xs font-medium text-gray-500 dark:text-gray-400 uppercase cursor-pointer hover:text-gray-700 dark:hover:text-gray-200 select-none">Ref {sortIcon("ref")}</th>
            <th onclick={() => toggleSort("discovered")} class="px-4 py-3 text-left text-xs font-medium text-gray-500 dark:text-gray-400 uppercase cursor-pointer hover:text-gray-700 dark:hover:text-gray-200 select-none">Discovered {sortIcon("discovered")}</th>
          </tr>
        </thead>
        <tbody class="divide-y divide-gray-200 dark:divide-gray-700">
          {#each sortedArtifacts as artifact (artifact.id)}
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

    <div class="flex items-center justify-between mt-4 text-sm text-gray-500 dark:text-gray-400">
      <span>Showing {offset + 1}–{offset + artifacts.length} of {artifacts.length < limit ? offset + artifacts.length : `${offset + artifacts.length}+`}</span>
      <div class="flex gap-2">
        <button
          onclick={() => { offset = prevOffset; load(); }}
          disabled={offset === 0}
          class="px-3 py-1.5 border border-gray-300 dark:border-gray-600 rounded disabled:opacity-40 hover:bg-gray-100 dark:hover:bg-gray-700 disabled:cursor-not-allowed"
        >
          ← Prev
        </button>
        <button
          onclick={() => { offset = nextOffset; load(); }}
          disabled={artifacts.length < limit}
          class="px-3 py-1.5 border border-gray-300 dark:border-gray-600 rounded disabled:opacity-40 hover:bg-gray-100 dark:hover:bg-gray-700 disabled:cursor-not-allowed"
        >
          Next →
        </button>
      </div>
    </div>
  {/if}
</div>
