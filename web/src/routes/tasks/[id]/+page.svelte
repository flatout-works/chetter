<script lang="ts">
  import { onMount, onDestroy } from "svelte";
  import { SvelteSet } from "svelte/reactivity";
  import { createClient } from "@connectrpc/connect";
  import { TaskService, AdminService } from "$gen/proto/api/v1/api_pb";
  import type { Task, TaskArtifact } from "$gen/proto/api/v1/api_pb";
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

  let expandedProgress = new SvelteSet<string>();

  function progressKey(entry: { time: string; summary: string; status: string }) {
    return `${entry.time}:${entry.status}:${entry.summary}`;
  }

  function toggleProgress(key: string) {
    if (expandedProgress.has(key)) { expandedProgress.delete(key); }
    else { expandedProgress.add(key); }
  }

  // Events sorted chronologically for matching raw events to progress entries.
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
    return result.sort((a, b) => b.time.localeCompare(a.time));
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
        unsub = subscribeToTaskEvents(params.id, streamSince, () => refreshTask());
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

  async function refreshTask() {
    try {
      const client = createClient(TaskService, getTransport());
      const resp = await client.getTask({ taskId: params.id });
      task = resp.task ?? null;
      if (task?.status === "done" || task?.status === "error" || task?.status === "cancelled") {
        try {
          const adminClient = createClient(AdminService, getTransport());
          const artResp = await adminClient.listTaskArtifacts({ taskId: params.id });
          artifacts = artResp.artifacts ?? [];
        } catch { /* silently skip */ }
      }
      await loadTaskProgress(params.id);
      if (unsub) { unsub(); unsub = null; }
    } catch (e) {
      console.error("Failed to refresh task after completion:", e);
    }
  }

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
    <Alert color="red">{error}</Alert>
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
      <Card size="md" shadow="sm" contentClass="!p-4">
        <p class="text-xs text-gray-500 dark:text-gray-400 mb-1">Agent</p>
        <p class="text-sm font-medium text-gray-900 dark:text-white">{task.agent || "default"}</p>
      </Card>
      <Card size="md" shadow="sm" contentClass="!p-4">
        <p class="text-xs text-gray-500 dark:text-gray-400 mb-1">Model</p>
        <p class="text-sm font-medium text-gray-900 dark:text-white">{task.modelId || "default"}</p>
      </Card>
      <Card size="md" shadow="sm" contentClass="!p-4">
        <p class="text-xs text-gray-500 dark:text-gray-400 mb-1">Image</p>
        <p class="text-sm font-medium text-gray-900 dark:text-white truncate">{task.agentImage || "default"}</p>
      </Card>
      <Card size="md" shadow="sm" contentClass="!p-4">
        <p class="text-xs text-gray-500 dark:text-gray-400 mb-1">Timeout</p>
        <p class="text-sm font-medium text-gray-900 dark:text-white">{task.timeoutSec}s</p>
      </Card>
      <Card size="md" shadow="sm" contentClass="!p-4">
        <p class="text-xs text-gray-500 dark:text-gray-400 mb-1">Duration</p>
        <p class="text-sm font-medium text-gray-900 dark:text-white">{duration}</p>
      </Card>
    </div>

    <!-- Prompt -->
    <Card size="xl" class="mb-6 w-full" shadow="sm" contentClass="!p-5">
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
      <Card size="xl" class="mb-6 w-full" shadow="sm" contentClass="!p-5">
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
      <Card size="xl" class="mb-6 w-full" shadow="sm" contentClass="!p-5">
        <div class="flex items-center justify-between">
          <div>
            <h2 class="text-sm font-semibold text-gray-700 dark:text-gray-300">Timeline</h2>
            <p class="mt-1 text-xs text-gray-500 dark:text-gray-400">Newest progress first. Use Details to expand raw event payloads.</p>
          </div>
          {#if connected}
            <span class="flex items-center gap-1.5 text-xs text-green-600 dark:text-green-400">
              <span class="w-2 h-2 bg-green-500 rounded-full animate-pulse"></span>
              Live
            </span>
          {/if}
        </div>
        <div class="mt-4 max-h-[34rem] overflow-y-auto divide-y divide-gray-100 rounded-lg border border-gray-100 dark:divide-gray-700 dark:border-gray-700">
          {#each mergedTimeline as entry (progressKey(entry))}
            <div>
              <button
                onclick={() => toggleProgress(progressKey(entry))}
                class="w-full flex gap-3 px-4 py-3 text-left hover:bg-gray-50 dark:hover:bg-gray-700/30 items-start"
              >
                <span class="mt-2 w-2 h-2 rounded-full shrink-0 {entry.status === 'done' || entry.status === 'error' ? 'bg-blue-500' : entry.status === 'running' ? 'bg-green-500' : 'bg-gray-400'}"></span>
                <span class="min-w-0 flex-1">
                  <span class="flex flex-wrap items-center gap-2">
                    <span class="text-sm text-gray-700 dark:text-gray-300">{humanReadableStatus(entry.status, entry.summary)}</span>
                    <StatusBadge status={entry.status} />
                  </span>
                  <span class="mt-1 block text-xs font-mono text-gray-400 dark:text-gray-500">{formatTime(entry.time)}</span>
                </span>
                {#if entry.error}
                  <span class="text-red-500 shrink-0">Error</span>
                {/if}
                <span class="mt-0.5 inline-flex shrink-0 items-center gap-1 rounded-md border border-gray-200 px-2 py-1 text-xs font-medium text-gray-500 shadow-sm dark:border-gray-700 dark:text-gray-300">
                  <span class="text-sm leading-none">{expandedProgress.has(progressKey(entry)) ? "▾" : "▸"}</span>
                  <span>{expandedProgress.has(progressKey(entry)) ? "Hide details" : "Details"}</span>
                </span>
              </button>
              {#if expandedProgress.has(progressKey(entry))}
                <div class="mx-4 mb-4 rounded-lg bg-gray-50 px-3 py-2 dark:bg-gray-900/50">
                  {#if entry.error}
                    <pre class="text-red-600 dark:text-red-400 overflow-x-auto whitespace-pre-wrap max-h-32 overflow-y-auto">{entry.error}</pre>
                  {/if}
                  {#if entry.rawEvents.length > 0}
                    <div class="space-y-2 font-mono text-xs">
                      {#each entry.rawEvents as ev (ev.id)}
                        <div class="rounded border border-gray-200 bg-white p-2 dark:border-gray-700 dark:bg-gray-800">
                          <div class="mb-1 flex flex-wrap gap-2 text-gray-400">
                            <span>{formatTime(ev.createdAt)}</span>
                            <span>{ev.eventType || ev.status}</span>
                          </div>
                          <pre class="max-h-48 overflow-auto whitespace-pre-wrap text-gray-500 dark:text-gray-400">{ev.payload?.slice(0, 1200) || "—"}</pre>
                        </div>
                      {/each}
                    </div>
                  {:else}
                    <p class="text-xs text-gray-500 dark:text-gray-400">No raw event payload was matched to this progress item.</p>
                  {/if}
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
