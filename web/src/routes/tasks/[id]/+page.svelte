<script lang="ts">
  import { onMount, onDestroy } from "svelte";
  import { resolve } from "$app/paths";
  import { SvelteMap, SvelteSet } from "svelte/reactivity";
  import { createClient } from "@connectrpc/connect";
  import { TaskService, AdminService, SessionService, FleetService } from "$gen/proto/api/v1/api_pb";
  import type { AgentSession, Task, TaskArtifact, TaskEvent, TaskProgressEntry, RunnerInfo, UserPrompt } from "$gen/proto/api/v1/api_pb";
  import { getTransport } from "$lib/api/client";
  import {
    loadTaskEvents, loadTaskProgress, loadOlderTaskProgress, refreshTaskProgress, subscribeToTaskEvents,
    taskEvents, taskProgress, taskProgressHasMore, streamConnected, clearTaskDetail,
  } from "$lib/stores/taskDetail.svelte";
  import { formatDuration, formatTime, formatTimeShort, formatAge, humanReadableStatus, renderMarkdown } from "$lib/utils.svelte";
  import StatusBadge from "$lib/components/StatusBadge.svelte";
  import { Alert, Badge, Button, Card, Label, Modal, Progressbar, Select, Spinner, Textarea, Timeline, TimelineItem } from "flowbite-svelte";
  import { marked } from "marked";

  let { params } = $props();
  let task = $state<Task | null>(null);
  let taskSession = $state<AgentSession | null>(null);
  let userPrompts = $state<UserPrompt[]>([]);
  let artifacts = $state<TaskArtifact[]>([]);
  let artifactGroups = $derived.by(() => {
    const groups = new SvelteMap<string, { artifact: TaskArtifact; attemptIds: string[] }>();
    for (const artifact of artifacts) {
      const key = `${artifact.artifactType}:${artifact.repo}:${artifact.number}`;
      const group = groups.get(key) ?? { artifact, attemptIds: [] };
      if (artifact.executionAttemptId && !group.attemptIds.includes(artifact.executionAttemptId)) {
        group.attemptIds.push(artifact.executionAttemptId);
      }
      groups.set(key, group);
    }
    return [...groups.values()];
  });
  let loading = $state(true);
  let error = $state<string | null>(null);
  let unsub: (() => void) | null = null;
  let now = $state(Date.now());
  let viewMarkdown = $state<string | null>(null);
  let viewLoading = $state(false);
  let showExportViewer = $state(false);

  let recovering = $state(false);

  let totalTokens = $derived.by(() => {
    const tu = task?.tokenUsage;
    if (!tu) return 0n;
    return (tu.inputTokens || 0n) + (tu.outputTokens || 0n) + (tu.cacheReadTokens || 0n) + (tu.reasoningTokens || 0n);
  });

  function fmtCost(cents: bigint): string {
    return `$${(Number(cents) / 100).toFixed(4)}`;
  }

  function fmtTokens(n: bigint): string {
    const v = Number(n);
    if (v >= 1_000_000) return `${(v / 1_000_000).toFixed(1)}M`;
    if (v >= 1_000) return `${(v / 1_000).toFixed(1)}K`;
    return v.toString();
  }

  let events = $state<TaskEvent[]>([]);
  let progress = $state<TaskProgressEntry[]>([]);
  let progressHasMore = $state(false);
  let loadingOlderProgress = $state(false);
  let connected = $state(false);
  let activeRunners = $state<string[]>([]);

  let expandedProgress = new SvelteSet<string>();
  let rawEventsLoaded = $state(false);
  let rawEventsLoading = $state(false);

  function progressKey(entry: { time: string; index: number }) {
    return `${entry.time}:${entry.index}`;
  }

  async function ensureRawEvents() {
    if (rawEventsLoaded || rawEventsLoading) return;
    rawEventsLoading = true;
    try {
      await loadTaskEvents(params.id, 100);
      rawEventsLoaded = true;
    } finally {
      rawEventsLoading = false;
    }
  }

  async function toggleProgress(key: string) {
    if (expandedProgress.has(key)) { expandedProgress.delete(key); }
    else {
      await ensureRawEvents();
      expandedProgress.add(key);
    }
  }

  function entryRawEvents(time: string): TaskEvent[] {
    if (events.length === 0) return [];
    const entryTime = new Date(time).getTime();
    return events
      .filter(ev => Math.abs(new Date(ev.createdAt).getTime() - entryTime) < 10000)
      .sort((a, b) => a.createdAt.localeCompare(b.createdAt));
  }

  // Events sorted chronologically, with index as tiebreaker for same-second timestamps.
  // Heartbeat events are shown as a summary metric, not individual timeline entries.
  let eventsChrono = $derived(
    [...events]
      .filter(e => !e.eventType?.includes("heartbeat") && !e.subject?.includes("heartbeat"))
      .map((e, i) => ({ e, i }))
      .sort((a, b) => a.e.createdAt.localeCompare(b.e.createdAt) || a.i - b.i)
      .map(x => x.e)
  );

  // Timeline entries come directly from the distilled progress API.
  // Raw events are loaded lazily only when a user expands a timeline entry.
  // For harnesses that emit no structured events (Claude, Codex, Codewhale),
  // fall back to showing raw events as standalone entries.
  let timeline = $derived.by(() => {
    if (progress.length > 0) {
      return progress
        .map((entry, i) => ({
          time: entry.time,
          status: entry.status,
          summary: entry.summary,
          error: entry.error,
          index: i,
        }))
        .sort((a, b) => b.time.localeCompare(a.time) || b.index - a.index);
    }
    if (eventsChrono.length > 0) {
      return eventsChrono
        .map((ev, i) => ({
          time: ev.createdAt,
          status: ev.status,
          summary: ev.eventType || ev.status,
          error: "",
          index: i,
        }))
        .sort((a, b) => b.time.localeCompare(a.time) || b.index - a.index);
    }
    return [];
  });

  let duration = $derived(now && formatDuration(task?.startedAt, task?.endedAt));

  let heartbeatAge = $derived.by(() => {
    const hb = events
      .filter(e => e.eventType?.includes("heartbeat") || e.subject?.includes("heartbeat"))
      .sort((a, b) => b.createdAt.localeCompare(a.createdAt))[0];
    return hb ? formatAge(hb.createdAt) : null;
  });

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

  let ghRepo = $derived(task?.env?.GITHUB_REPO || (task?.env && task.env["GITHUB_REPO"]));
  let ghIssueNum = $derived(task?.env?.ISSUE_NUMBER || (task?.env && task.env["ISSUE_NUMBER"]));
  let ghIssueTitle = $derived(task?.env?.ISSUE_TITLE || (task?.env && task.env["ISSUE_TITLE"]));
  let ghIssueUrl = $derived(task?.env?.ISSUE_URL || (task?.env && task.env["ISSUE_URL"]));
  let ghPrNum = $derived(task?.env?.PR_NUMBER || (task?.env && task.env["PR_NUMBER"]));
  let taskHarness = $derived(task?.env?.__chetter_harness || "");
  let visibleEnv = $derived.by(() =>
    Object.entries(task?.env ?? {}).filter(([key]) => key !== "__chetter_harness")
  );
  let sessionMode = $derived(taskSession ? (taskSession.resumeMode === "harness_session" ? "resumable" : "none") : "—");

  function submissionSourceLabel(source: string): string {
    switch (source) {
      case "ui": return "Submitted via UI";
      case "mcp": return "Submitted via MCP";
      case "recovery": return "Recovery task";
      case "session_resume": return "Session resume";
      case "event_callback": return "Event callback";
      default: return "Manually submitted";
    }
  }

  function hasTriggerOrigin(task: Task): boolean {
    return !!task.triggerName && task.triggerType !== "event_callback";
  }

  let ghLink = $derived.by(() => {
    if (ghIssueUrl) return ghIssueUrl;
    if (ghRepo && ghIssueNum) return `https://github.com/${ghRepo}/issues/${ghIssueNum}`;
    if (ghRepo && ghPrNum) return `https://github.com/${ghRepo}/pull/${ghPrNum}`;
    return null;
  });

  let ghLabel = $derived.by(() => {
    if (ghIssueTitle) return `${ghRepo} — issue #${ghIssueNum}: ${ghIssueTitle}`;
    if (ghIssueNum) return `${ghRepo}#${ghIssueNum}`;
    if (ghPrNum) return `${ghRepo}#${ghPrNum} (PR)`;
    return null;
  });

  let ghContext = $derived(ghRepo != null || ghIssueNum != null || ghPrNum != null);

  let pinnedRunnerAvailable = $derived(
    !taskSession?.pinnedRunnerId || activeRunners.includes(taskSession.pinnedRunnerId)
  );

  let canResumeTask = $derived(
    (taskSession?.status === "paused" ||
    taskSession?.status === "recoverable" ||
    taskSession?.status === "paused_waiting_review") &&
    pinnedRunnerAvailable
  );
  let restartingTimedOutTask = $derived(task?.errorCategory === "timeout" && taskSession?.status === "recoverable");

  let timerInterval: ReturnType<typeof setInterval> | undefined;
  let progressRefreshCounter = $state(0);
  let unsubStores: (() => void)[] = [];
  let prevTaskId = $state("");

  $effect(() => {
    if (prevTaskId && prevTaskId !== params.id) {
      loadTaskData(params.id);
    }
    prevTaskId = params.id;
  });

  async function loadTaskData(taskId: string) {
    loading = true;
    error = null;
    artifacts = [];
    if (unsub) { unsub(); unsub = null; }
    clearTaskDetail();
    rawEventsLoaded = false;
    rawEventsLoading = false;
    expandedProgress.clear();

    await loadTaskProgress(taskId);

    try {
      const client = createClient(TaskService, getTransport());
      const resp = await client.getTask({ taskId });
      task = resp.task ?? null;
      await loadTaskSession();
      await loadActiveRunners();
      loading = false;

      if (task?.status === "done" || task?.status === "error" || task?.status === "cancelled") {
        try {
          const adminClient = createClient(AdminService, getTransport());
          const artResp = await adminClient.listTaskArtifacts({ taskId });
          artifacts = artResp.artifacts ?? [];
        } catch { /* silently skip */ }
      }

      if (task?.status === "running" || task?.status === "pending") {
        const streamSince = new Date().toISOString();
        unsub = subscribeToTaskEvents(taskId, streamSince, () => refreshTask());
      }
    } catch (e) {
      error = e instanceof Error ? e.message : "Failed to load task";
      loading = false;
    }
  }

  onMount(async () => {
    unsubStores = [
      taskEvents.subscribe(v => events = v),
      taskProgress.subscribe(v => progress = v),
      taskProgressHasMore.subscribe(v => progressHasMore = v),
      streamConnected.subscribe(v => connected = v),
    ];
    timerInterval = setInterval(() => {
      now = Date.now();
      progressRefreshCounter++;
      if (progressRefreshCounter % 5 === 0 && connected) {
        refreshTaskProgress(params.id);
      }
    }, 1000);
    await loadTaskData(params.id);
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
      await refreshTaskProgress(params.id);
      if (unsub) { unsub(); unsub = null; }
    } catch (e) {
      console.error("Failed to refresh task after completion:", e);
    }
  }

  async function loadOlderProgress() {
    if (loadingOlderProgress || !progressHasMore) return;
    loadingOlderProgress = true;
    try {
      await loadOlderTaskProgress(params.id);
    } finally {
      loadingOlderProgress = false;
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
    userPrompts = [];
    if (!task?.agentSessionId) return;
    try {
      const client = createClient(SessionService, getTransport());
      const resp = await client.getSession({ sessionId: task.agentSessionId });
      taskSession = resp.session ?? null;
      userPrompts = resp.prompts ?? [];
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
  let showExtendModal = $state(false);
  let extensionSeconds = $state("900");
  let extending = $state(false);

  async function resumeTask() {
    if (!taskSession) return;
    resumePrompt = restartingTimedOutTask ? "Continue from where the task timed out." : "";
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

  async function extendTask() {
    const extensionSec = Number(extensionSeconds);
    if (!Number.isInteger(extensionSec) || extensionSec <= 0) return;
    extending = true;
    try {
      const client = createClient(TaskService, getTransport());
      const resp = await client.extendTask({ taskId: params.id, extensionSec });
      task = resp.task ?? task;
      showExtendModal = false;
    } catch (e) {
      error = e instanceof Error ? e.message : "Failed to extend task timeout";
    } finally { extending = false; }
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

  async function recoverTask() {
    recovering = true;
    try {
      const client = createClient(TaskService, getTransport());
      await client.recoverTask({ taskId: params.id });
      window.location.href = resolve("/tasks");
    } catch (e) {
      error = e instanceof Error ? e.message : "Failed to recover task";
      recovering = false;
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
    <div class="flex flex-wrap items-center justify-between mb-6 gap-3">
      <div>
        <div class="flex flex-wrap items-center gap-3 mb-1">
          <h1 class="text-xl font-mono font-bold text-gray-900 dark:text-white break-all">{task.id}</h1>
          <StatusBadge status={task.status} />
          {#if statusText}
            <span class="text-xs text-gray-500 dark:text-gray-400 font-mono">({statusText})</span>
          {/if}
        </div>
        <p class="text-sm text-gray-500 dark:text-gray-400">
          Created {formatTime(task.createdAt)} · Updated {formatTime(task.updatedAt)}
        </p>
      </div>
      <div class="flex flex-wrap gap-2">
        {#if task.status === "running" || task.status === "pending"}
          {#if task.timeoutSec > 0}
            <Button color="alternative" size="sm" onclick={() => showExtendModal = true}>Extend deadline</Button>
          {/if}
          <Button color="red" size="sm" onclick={cancelTask}>Cancel</Button>
        {/if}
        {#if taskSession && (taskSession.status === "paused" || taskSession.status === "recoverable" || taskSession.status === "paused_waiting_review")}
          <Button color="green" size="sm" onclick={resumeTask} disabled={!pinnedRunnerAvailable}>
            {pinnedRunnerAvailable ? (restartingTimedOutTask ? "Restart" : "Resume") : "Pinned runner offline, can not resume"}
          </Button>
        {/if}
        {#if task.status === "done" || task.status === "error" || task.status === "cancelled"}
          <Button color="green" size="sm" onclick={recoverTask} disabled={recovering}>
            {recovering ? "Recovering…" : "Recover"}
          </Button>
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
    <div class="grid grid-cols-2 md:grid-cols-4 xl:grid-cols-6 gap-4 mb-6">
      <Card size="md" shadow="sm" class="!p-4">
        <p class="text-xs text-gray-500 dark:text-gray-400 mb-1">Agent</p>
        <p class="text-sm font-medium text-gray-900 dark:text-white">{task.agent || "default"}</p>
      </Card>
      <Card size="md" shadow="sm" class="!p-4">
        <p class="text-xs text-gray-500 dark:text-gray-400 mb-1">Provider</p>
        <p class="text-sm font-medium text-gray-900 dark:text-white">{task.providerId || "default"}</p>
      </Card>
      <Card size="md" shadow="sm" class="!p-4">
        <p class="text-xs text-gray-500 dark:text-gray-400 mb-1">Model</p>
        <p class="text-sm font-medium text-gray-900 dark:text-white">{task.modelId || "default"}</p>
      </Card>
      <Card size="md" shadow="sm" class="!p-4">
        <p class="text-xs text-gray-500 dark:text-gray-400 mb-1">Variant</p>
        <p class="text-sm font-medium text-gray-900 dark:text-white">{task.variantId || "default"}</p>
      </Card>
      <Card size="md" shadow="sm" class="!p-4">
        <p class="text-xs text-gray-500 dark:text-gray-400 mb-1">Harness</p>
        <p class="text-sm font-medium text-gray-900 dark:text-white">{taskHarness || "default"}</p>
      </Card>
       <Card size="md" shadow="sm" class="!p-4">
         <p class="text-xs text-gray-500 dark:text-gray-400 mb-1">Session Mode</p>
         <p class="text-sm font-medium text-gray-900 dark:text-white">{sessionMode}</p>
       </Card>
       <Card size="md" shadow="sm" class="!p-4">
         <p class="text-xs text-gray-500 dark:text-gray-400 mb-1">Origin</p>
         {#if hasTriggerOrigin(task)}
           <a href={resolve("/triggers/[name]", { name: task.triggerName })} class="text-sm font-medium text-blue-600 dark:text-blue-400 hover:underline truncate">
             {task.triggerName}
           </a>
           {#if task.triggerType}
             <p class="text-xs text-gray-500 dark:text-gray-400">{task.triggerType} trigger</p>
           {/if}
         {:else if task.triggerName}
           <p class="text-sm font-medium text-gray-900 dark:text-white">Event callback: {task.triggerName}</p>
         {:else}
           <p class="text-sm font-medium text-gray-900 dark:text-white">{submissionSourceLabel(task.submissionSource)}</p>
         {/if}
       </Card>
       <Card size="md" shadow="sm" class="!p-4">
        <p class="text-xs text-gray-500 dark:text-gray-400 mb-1">Agent Image</p>
        <p class="text-sm font-medium text-gray-900 dark:text-white truncate">{task.agentImage || "default"}</p>
      </Card>
       <Card size="md" shadow="sm" class="!p-4">
         <p class="text-xs text-gray-500 dark:text-gray-400 mb-1">Timeout</p>
         <p class="text-sm font-medium text-gray-900 dark:text-white">{task.timeoutSec}s</p>
       </Card>
       {#if task.executionId}
         <Card size="md" shadow="sm" class="!p-4">
           <p class="text-xs text-gray-500 dark:text-gray-400 mb-1">Current Execution</p>
           <p class="text-sm font-mono font-medium text-gray-900 dark:text-white truncate" title={task.executionId}>{task.executionId}</p>
         </Card>
       {/if}
       <Card size="md" shadow="sm" class="!p-4">
        <p class="text-xs text-gray-500 dark:text-gray-400 mb-1">Duration</p>
        <p class="text-sm font-medium text-gray-900 dark:text-white">{duration}</p>
      </Card>
      {#if heartbeatAge}
        <Card size="md" shadow="sm" class="!p-4">
          <p class="text-xs text-gray-500 dark:text-gray-400 mb-1">Last heartbeat</p>
          <p class="text-sm font-medium text-gray-900 dark:text-white">{heartbeatAge} ago</p>
        </Card>
      {/if}
      {#if task.agentSessionId}
        <Card size="md" shadow="sm" class="!p-4">
          <p class="text-xs text-gray-500 dark:text-gray-400 mb-1">Session</p>
          <div class="flex items-center gap-2 min-w-0">
            <a href={resolve("/sessions/[id]", { id: task.agentSessionId })} class="text-sm font-mono text-blue-600 dark:text-blue-400 hover:underline truncate">
              {task.agentSessionId.slice(0, 11)}…
            </a>
            {#if canResumeTask}
              <Badge color="green" class="shrink-0">Resumable</Badge>
            {/if}
          </div>
        </Card>
      {/if}
    </div>

    {#if task.gitUrl || task.gitRef || task.skills.length > 0 || visibleEnv.length > 0 || taskSession?.pauseReason || taskSession?.expiresAt}
      <Card size="xl" class="mb-6 w-full !p-5" shadow="sm">
        <h2 class="text-sm font-semibold text-gray-700 dark:text-gray-300 mb-3">Task Configuration</h2>
        <div class="grid grid-cols-1 md:grid-cols-2 gap-x-6 gap-y-4 text-sm">
          {#if task.gitUrl}
            <div>
              <p class="text-xs text-gray-500 dark:text-gray-400 mb-1">Git URL</p>
              <a href={task.gitUrl} target="_blank" rel="noopener noreferrer" class="block font-mono text-xs text-blue-600 dark:text-blue-400 hover:underline break-all">{task.gitUrl}</a>
            </div>
          {/if}
          {#if task.gitRef}
            <div>
              <p class="text-xs text-gray-500 dark:text-gray-400 mb-1">Git Ref</p>
              <p class="font-mono text-gray-900 dark:text-white">{task.gitRef}</p>
            </div>
          {/if}
          {#if task.skills.length > 0}
            <div>
              <p class="text-xs text-gray-500 dark:text-gray-400 mb-1">Skills</p>
              <p class="text-gray-900 dark:text-white">{task.skills.join(", ")}</p>
            </div>
          {/if}
          {#if taskSession?.pauseReason}
            <div>
              <p class="text-xs text-gray-500 dark:text-gray-400 mb-1">Pause Reason</p>
              <p class="text-gray-900 dark:text-white">{taskSession.pauseReason}</p>
            </div>
          {/if}
          {#if taskSession?.expiresAt}
            <div>
              <p class="text-xs text-gray-500 dark:text-gray-400 mb-1">Session Expires</p>
              <p class="text-gray-900 dark:text-white">{formatTime(taskSession.expiresAt)}</p>
            </div>
          {/if}
          {#if visibleEnv.length > 0}
            <div class="md:col-span-2">
              <p class="text-xs text-gray-500 dark:text-gray-400 mb-1">Environment</p>
              <pre class="max-h-48 overflow-auto whitespace-pre-wrap rounded-lg bg-gray-50 p-3 font-mono text-xs text-gray-600 dark:bg-gray-900/50 dark:text-gray-400">{visibleEnv.map(([key, value]) => `${key}=${value}`).join("\n")}</pre>
            </div>
          {/if}
        </div>
      </Card>
    {/if}

    {#if ghContext}
      <Card size="xl" class="mb-6 w-full !p-5" shadow="sm">
        <h2 class="text-sm font-semibold text-gray-700 dark:text-gray-300 mb-2">GitHub Context</h2>
        <div class="flex items-center gap-3">
          {#if ghLink}
            <a href={ghLink} target="_blank" rel="noopener" class="text-blue-600 dark:text-blue-400 hover:underline text-sm font-medium">
              {ghLabel}
            </a>
          {:else}
            <span class="text-sm font-medium text-gray-700 dark:text-gray-300">{ghLabel}</span>
          {/if}
          {#if ghIssueTitle}
            <span class="text-xs text-gray-500 dark:text-gray-400 truncate max-w-md">{ghIssueTitle}</span>
          {/if}
        </div>
      </Card>
    {/if}

    {#if task.tokenUsage && totalTokens > 0}
      <Card size="xl" class="mb-6 w-full !p-5" shadow="sm">
        <h2 class="text-sm font-semibold text-gray-700 dark:text-gray-300 mb-3">Token Consumption</h2>
        <div class="grid grid-cols-2 md:grid-cols-4 gap-4">
          <div>
            <p class="text-xs text-gray-500 dark:text-gray-400">Input tokens</p>
            <p class="text-lg font-mono font-medium text-gray-900 dark:text-white">{fmtTokens(task.tokenUsage.inputTokens)}</p>
          </div>
          <div>
            <p class="text-xs text-gray-500 dark:text-gray-400">Output tokens</p>
            <p class="text-lg font-mono font-medium text-gray-900 dark:text-white">{fmtTokens(task.tokenUsage.outputTokens)}</p>
          </div>
          <div>
            <p class="text-xs text-gray-500 dark:text-gray-400">Cache (R/W)</p>
            <p class="text-lg font-mono font-medium text-gray-900 dark:text-white">{fmtTokens(task.tokenUsage.cacheReadTokens)}/{fmtTokens(task.tokenUsage.cacheWriteTokens)}</p>
          </div>
          <div>
            <p class="text-xs text-gray-500 dark:text-gray-400">Reasoning tokens</p>
            <p class="text-lg font-mono font-medium text-gray-900 dark:text-white">{fmtTokens(task.tokenUsage.reasoningTokens)}</p>
          </div>
          <div>
            <p class="text-xs text-gray-500 dark:text-gray-400">Total tokens</p>
            <p class="text-lg font-mono font-medium text-gray-900 dark:text-white">{fmtTokens(totalTokens)}</p>
          </div>
          <div>
            <p class="text-xs text-gray-500 dark:text-gray-400">Est. cost</p>
            <p class="text-lg font-mono font-medium text-gray-900 dark:text-white">{fmtCost(task.tokenUsage.costCents)}</p>
          </div>
        </div>
      </Card>
    {/if}

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

    <!-- User Prompt Chain -->
    {#if userPrompts.length > 0}
      <Card size="xl" class="mb-6 w-full !p-5" shadow="sm">
        <h2 class="text-sm font-semibold text-gray-700 dark:text-gray-300 mb-3">User Prompts</h2>
        <p class="text-xs text-gray-500 dark:text-gray-400 mb-3">Conversation prompts and their runner execution attempts.</p>
        <div class="space-y-1">
          {#each userPrompts as prompt (prompt.id)}
            <div class="rounded px-3 py-2 {prompt.id === userPrompts[userPrompts.length - 1].id ? 'bg-blue-50 dark:bg-blue-900/20 border border-blue-200 dark:border-blue-700' : 'bg-gray-50 dark:bg-gray-800/50'}">
              <div class="flex items-center gap-3">
                <StatusBadge status={prompt.status} />
                <span class="text-sm font-mono text-gray-700 dark:text-gray-300">Prompt {prompt.sequence}</span>
                {#if prompt.prompt}
                  <span class="flex-1 text-xs text-gray-500 dark:text-gray-400 truncate">{prompt.prompt}</span>
                {/if}
                {#if prompt.id === userPrompts[userPrompts.length - 1].id}
                  <Badge color="blue">current</Badge>
                {:else if prompt.id === userPrompts[0].id}
                  <span class="text-xs text-gray-400">initial</span>
                {/if}
              </div>
              {#if prompt.attempts.length > 0}
                <div class="mt-2 ml-6 space-y-1 border-l border-gray-200 pl-3 dark:border-gray-700">
                  {#each prompt.attempts as attempt (attempt.id)}
                    <div class="flex items-center gap-2 text-xs text-gray-500 dark:text-gray-400">
                      <StatusBadge status={attempt.status} />
                      <span class="font-mono">Attempt {attempt.sequence}</span>
                      {#if attempt.runnerId}<span>on {attempt.runnerId}</span>{/if}
                      {#if attempt.error}<span class="truncate text-red-600 dark:text-red-400">{attempt.error}</span>{/if}
                    </div>
                  {/each}
                </div>
              {/if}
            </div>
          {/each}
        </div>
      </Card>
    {/if}

    <!-- Artifacts -->
    {#if artifactGroups.length > 0}
      <Card size="xl" class="mb-6 w-full !p-5" shadow="sm">
        <h2 class="text-sm font-semibold text-gray-700 dark:text-gray-300 mb-3">GitHub Artifacts</h2>
        <div class="space-y-2">
          {#each artifactGroups as group (`${group.artifact.artifactType}:${group.artifact.repo}:${group.artifact.number}`)}
            {@const art = group.artifact}
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
              {#each group.attemptIds as attemptId (attemptId)}
                <Badge color="gray" class="font-mono text-xs">{attemptId}</Badge>
              {/each}
            </div>
          {/each}
        </div>
      </Card>
    {/if}

    <!-- Progress Timeline -->
    {#if timeline.length > 0}
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
            {#each timeline as entry, i (progressKey(entry))}
              <TimelineItem title={entry.status === "done" ? "Completed successfully" : humanReadableStatus(entry.status, entry.summary)} date={formatTimeShort(entry.time)} isLast={i === timeline.length - 1}>
                <div class="mt-2 flex flex-wrap items-center gap-2">
                  <StatusBadge status={entry.status} />
                  {#if entry.error}
                    <Badge color="red">Error</Badge>
                  {/if}
                  <Button color="alternative" size="xs" onclick={() => toggleProgress(progressKey(entry))}>
                    {expandedProgress.has(progressKey(entry)) ? "Hide details" : "Details"}
                  </Button>
                </div>
                {#if entry.status === "done" && entry.summary && entry.summary !== entry.status}
                  <div class="mt-3">
                    <p class="text-xs text-gray-400 dark:text-gray-500 mb-1">Agent says:</p>
                    <div class="prose prose-sm dark:prose-invert max-w-none">
                      {@html renderMarkdown(entry.summary)}
                    </div>
                  </div>
                {/if}
                {#if expandedProgress.has(progressKey(entry))}
                  <div class="mt-3 rounded-lg bg-gray-50 px-3 py-2 dark:bg-gray-900/50">
                    {#if rawEventsLoading}
                      <div class="flex items-center gap-2 text-gray-500 dark:text-gray-400">
                        <Spinner size="4" />
                        <span class="text-xs">Loading raw events…</span>
                      </div>
                    {:else if entry.error}
                      <pre class="text-red-600 dark:text-red-400 overflow-x-auto whitespace-pre-wrap max-h-32 overflow-y-auto">{entry.error}</pre>
                    {:else}
                      {@const raw = entryRawEvents(entry.time)}
                      {#if raw.length > 0}
                        <div class="space-y-2 font-mono text-xs">
                          {#each raw as ev (ev.id)}
                            <div class="rounded border border-gray-200 bg-white p-2 dark:border-gray-700 dark:bg-gray-800">
                              <div class="mb-1 flex flex-wrap gap-2 text-gray-400">
                                <span>{formatTimeShort(ev.createdAt)}</span>
                                <span>{ev.eventType || ev.status}</span>
                                {#if ev.executionId}
                                  <span>{ev.executionId}</span>
                                {/if}
                              </div>
                              <pre class="max-h-96 overflow-auto whitespace-pre-wrap text-gray-600 dark:text-gray-400">{ev.payload || "—"}</pre>
                            </div>
                          {/each}
                        </div>
                      {:else}
                        <p class="text-xs text-gray-500 dark:text-gray-400">No raw events for this entry.</p>
                      {/if}
                    {/if}
                  </div>
                {/if}
              </TimelineItem>
            {/each}
          </Timeline>
          {#if progressHasMore}
            <div class="mt-4 flex justify-center">
              <Button color="alternative" size="sm" disabled={loadingOlderProgress} onclick={loadOlderProgress}>
                {#if loadingOlderProgress}
                  <Spinner size="4" class="me-2" />
                  Loading older events
                {:else}
                  Load older
                {/if}
              </Button>
            </div>
          {/if}
        </div>
      </Card>
    {/if}
  </div>
{/if}

<!-- Resume session modal -->
<Modal title={restartingTimedOutTask ? "Restart Timed-Out Task" : "Resume Session"} bind:open={showResumeModal} size="md" onclose={() => showResumeModal = false}>
  <div class="space-y-4">
    <div>
      <Label for="rt-prompt" class="mb-2">Follow-up prompt</Label>
      <Textarea id="rt-prompt" bind:value={resumePrompt} placeholder="Enter follow-up prompt for the agent" rows={4} class="w-full" />
    </div>
    <Button color="blue" disabled={!resumePrompt.trim() || resuming} onclick={doResume} class="w-full">
      {resuming ? "Resuming…" : (restartingTimedOutTask ? "Restart task" : "Resume")}
    </Button>
  </div>
</Modal>

<Modal title="Extend Task Deadline" bind:open={showExtendModal} size="md" onclose={() => showExtendModal = false}>
  <div class="space-y-4">
    <p class="text-sm text-gray-600 dark:text-gray-400">Add time to this task's current timeout without interrupting the runner.</p>
    <div>
      <Label for="extension-seconds" class="mb-2">Additional time</Label>
      <Select id="extension-seconds" bind:value={extensionSeconds}>
        <option value="300">5 minutes</option>
        <option value="900">15 minutes</option>
        <option value="1800">30 minutes</option>
        <option value="3600">1 hour</option>
      </Select>
    </div>
    <Button color="blue" disabled={extending} onclick={extendTask} class="w-full">
      {extending ? "Extending…" : "Extend deadline"}
    </Button>
  </div>
</Modal>

<!-- Export viewer modal -->
<Modal title="Session Export" bind:open={showExportViewer} size="xl" onclose={closeView}>
  <div class="prose prose-sm dark:prose-invert max-w-none overflow-y-auto max-h-[70vh]">
    {@html viewMarkdown}
  </div>
</Modal>
