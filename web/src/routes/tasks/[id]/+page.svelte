<script lang="ts">
  import { onMount, onDestroy } from "svelte";
  import { page } from "$app/stores";
  import { createClient } from "@connectrpc/connect";
  import { TaskService, AdminService } from "$gen/proto/api/v1/api_pb";
  import type { Task, TaskArtifact } from "$gen/proto/api/v1/api_pb";
  import { getTransport } from "$lib/api/client";
  import {
    loadTaskEvents,
    loadTaskProgress,
    subscribeToTaskEvents,
    taskEvents,
    taskProgress,
    streamConnected,
    clearTaskDetail,
  } from "$lib/stores/taskDetail.svelte";

  let { params } = $props();
  let task = $state<Task | null>(null);
  let artifacts = $state<TaskArtifact[]>([]);
  let loading = $state(true);
  let error = $state<string | null>(null);
  let unsub: (() => void) | null = null;

  const statusColors: Record<string, string> = {
    running: "bg-green-100 text-green-800 dark:bg-green-900/30 dark:text-green-400",
    pending: "bg-yellow-100 text-yellow-800 dark:bg-yellow-900/30 dark:text-yellow-400",
    done: "bg-blue-100 text-blue-800 dark:bg-blue-900/30 dark:text-blue-400",
    error: "bg-red-100 text-red-800 dark:bg-red-900/30 dark:text-red-400",
    cancelled: "bg-gray-100 text-gray-800 dark:bg-gray-700 dark:text-gray-400",
  };

  let events = $derived($taskEvents);
  let progress = $derived($taskProgress);
  let connected = $derived($streamConnected);

  onMount(async () => {
    try {
      const streamSince = new Date().toISOString();
      const client = createClient(TaskService, getTransport());
      const resp = await client.getTask({ taskId: params.id });
      task = resp.task ?? null;
      loading = false;

      await loadTaskEvents(params.id, 100);
      await loadTaskProgress(params.id);

      // Load artifacts for completed tasks
      if (task?.status === "done" || task?.status === "error") {
        try {
          const adminClient = createClient(AdminService, getTransport());
          const artResp = await adminClient.listTaskArtifacts({ taskId: params.id });
          artifacts = artResp.artifacts ?? [];
        } catch { /* artifacts are admin-only; silently skip */ }
      }

      if (task?.status === "running" || task?.status === "pending") {
        unsub = subscribeToTaskEvents(params.id, streamSince);
      }
    } catch (e) {
      error = e instanceof Error ? e.message : "Failed to load task";
      loading = false;
    }
  });

  onDestroy(() => {
    clearTaskDetail();
    if (unsub) unsub();
  });

  async function cancelTask() {
    try {
      const client = createClient(TaskService, getTransport());
      const resp = await client.cancelTask({ taskId: params.id, reason: "cancelled via web UI" });
      task = resp.task ?? null;
      if (unsub) {
        unsub();
        unsub = null;
      }
    } catch (e) {
      error = e instanceof Error ? e.message : "Failed to cancel task";
    }
  }

  async function exportTask() {
    try {
      const client = createClient(TaskService, getTransport());
      const resp = await client.exportTask({ taskId: params.id });
      const blob = new Blob([resp.export], { type: "text/markdown" });
      const url = URL.createObjectURL(blob);
      const a = document.createElement("a");
      a.href = url;
      a.download = `${params.id}.md`;
      a.click();
      URL.revokeObjectURL(url);
    } catch (e) {
      error = e instanceof Error ? e.message : "Failed to export task";
    }
  }

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
  <title>Task {params.id.slice(0, 12)}… — Chetter</title>
</svelte:head>

{#if loading}
  <div class="p-6">
    <p class="text-gray-500 dark:text-gray-400">Loading…</p>
  </div>
{:else if error}
  <div class="p-6">
    <div class="bg-red-50 dark:bg-red-900/20 text-red-700 dark:text-red-400 p-4 rounded-lg">{error}</div>
  </div>
{:else if task}
  <div class="p-6 max-w-6xl">
    <!-- Header -->
    <div class="flex items-center justify-between mb-6">
      <div>
        <div class="flex items-center gap-3 mb-1">
          <h1 class="text-xl font-mono font-bold text-gray-900 dark:text-white">{task.id}</h1>
          <span class={`px-2 py-0.5 rounded text-xs font-medium ${statusColors[task.status] || statusColors.pending}`}>
            {task.status}
          </span>
        </div>
        <p class="text-sm text-gray-500 dark:text-gray-400">
          Created {formatTime(task.createdAt)} · Updated {formatTime(task.updatedAt)}
        </p>
      </div>
      <div class="flex gap-2">
        {#if task.status === "running" || task.status === "pending"}
          <button
            onclick={cancelTask}
            class="px-3 py-1.5 text-sm bg-red-600 hover:bg-red-700 text-white rounded-lg font-medium"
          >
            Cancel
          </button>
        {/if}
        {#if task.status === "done" || task.status === "error" || task.status === "cancelled"}
          <button
            onclick={exportTask}
            class="px-3 py-1.5 text-sm bg-gray-600 hover:bg-gray-700 text-white rounded-lg font-medium"
          >
            Export
          </button>
        {/if}
      </div>
    </div>

    <!-- Task metadata -->
    <div class="grid grid-cols-2 md:grid-cols-4 gap-4 mb-6">
      <div class="bg-white dark:bg-gray-800 rounded-lg p-3 border border-gray-200 dark:border-gray-700">
        <p class="text-xs text-gray-500 dark:text-gray-400 mb-1">Agent</p>
        <p class="text-sm font-medium text-gray-900 dark:text-white">{task.agent || "default"}</p>
      </div>
      <div class="bg-white dark:bg-gray-800 rounded-lg p-3 border border-gray-200 dark:border-gray-700">
        <p class="text-xs text-gray-500 dark:text-gray-400 mb-1">Model</p>
        <p class="text-sm font-medium text-gray-900 dark:text-white">{task.modelId || "default"}</p>
      </div>
      <div class="bg-white dark:bg-gray-800 rounded-lg p-3 border border-gray-200 dark:border-gray-700">
        <p class="text-xs text-gray-500 dark:text-gray-400 mb-1">Image</p>
        <p class="text-sm font-medium text-gray-900 dark:text-white truncate">{task.agentImage || "default"}</p>
      </div>
      <div class="bg-white dark:bg-gray-800 rounded-lg p-3 border border-gray-200 dark:border-gray-700">
        <p class="text-xs text-gray-500 dark:text-gray-400 mb-1">Timeout</p>
        <p class="text-sm font-medium text-gray-900 dark:text-white">{task.timeoutSec}s</p>
      </div>
    </div>

    <!-- Prompt -->
    <div class="bg-white dark:bg-gray-800 rounded-lg p-4 border border-gray-200 dark:border-gray-700 mb-6">
      <h2 class="text-sm font-semibold text-gray-700 dark:text-gray-300 mb-2">Prompt</h2>
      <pre class="text-sm text-gray-600 dark:text-gray-400 whitespace-pre-wrap font-mono max-h-48 overflow-y-auto">{task.prompt}</pre>
    </div>

    {#if task.error}
      <div class="bg-red-50 dark:bg-red-900/20 text-red-700 dark:text-red-400 p-4 rounded-lg mb-6">
        <h2 class="text-sm font-semibold mb-1">Error</h2>
        <p class="text-sm font-mono">{task.error}</p>
      </div>
    {/if}

    <!-- Progress timeline -->
    {#if progress.length > 0}
      <div class="bg-white dark:bg-gray-800 rounded-lg p-4 border border-gray-200 dark:border-gray-700 mb-6">
        <h2 class="text-sm font-semibold text-gray-700 dark:text-gray-300 mb-3">Progress Timeline</h2>
        <div class="space-y-2">
          {#each progress as entry (`${entry.time}:${entry.status}:${entry.summary}`)}
            <div class="flex gap-3 text-sm">
              <span class="text-gray-400 dark:text-gray-500 font-mono text-xs whitespace-nowrap pt-0.5">
                {formatTime(entry.time)}
              </span>
              <span class={`px-1.5 py-0.5 rounded text-xs font-medium ${statusColors[entry.status] || statusColors.pending}`}>
                {entry.status}
              </span>
              <span class="text-gray-600 dark:text-gray-400 flex-1 truncate">{entry.summary}</span>
            </div>
          {/each}
        </div>
      </div>
    {/if}

    <!-- Artifacts -->
    {#if artifacts.length > 0}
      <div class="bg-white dark:bg-gray-800 rounded-lg p-4 border border-gray-200 dark:border-gray-700 mb-6">
        <h2 class="text-sm font-semibold text-gray-700 dark:text-gray-300 mb-3">GitHub Artifacts</h2>
        <div class="space-y-2">
          {#each artifacts as art (art.id)}
            <div class="flex items-center gap-3 text-sm">
              <span class={`px-2 py-0.5 rounded text-xs font-medium ${
                art.artifactType === "issue" ? "bg-green-100 text-green-800 dark:bg-green-900/30 dark:text-green-400" :
                art.artifactType === "pull_request" ? "bg-purple-100 text-purple-800 dark:bg-purple-900/30 dark:text-purple-400" :
                "bg-gray-100 text-gray-800 dark:bg-gray-700 dark:text-gray-300"
              }`}>
                {art.artifactType}
              </span>
              {#if art.url}
                <button onclick={() => window.open(art.url, "_blank", "noopener,noreferrer")} class="text-blue-600 dark:text-blue-400 hover:underline">
                  {art.repo}#{art.number}
                </button>
              {:else}
                <span class="text-gray-700 dark:text-gray-300">{art.repo}#{art.number}</span>
              {/if}
              {#if art.ref}
                <span class="text-gray-400 dark:text-gray-500 font-mono text-xs">{art.ref}</span>
              {/if}
            </div>
          {/each}
        </div>
      </div>
    {/if}

    <!-- Live event stream -->
    <div class="bg-white dark:bg-gray-800 rounded-lg p-4 border border-gray-200 dark:border-gray-700">
      <div class="flex items-center justify-between mb-3">
        <h2 class="text-sm font-semibold text-gray-700 dark:text-gray-300">Event Stream</h2>
        {#if connected}
          <span class="flex items-center gap-1.5 text-xs text-green-600 dark:text-green-400">
            <span class="w-2 h-2 bg-green-500 rounded-full animate-pulse"></span>
            Live
          </span>
        {/if}
      </div>
      <div class="space-y-1 max-h-96 overflow-y-auto font-mono text-xs">
        {#each events as event (event.id || `${event.createdAt}:${event.status}:${event.payload}`)}
          <div class="flex gap-2 py-1 border-b border-gray-100 dark:border-gray-700/50">
            <span class="text-gray-400 dark:text-gray-500 whitespace-nowrap">{formatTime(event.createdAt)}</span>
            <span class={`px-1 rounded font-medium ${
              event.status === "keepalive" ? "text-gray-400" : "text-gray-600 dark:text-gray-300"
            }`}>
              {event.status}
            </span>
            <span class="text-gray-500 dark:text-gray-400 flex-1 truncate">{event.payload?.slice(0, 200)}</span>
          </div>
        {:else}
          <p class="text-gray-500 dark:text-gray-400 text-center py-4">No events</p>
        {/each}
      </div>
    </div>
  </div>
{/if}
