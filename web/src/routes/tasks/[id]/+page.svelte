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
  import StatusBadge from "$lib/components/StatusBadge.svelte";
  import { Alert, Badge, Button, Card, Modal, Spinner } from "flowbite-svelte";
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

  let events = $derived($taskEvents);
  let progress = $derived($taskProgress);
  let connected = $derived($streamConnected);

  // Extract session ID from event payloads for navigation to session page
  let sessionId = $derived.by(() => {
    for (const ev of events) {
      try {
        const payload = JSON.parse(ev.payload);
        if (payload.sessionID) return payload.sessionID;
      } catch {}
    }
    return null;
  });

  let expandedProgress = $state<Set<number>>(new Set());

  function toggleProgress(i: number) {
    if (expandedProgress.has(i)) { expandedProgress.delete(i); }
    else { expandedProgress.add(i); }
    expandedProgress = new Set(expandedProgress);
  }

  // Events sorted chronologically (oldest first) for matching with progress entries
  let eventsChrono = $derived(
    [...events].sort((a, b) => a.createdAt.localeCompare(b.createdAt))
  );

  // Build merged timeline: progress entries with their matching raw events
  let mergedTimeline = $derived.by(() => {
    if (progress.length === 0 && events.length === 0) return [];
    // Start with progress entries
    const result = progress.map((entry, i) => ({
      type: "progress" as const,
      time: entry.time,
      status: entry.status,
      summary: entry.summary,
      error: entry.error,
      rawEvents: [] as typeof events,
      index: i,
    }));
    // Add any raw events that don't correspond to existing progress entries
    // (events that happened between progress timestamps or after the last one)
    for (const ev of eventsChrono) {
      // Find the nearest progress entry by time proximity
      if (result.length === 0) {
        // No progress entries yet, skip raw-only events
        continue;
      }
      // Find which progress entry this event belongs to (by time window)
      let closest = 0;
      let closestDiff = Infinity;
      for (let i = 0; i < result.length; i++) {
        const diff = Math.abs(new Date(ev.createdAt).getTime() - new Date(result[i].time).getTime());
        if (diff < closestDiff) {
          closestDiff = diff;
          closest = i;
        }
      }
      // Only attach if within 10 seconds of the progress entry
      if (closestDiff < 10000) {
        result[closest].rawEvents.push(ev);
      }
    }
    return result;
  });

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
  <div class="p-6">
    <!-- Header -->
    <div class="flex items-center justify-between mb-6">
      <div>
        <div class="flex items-center gap-3 mb-1">
          <h1 class="text-xl font-mono font-bold text-gray-900 dark:text-white">{task.id}</h1>
          <StatusBadge status={task.status} />
          {#if statusText}
            <span class="text-xs text-gray-500 dark:text-gray-400 font-mono">({statusText})</span>
          {/if}
        </div>
        <p class="text-sm text-gray-500 dark:text-gray-400">
          Created {formatTime(task.createdAt)} · Updated {formatTime(task.updatedAt)}
          {#if sessionId}
            · <a href={`/sessions/${sessionId}`} class="text-blue-600 dark:text-blue-400 hover:underline">View Session</a>
          {/if}
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
      <Card size="sm" shadow="sm">
        <p class="text-xs text-gray-500 dark:text-gray-400 mb-1">Agent</p>
        <p class="text-sm font-medium text-gray-900 dark:text-white">{task.agent || "default"}</p>
      </Card>
      <Card size="sm" shadow="sm">
        <p class="text-xs text-gray-500 dark:text-gray-400 mb-1">Model</p>
        <p class="text-sm font-medium text-gray-900 dark:text-white">{task.modelId || "default"}</p>
      </Card>
      <Card size="sm" shadow="sm">
        <p class="text-xs text-gray-500 dark:text-gray-400 mb-1">Image</p>
        <p class="text-sm font-medium text-gray-900 dark:text-white truncate">{task.agentImage || "default"}</p>
      </Card>
      <Card size="sm" shadow="sm">
        <p class="text-xs text-gray-500 dark:text-gray-400 mb-1">Timeout</p>
        <p class="text-sm font-medium text-gray-900 dark:text-white">{task.timeoutSec}s</p>
      </Card>
      <Card size="sm" shadow="sm">
        <p class="text-xs text-gray-500 dark:text-gray-400 mb-1">Duration</p>
        <p class="text-sm font-medium text-gray-900 dark:text-white">{duration}</p>
      </Card>
    </div>

    <!-- Prompt -->
    <Card class="mb-6" shadow="sm">
      <h2 class="text-sm font-semibold text-gray-700 dark:text-gray-300 mb-2">Prompt</h2>
      <pre class="text-sm text-gray-600 dark:text-gray-400 whitespace-pre-wrap font-mono max-h-48 overflow-y-auto">{task.prompt}</pre>
    </Card>

    {#if task.error}
      <Alert color="red" class="mb-6">
        <div class="flex items-center gap-2 mb-1">
          <h2 class="text-sm font-semibold">Error</h2>
          {#if task.errorCategory}
            <Badge color="red">{task.errorCategory}</Badge>
          {/if}
        </div>
        <p class="text-sm font-mono">{task.error}</p>
      </Alert>
    {/if}

    <!-- Artifacts -->
    {#if artifacts.length > 0}
      <Card class="mb-6" shadow="sm">
        <h2 class="text-sm font-semibold text-gray-700 dark:text-gray-300 mb-3">GitHub Artifacts</h2>
        <div class="space-y-2">
          {#each artifacts as art (art.id)}
            <div class="flex items-center gap-3 text-sm">
              <StatusBadge status={art.artifactType === "pr_review" ? "pr_review_artifact" : art.artifactType} label={art.artifactType} />
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
      </Card>
    {/if}

    <!-- Merged Progress Timeline (with expandable raw event details) -->
    {#if mergedTimeline.length > 0}
      <Card class="mb-6" shadow="sm">
        <div class="flex items-center justify-between">
          <h2 class="text-sm font-semibold text-gray-700 dark:text-gray-300">Timeline</h2>
          {#if connected}
            <span class="flex items-center gap-1.5 text-xs text-green-600 dark:text-green-400">
              <span class="w-2 h-2 bg-green-500 rounded-full animate-pulse"></span>
              Live
            </span>
          {/if}
        </div>
        <div class="space-y-1 max-h-[32rem] overflow-y-auto font-mono text-xs mt-3 -mx-4 px-4">
          {#each mergedTimeline as entry, i (entry.time + entry.summary)}
            <div class="border-b border-gray-100 dark:border-gray-700/50">
              <button
                onclick={() => toggleProgress(i)}
                class="w-full flex gap-2 py-1.5 text-left hover:bg-gray-50 dark:hover:bg-gray-700/30 px-1 rounded items-start"
              >
                <span class="text-gray-400 dark:text-gray-500 whitespace-nowrap shrink-0 mt-0.5">{formatTime(entry.time)}</span>
                <span class="w-2 h-2 rounded-full shrink-0 mt-1.5 {entry.status === 'done' || entry.status === 'error' ? 'bg-blue-500' : entry.status === 'running' ? 'bg-green-500' : 'bg-gray-400'}"></span>
                <span class="px-1 rounded font-medium shrink-0 {entry.status === 'keepalive' ? 'text-gray-400' : 'text-gray-600 dark:text-gray-300'}">
                  {entry.status}
                </span>
                <span class="text-gray-500 dark:text-gray-400 flex-1 truncate text-left">
                  {humanReadableStatus(entry.status, entry.summary)}
                </span>
                {#if entry.error}
                  <span class="text-red-500 shrink-0">Error</span>
                {/if}
                <span class="text-gray-400 shrink-0">{expandedProgress.has(i) ? "▲" : "▼"}</span>
              </button>
              {#if expandedProgress.has(i)}
                <div class="ml-1 mb-2 px-3 py-2 bg-gray-50 dark:bg-gray-900/50 rounded space-y-1">
                  {#if entry.error}
                    <pre class="text-red-600 dark:text-red-400 overflow-x-auto whitespace-pre-wrap max-h-32 overflow-y-auto">{entry.error}</pre>
                  {/if}
                  {#each entry.rawEvents as ev}
                    <div class="flex items-start gap-2">
                      <span class="text-gray-400 whitespace-nowrap">{formatTime(ev.createdAt)}</span>
                      <span class="text-gray-500 shrink-0">{ev.eventType || ev.status}</span>
                      <pre class="text-gray-500 overflow-x-auto whitespace-pre-wrap flex-1">{ev.payload?.slice(0, 300) || "—"}</pre>
                    </div>
                  {/each}
                </div>
              {/if}
            </div>
          {/each}
        </div>
      </Card>
    {/if}
  </div>
{/if}

<!-- Export viewer modal -->
<Modal title="Session Export" bind:open={showExportViewer} size="xl" onclose={closeView}>
  <div class="prose prose-sm dark:prose-invert max-w-none overflow-y-auto max-h-[70vh]">
    {@html viewMarkdown}
  </div>
</Modal>
