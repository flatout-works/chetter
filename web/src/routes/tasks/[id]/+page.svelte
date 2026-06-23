<script lang="ts">
  import { onMount, onDestroy } from "svelte";
  import { resolve } from "$app/paths";
  import { SvelteSet } from "svelte/reactivity";
  import { createClient } from "@connectrpc/connect";
  import { TaskService, AdminService, SessionService, FleetService } from "$gen/proto/api/v1/api_pb";
  import type { AgentSession, Task, TaskArtifact, TaskEvent, TaskProgressEntry, RunnerInfo, SessionRun } from "$gen/proto/api/v1/api_pb";
  import { getTransport } from "$lib/api/client";
  import {
    loadTaskEvents, loadTaskProgress, subscribeToTaskEvents,
    taskEvents, taskProgress, streamConnected, clearTaskDetail,
  } from "$lib/stores/taskDetail.svelte";
  import { formatDuration, formatTime, formatTimeShort, humanReadableStatus } from "$lib/utils.svelte";
  import StatusBadge from "$lib/components/StatusBadge.svelte";
  import { Alert, Badge, Button, Card, Label, Modal, Progressbar, Spinner, Textarea, Timeline, TimelineItem } from "flowbite-svelte";
  import { marked } from "marked";

  let { params } = $props();
  let task = $state<Task | null>(null);
  let taskSession = $state<AgentSession | null>(null);
  let sessionRuns = $state<SessionRun[]>([]);
  let artifacts = $state<TaskArtifact[]>([]);
  let loading = $state(true);
  let error = $state<string | null>(null);
  let unsub: (() => void) | null = null;
  let now = $state(Date.now());
  let viewMarkdown = $state<string | null>(null);
  let viewLoading = $state(false);
  let showExportViewer = $state(false);

  let events = $state<TaskEvent[]>([]);
  let progress = $state<TaskProgressEntry[]>([]);
  let connected = $state(false);
  let activeRunners = $state<string[]>([]);

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

    // If there are no progress entries, promote raw events to standalone entries
    if (result.length === 0) {
      for (const ev of eventsChrono) {
        result.push({
          type: "progress" as const,
          time: ev.createdAt,
          status: ev.status,
          summary: ev.eventType || ev.status,
          error: "",
          rawEvents: [ev],
          index: 0,
        });
      }
      return result.sort((a, b) => b.time.localeCompare(a.time));
    }

    // Add any raw events that don't correspond to existing progress entries
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

  let taskProgressPercent = $derived.by(() => {
    if (!task || (task.status !== "running" && task.status !== "pending") || task.timeoutSec <= 0) return 0;
    if (!task.startedAt) return task.status === "pending" ? 2 : 0;
    const elapsedSec = Math.max(0, (now - new Date(task.startedAt).getTime()) / 1000);
    return Math.min(100, Math.max(2, Math.round((elapsedSec / task.timeoutSec) * 100)));
  });

  let taskProgressLabel = $derived.by(() => {
    if (!task || (task.status !== "running" && task.status !== "pending")) return "";
    if (task.status === "pending") return "Waiting for a runner";
    return `${duration} elapsed of ${task.timeoutSec}s timeout`;
  });

  let statusText = $derived.by(() => {
    if (!task) return "";
    if (task.status === "running" || task.status === "pending") return `${duration}`;
    if (task.status === "done") return `Completed in ${duration}`;
    if (task.status === "error") return `Failed after ${duration}`;
    if (task.status === "cancelled") return `Cancelled after ${duration}`;
    return "";
  });

  let pinnedRunnerAvailable = $derived(
    !taskSession?.pinnedRunnerId || activeRunners.includes(taskSession.pinnedRunnerId)
  );

  let canResumeTask = $derived(
    (taskSession?.status === "paused" ||
    taskSession?.status === "recoverable" ||
    taskSession?.status === "paused_waiting_review") &&
    pinnedRunnerAvailable
  );

  let timerInterval: ReturnType<typeof setInterval> | undefined;
  let progressRefreshCounter = $state(0);
  let unsubStores: (() => void)[] = [];

  onMount(async () => {
    unsubStores = [
      taskEvents.subscribe(v => events = v),
      taskProgress.subscribe(v => progress = v),
      streamConnected.subscribe(v => connected = v),
    ];
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
      await loadTaskSession();
      await loadActiveRunners();
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
    for (const u of unsubStores) u();
  });

  async function refreshTask() {
    try {
      const client = createClient(TaskService, getTransport());
      const resp = await client.getTask({ taskId: params.id });
      task = resp.task ?? null;
      await loadTaskSession();
      await loadActiveRunners();
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

  async function loadTaskSession() {
    taskSession = null;
    sessionRuns = [];
    if (!task?.agentSessionId) return;
    try {
      const client = createClient(SessionService, getTransport());
      const resp = await client.getSession({ sessionId: task.agentSessionId });
      taskSession = resp.session ?? null;
      sessionRuns = resp.runs ?? [];
    } catch {
      taskSession = null;
    }
  }

  async function loadActiveRunners() {
    try {
      const client = createClient(FleetService, getTransport());
      const resp = await client.getRunnerHealth({ includeTasks: false });
      activeRunners = (resp.health?.runners ?? []).map((r) => r.runnerId);
    } catch {
      activeRunners = [];
    }
  }

  let showResumeModal = $state(false);
  let resumePrompt = $state("");
  let resuming = $state(false);

  async function resumeTask() {
    if (!taskSession) return;
    resumePrompt = "";
    showResumeModal = true;
  }

  async function doResume() {
    if (!resumePrompt.trim() || !taskSession) return;
    resuming = true;
    try {
      const client = createClient(SessionService, getTransport());
      await client.resumeSession({ sessionId: taskSession.id, prompt: resumePrompt.trim() });
      showResumeModal = false;
      await refreshTask();
    } catch (e) {
      error = e instanceof Error ? e.message : "Failed to resume session";
    } finally { resuming = false; }
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
        {#if taskSession && (taskSession.status === "paused" || taskSession.status === "recoverable" || taskSession.status === "paused_waiting_review")}
          <Button color="green" size="sm" onclick={resumeTask} disabled={!pinnedRunnerAvailable}>
            Resume{#if !pinnedRunnerAvailable} (pinned runner offline){/if}
          </Button>
        {/if}
        {#if task.status === "done" || task.status === "error" || task.status === "cancelled"}
          <Button size="sm" onclick={viewExport} disabled={viewLoading}>
            {viewLoading ? "Loading…" : "View"}
          </Button>
          <Button color="alternative" size="sm" onclick={exportTask}>Export</Button>
        {/if}
      </div>
    </div>

    {#if task.status === "running" || task.status === "pending"}
      <Card size="xl" class="mb-6 w-full !p-4" shadow="sm">
        <div class="mb-2 flex items-center justify-between gap-3">
          <span class="text-sm font-medium text-gray-700 dark:text-gray-300">Task progress</span>
          <span class="text-xs text-gray-500 dark:text-gray-400">{taskProgressLabel}</span>
        </div>
        <Progressbar progress={taskProgressPercent} size="h-3" color={task.status === "pending" ? "yellow" : "green"} />
      </Card>
    {/if}

    <!-- Task metadata -->
    <div class="grid grid-cols-2 md:grid-cols-6 gap-4 mb-6">
      <Card size="md" shadow="sm" class="!p-4">
        <p class="text-xs text-gray-500 dark:text-gray-400 mb-1">Agent</p>
        <p class="text-sm font-medium text-gray-900 dark:text-white">{task.agent || "default"}</p>
      </Card>
      <Card size="md" shadow="sm" class="!p-4">
        <p class="text-xs text-gray-500 dark:text-gray-400 mb-1">Model</p>
        <p class="text-sm font-medium text-gray-900 dark:text-white">{task.modelId || "default"}</p>
      </Card>
      <Card size="md" shadow="sm" class="!p-4">
        <p class="text-xs text-gray-500 dark:text-gray-400 mb-1">Image</p>
        <p class="text-sm font-medium text-gray-900 dark:text-white truncate">{task.agentImage || "default"}</p>
      </Card>
      <Card size="md" shadow="sm" class="!p-4">
        <p class="text-xs text-gray-500 dark:text-gray-400 mb-1">Timeout</p>
        <p class="text-sm font-medium text-gray-900 dark:text-white">{task.timeoutSec}s</p>
      </Card>
      <Card size="md" shadow="sm" class="!p-4">
        <p class="text-xs text-gray-500 dark:text-gray-400 mb-1">Duration</p>
        <p class="text-sm font-medium text-gray-900 dark:text-white">{duration}</p>
      </Card>
      {#if task.agentSessionId}
        <Card size="md" shadow="sm" class="!p-4">
          <p class="text-xs text-gray-500 dark:text-gray-400 mb-1">Session</p>
          <div class="flex items-center gap-2">
            <a href={resolve("/sessions/[id]", { id: task.agentSessionId })} class="text-sm font-mono text-blue-600 dark:text-blue-400 hover:underline">
              {task.agentSessionId.slice(0, 20)}…
            </a>
            {#if canResumeTask}
              <Badge color="green">Resumable</Badge>
            {/if}
          </div>
        </Card>
      {/if}
    </div>

    <!-- Prompt -->
    <Card size="xl" class="mb-6 w-full !p-5" shadow="sm">
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

    <!-- Session Run Chain -->
    {#if sessionRuns.length > 1}
      <Card size="xl" class="mb-6 w-full !p-5" shadow="sm">
        <h2 class="text-sm font-semibold text-gray-700 dark:text-gray-300 mb-3">Session Runs</h2>
        <p class="text-xs text-gray-500 dark:text-gray-400 mb-3">This task is part of a resumed session. Each follow-up prompt creates a new session run.</p>
        <div class="space-y-1">
          {#each sessionRuns as run (run.id)}
            <div class="flex items-center gap-3 px-3 py-2 rounded {run.taskId === task.id ? 'bg-blue-50 dark:bg-blue-900/20 border border-blue-200 dark:border-blue-700' : 'bg-gray-50 dark:bg-gray-800/50'}">
              <StatusBadge status={run.status} />
              <a href={resolve("/tasks/[id]", { id: run.taskId })} class="flex-1 min-w-0">
                <span class="text-sm font-mono text-blue-600 dark:text-blue-400 hover:underline">
                  {run.taskId.slice(0, 24)}…
                </span>
              </a>
              {#if run.prompt}
                <span class="text-xs text-gray-400 dark:text-gray-500 truncate hidden sm:block">{run.prompt.slice(0, 60)}</span>
              {/if}
              {#if run.taskId === task.id}
                <Badge color="blue">current</Badge>
              {:else if run.id === sessionRuns[0].id}
                <span class="text-xs text-gray-400">initial</span>
              {/if}
            </div>
          {/each}
        </div>
      </Card>
    {/if}

    <!-- Artifacts -->
    {#if artifacts.length > 0}
      <Card size="xl" class="mb-6 w-full !p-5" shadow="sm">
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
      <Card size="xl" class="mb-6 w-full !p-5" shadow="sm">
        <div class="flex items-center justify-between">
          <div>
            <h2 class="text-sm font-semibold text-gray-700 dark:text-gray-300">Timeline</h2>
            <p class="mt-1 text-xs text-gray-500 dark:text-gray-400">Newest progress first. Use Details to expand raw event payloads.</p>
          </div>
          {#if connected}
            <Badge color="green" class="inline-flex items-center gap-1.5">
              <span class="w-2 h-2 bg-green-500 rounded-full animate-pulse"></span>
              Live
            </Badge>
          {/if}
        </div>
        <div class="mt-4 max-h-[34rem] overflow-y-auto rounded-lg border border-gray-100 p-4 dark:border-gray-700">
          <Timeline>
            {#each mergedTimeline as entry, i (progressKey(entry))}
              <TimelineItem title={humanReadableStatus(entry.status, entry.summary)} date={formatTimeShort(entry.time)} isLast={i === mergedTimeline.length - 1}>
                <div class="mt-2 flex flex-wrap items-center gap-2">
                  <StatusBadge status={entry.status} />
                  {#if entry.error}
                    <Badge color="red">Error</Badge>
                  {/if}
                  <Button color="alternative" size="xs" onclick={() => toggleProgress(progressKey(entry))}>
                    {expandedProgress.has(progressKey(entry)) ? "Hide details" : "Details"}
                  </Button>
                </div>
                {#if expandedProgress.has(progressKey(entry))}
                  <div class="mt-3 rounded-lg bg-gray-50 px-3 py-2 dark:bg-gray-900/50">
                    {#if entry.error}
                      <pre class="text-red-600 dark:text-red-400 overflow-x-auto whitespace-pre-wrap max-h-32 overflow-y-auto">{entry.error}</pre>
                    {/if}
                    {#if entry.rawEvents.length > 0}
                      <div class="space-y-2 font-mono text-xs">
                        {#each entry.rawEvents as ev (ev.id)}
                          <div class="rounded border border-gray-200 bg-white p-2 dark:border-gray-700 dark:bg-gray-800">
                            <div class="mb-1 flex flex-wrap gap-2 text-gray-400">
                              <span>{formatTimeShort(ev.createdAt)}</span>
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
              </TimelineItem>
            {/each}
          </Timeline>
        </div>
      </Card>
    {/if}
  </div>
{/if}

<!-- Resume session modal -->
<Modal title="Resume Session" bind:open={showResumeModal} size="md" onclose={() => showResumeModal = false}>
  <div class="space-y-4">
    <div>
      <Label for="rt-prompt" class="mb-2">Follow-up prompt</Label>
      <Textarea id="rt-prompt" bind:value={resumePrompt} placeholder="Enter follow-up prompt for the agent" rows={4} class="w-full" />
    </div>
    <Button color="blue" disabled={!resumePrompt.trim() || resuming} onclick={doResume} class="w-full">
      {resuming ? "Resuming…" : "Resume"}
    </Button>
  </div>
</Modal>

<!-- Export viewer modal -->
<Modal title="Session Export" bind:open={showExportViewer} size="xl" onclose={closeView}>
  <div class="prose prose-sm dark:prose-invert max-w-none overflow-y-auto max-h-[70vh]">
    {@html viewMarkdown}
  </div>
</Modal>
