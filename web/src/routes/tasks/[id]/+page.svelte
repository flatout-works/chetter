<script lang="ts">
  import { onMount, onDestroy } from "svelte";
  import { page } from "$app/stores";
  import { createClient } from "@connectrpc/connect";
  import { TaskService, AdminService } from "$gen/proto/api/v1/api_pb";
  import type { Task, TaskArtifact, TaskEvent } from "$gen/proto/api/v1/api_pb";
  import { getTransport } from "$lib/api/client";
  import {
    loadTaskEvents, loadTaskProgress, subscribeToTaskEvents,
    taskEvents, taskProgress, streamConnected, clearTaskDetail,
  } from "$lib/stores/taskDetail.svelte";
  import { formatDuration, formatTime, humanReadableStatus } from "$lib/utils.svelte";
  import { Badge, Modal, Button, Spinner } from "flowbite-svelte";
  import { marked } from "marked";

  let { params } = $props();
  let task = $state<Task | null>(null);
  let artifacts = $state<TaskArtifact[]>([]);
  let loading = $state(true);
  let error = $state<string | null>(null);
  let unsub: (() => void) | null = null;
  let now = $state(Date.now());
  let viewMarkdown = $state<string | null>(null);
  let viewLoading = $state(false);
  let showExportViewer = $state(false);

  function statusColor(status: string): "green" | "red" | "yellow" | "blue" | "gray" {
    const map: Record<string, "green" | "red" | "yellow" | "blue" | "gray"> = {
      running: "green", pending: "yellow", done: "blue", error: "red", cancelled: "gray",
    };
    return map[status] ?? "gray";
  }

  let events = $derived($taskEvents);
  let progress = $derived($taskProgress);
  let connected = $derived($streamConnected);

  let expandedEvents = $state<Set<string>>(new Set());

  function toggleEvent(id: string) {
    if (expandedEvents.has(id)) { expandedEvents.delete(id); }
    else { expandedEvents.add(id); }
    expandedEvents = new Set(expandedEvents);
  }

  let duration = $derived(now && formatDuration(task?.startedAt, task?.endedAt));

  let statusText = $derived.by(() => {
    if (!task) return "";
    if (task.status === "running" || task.status === "pending") return `${duration}`;
    if (task.status === "done") return `Completed in ${duration}`;
    if (task.status === "error") return `Failed after ${duration}`;
    if (task.status === "cancelled") return `Cancelled after ${duration}`;
    return "";
  });

  let timerInterval: ReturnType<typeof setInterval> | undefined;
  let progressRefreshCounter = $state(0);

  onMount(async () => {
    timerInterval = setInterval(() => {
      now = Date.now();
      progressRefreshCounter++;
      if (progressRefreshCounter % 5 === 0 && connected) {
        loadTaskProgress(params.id);
      }
    }, 1000);
    try {
      const streamSince = new Date().toISOString();
      const client = createClient(TaskService, getTransport());
      const resp = await client.getTask({ taskId: params.id });
      task = resp.task ?? null;
      loading = false;

      await loadTaskEvents(params.id, 100);
      await loadTaskProgress(params.id);

      if (task?.status === "done" || task?.status === "error" || task?.status === "cancelled") {
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
    if (timerInterval) clearInterval(timerInterval);
  });

  async function cancelTask() {
    try {
      const client = createClient(TaskService, getTransport());
      const resp = await client.cancelTask({ taskId: params.id, reason: "cancelled via web UI" });
      task = resp.task ?? null;
      if (unsub) { unsub(); unsub = null; }
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
      a.href = url; a.download = `${params.id}.md`; a.click();
      URL.revokeObjectURL(url);
    } catch (e) {
      error = e instanceof Error ? e.message : "Failed to export task";
    }
  }

  async function viewExport() {
    viewLoading = true;
    try {
      const client = createClient(TaskService, getTransport());
      const resp = await client.exportTask({ taskId: params.id });
      viewMarkdown = await marked.parse(resp.export, { breaks: true });
      showExportViewer = true;
    } catch (e) {
      error = e instanceof Error ? e.message : "Failed to load export";
    } finally { viewLoading = false; }
  }

  function closeView() {
    showExportViewer = false;
  }
</script>

<svelte:head>
  <title>Task {params.id.slice(0, 12)}… — Chetter</title>
</svelte:head>

{#if loading}
  <div class="p-6 flex items-center gap-3 text-gray-500 dark:text-gray-400">
    <Spinner size="5" />
    <span>Loading task…</span>
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
          <Badge color={statusColor(task.status)}>{task.status}</Badge>
          {#if statusText}
            <span class="text-xs text-gray-500 dark:text-gray-400 font-mono">({statusText})</span>
          {/if}
        </div>
        <p class="text-sm text-gray-500 dark:text-gray-400">
          Created {formatTime(task.createdAt)} · Updated {formatTime(task.updatedAt)}
        </p>
      </div>
      <div class="flex gap-2">
        {#if task.status === "running" || task.status === "pending"}
          <Button color="red" size="sm" onclick={cancelTask}>Cancel</Button>
        {/if}
        {#if task.status === "done" || task.status === "error" || task.status === "cancelled"}
          <Button size="sm" onclick={viewExport} disabled={viewLoading}>
            {viewLoading ? "Loading…" : "View"}
          </Button>
          <Button color="alternative" size="sm" onclick={exportTask}>Export</Button>
        {/if}
      </div>
    </div>

    <!-- Task metadata -->
    <div class="grid grid-cols-2 md:grid-cols-5 gap-4 mb-6">
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
      <div class="bg-white dark:bg-gray-800 rounded-lg p-3 border border-gray-200 dark:border-gray-700">
        <p class="text-xs text-gray-500 dark:text-gray-400 mb-1">Duration</p>
        <p class="text-sm font-medium text-gray-900 dark:text-white">{duration}</p>
      </div>
    </div>

    <!-- Prompt -->
    <div class="bg-white dark:bg-gray-800 rounded-lg p-4 border border-gray-200 dark:border-gray-700 mb-6">
      <h2 class="text-sm font-semibold text-gray-700 dark:text-gray-300 mb-2">Prompt</h2>
      <pre class="text-sm text-gray-600 dark:text-gray-400 whitespace-pre-wrap font-mono max-h-48 overflow-y-auto">{task.prompt}</pre>
    </div>

    {#if task.error}
      <div class="bg-red-50 dark:bg-red-900/20 text-red-700 dark:text-red-400 p-4 rounded-lg mb-6">
        <div class="flex items-center gap-2 mb-1">
          <h2 class="text-sm font-semibold">Error</h2>
          {#if task.errorCategory}
            <Badge color="red">{task.errorCategory}</Badge>
          {/if}
        </div>
        <p class="text-sm font-mono">{task.error}</p>
      </div>
    {/if}

    <!-- Artifacts -->
    {#if artifacts.length > 0}
      <div class="bg-white dark:bg-gray-800 rounded-lg p-4 border border-gray-200 dark:border-gray-700 mb-6">
        <h2 class="text-sm font-semibold text-gray-700 dark:text-gray-300 mb-3">GitHub Artifacts</h2>
        <div class="space-y-2">
          {#each artifacts as art (art.id)}
            <div class="flex items-center gap-3 text-sm">
              <Badge color={art.artifactType === "issue" ? "green" : art.artifactType === "pull_request" ? "purple" : "gray"}>
                {art.artifactType}
              </Badge>
              {#if art.url}
                <Button color="alternative" size="xs" onclick={() => window.open(art.url, "_blank", "noopener,noreferrer")}>
                  {art.repo}#{art.number}
                </Button>
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

    <!-- Progress Timeline -->
    {#if progress.length > 0}
      <div class="bg-white dark:bg-gray-800 rounded-lg p-4 border border-gray-200 dark:border-gray-700 mb-6">
        <h2 class="text-sm font-semibold text-gray-700 dark:text-gray-300 mb-3">Progress Timeline</h2>
        <div class="space-y-3">
          {#each progress as entry (`${entry.time}:${entry.status}:${entry.summary}`)}
            <div class="flex gap-3 text-sm items-start">
              <div class="flex flex-col items-center">
                <div class="w-2 h-2 rounded-full {entry.status === 'done' || entry.status === 'error' ? 'bg-blue-500' : entry.status === 'running' ? 'bg-green-500' : 'bg-gray-400'}"></div>
              </div>
              <div class="flex-1 min-w-0">
                <div class="flex gap-2 items-baseline">
                  <span class="text-gray-400 dark:text-gray-500 text-xs font-mono whitespace-nowrap">{formatTime(entry.time)}</span>
                  <Badge color={statusColor(entry.status)}>{entry.status}</Badge>
                </div>
                <p class="text-gray-600 dark:text-gray-400 text-sm mt-0.5">{humanReadableStatus(entry.status, entry.summary)}</p>
                {#if entry.error}
                  <p class="text-red-600 dark:text-red-400 text-xs mt-1 font-mono">{entry.error}</p>
                {/if}
              </div>
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
          <div class="border-b border-gray-100 dark:border-gray-700/50">
            <button
              onclick={() => toggleEvent(event.id)}
              class="w-full flex gap-2 py-1.5 text-left hover:bg-gray-50 dark:hover:bg-gray-700/30 px-1 rounded"
            >
              <span class="text-gray-400 dark:text-gray-500 whitespace-nowrap">{formatTime(event.createdAt)}</span>
              <span class="px-1 rounded font-medium {event.status === "keepalive" ? "text-gray-400" : "text-gray-600 dark:text-gray-300"}">
                {event.status}
              </span>
              {#if event.eventType && event.eventType !== "task." + event.status}
                <span class="text-gray-400 dark:text-gray-500 text-xs shrink-0">{event.eventType}</span>
              {/if}
              <span class="text-gray-500 dark:text-gray-400 flex-1 truncate">
                {event.payload?.slice(0, 120) || "—"}
              </span>
              <span class="text-gray-400 shrink-0">{expandedEvents.has(event.id) ? "▲" : "▼"}</span>
            </button>
            {#if expandedEvents.has(event.id) && event.payload}
              <pre class="ml-1 mb-2 px-3 py-2 bg-gray-50 dark:bg-gray-900/50 rounded text-gray-600 dark:text-gray-400 overflow-x-auto whitespace-pre-wrap max-h-64 overflow-y-auto">{event.payload}</pre>
            {/if}
          </div>
        {:else}
          <p class="text-gray-500 dark:text-gray-400 text-center py-4">No events</p>
        {/each}
      </div>
    </div>
  </div>
{/if}

<!-- Export viewer modal -->
<Modal title="Session Export" bind:open={showExportViewer} size="xl" onclose={closeView}>
  <div class="prose prose-sm dark:prose-invert max-w-none overflow-y-auto max-h-[70vh]">
    {@html viewMarkdown}
  </div>
</Modal>
