<script lang="ts">
  import { onMount } from "svelte";
  import { resolve } from "$app/paths";
  import { createClient } from "@connectrpc/connect";
  import { SessionService, FleetService, TaskService } from "$gen/proto/api/v1/api_pb";
  import type { AgentSession, SessionRun, Task } from "$gen/proto/api/v1/api_pb";
  import { getTransport } from "$lib/api/client";
  import { formatTime } from "$lib/utils.svelte";
  import StatusBadge from "$lib/components/StatusBadge.svelte";
  import TableCard from "$lib/components/TableCard.svelte";
  import { Alert, Badge, Button, Card, Label, Modal, Spinner, Table, TableHead, TableHeadCell, TableBody, TableBodyRow, TableBodyCell, Textarea } from "flowbite-svelte";

  let { params } = $props();
  let session = $state<AgentSession | null>(null);
  let runs = $state<SessionRun[]>([]);
  let loading = $state(true);
  let error = $state<string | null>(null);
  let activeRunners = $state<string[]>([]);

  let pinnedRunnerAvailable = $derived(
    !session?.pinnedRunnerId || activeRunners.includes(session.pinnedRunnerId)
  );

  let showResume = $state(false);
  let resumePrompt = $state("");
  let resuming = $state(false);

  let runTokenUsages = $state<Map<string, { totalTokens: bigint; costCents: bigint }>>(new Map());
  let runTasks = $state<Map<string, Task>>(new Map());
  let totalSessionTokens = $state<bigint>(0n);
  let totalSessionCost = $state<bigint>(0n);
  let initialTask = $derived(runs.length > 0 ? runTasks.get(runs[0].taskId) : undefined);

  function fmtCost(cents: bigint): string {
    return `$${(Number(cents) / 100).toFixed(4)}`;
  }

  function fmtTokens(n: bigint): string {
    const v = Number(n);
    if (v >= 1_000_000) return `${(v / 1_000_000).toFixed(1)}M`;
    if (v >= 1_000) return `${(v / 1_000).toFixed(1)}K`;
    return v.toString();
  }

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

  async function resume() {
    resumePrompt = "";
    showResume = true;
  }

  async function doResume() {
    if (!resumePrompt.trim()) return;
    resuming = true;
    try {
      const client = createClient(SessionService, getTransport());
      await client.resumeSession({ sessionId: params.id, prompt: resumePrompt.trim() });
      showResume = false;
      await load();
    } catch (e) {
      error = e instanceof Error ? e.message : "Failed to resume session.";
      console.error(e);
    } finally { resuming = false; }
  }

  async function load() {
    try {
      const [sessionResp, fleetResp] = await Promise.all([
        createClient(SessionService, getTransport()).getSession({ sessionId: params.id }),
        createClient(FleetService, getTransport()).getRunnerHealth({ includeTasks: false }),
      ]);
      session = sessionResp.session ?? null;
      runs = sessionResp.runs ?? [];
      activeRunners = (fleetResp.health?.runners ?? []).map((r) => r.runnerId);

      const taskClient = createClient(TaskService, getTransport());
      const taskResults = await Promise.allSettled(
        runs.map(async (run) => {
          const resp = await taskClient.getTask({ taskId: run.taskId });
          return resp.task;
        })
      );
      const m = new Map<string, { totalTokens: bigint; costCents: bigint }>();
      const taskMap = new Map<string, Task>();
      let sessionTotal = 0n;
      let costTotal = 0n;
      taskResults.forEach((result) => {
        if (result.status === "fulfilled" && result.value) {
          taskMap.set(result.value.id, result.value);
        }
        if (result.status === "fulfilled" && result.value?.tokenUsage) {
          const tu = result.value.tokenUsage;
          const total = (tu.inputTokens ?? 0n) + (tu.outputTokens ?? 0n) + (tu.reasoningTokens ?? 0n);
          m.set(result.value.id, { totalTokens: total, costCents: tu.costCents ?? 0n });
          sessionTotal += total;
          costTotal += tu.costCents ?? 0n;
        }
      });
      runTokenUsages = m;
      runTasks = taskMap;
      totalSessionTokens = sessionTotal;
      totalSessionCost = costTotal;
    } catch (e) {
      error = e instanceof Error ? e.message : "Failed to load session.";
      console.error(e);
    } finally { loading = false; }
  }

  onMount(load);
</script>

<svelte:head>
  <title>Session {params.id.slice(0, 12)}… — Chetter</title>
</svelte:head>

<div class="p-6 max-w-6xl">
  {#if loading}
    <div class="flex items-center gap-2 text-gray-500 dark:text-gray-400"><Spinner size="4" /> Loading…</div>
  {:else if error}
    <Alert color="red">{error}</Alert>
  {:else if session}
    <div class="flex items-center justify-between mb-6">
      <div>
        <div class="flex items-center gap-3 mb-1">
          <h1 class="text-xl font-mono font-bold text-gray-900 dark:text-white">{session.id}</h1>
          <StatusBadge status={session.status} />
        </div>
        <p class="text-sm text-gray-500 dark:text-gray-400">
          Created {formatTime(session.createdAt)} · Updated {formatTime(session.updatedAt)}
        </p>
      </div>
      {#if session.status === "paused" || session.status === "recoverable" || session.status === "paused_waiting_review"}
        <Button color="green" size="sm" onclick={resume} disabled={!pinnedRunnerAvailable}>
          Resume
          {#if !pinnedRunnerAvailable}
            <span class="ml-1 opacity-70">(pinned runner offline)</span>
          {/if}
        </Button>
      {/if}
    </div>

    <div class="grid grid-cols-2 md:grid-cols-4 gap-4 mb-6">
      <Card size="sm" shadow="sm" class="!p-4">
        <p class="text-xs text-gray-500 dark:text-gray-400 mb-1">Agent</p>
        <p class="text-sm font-medium text-gray-900 dark:text-white">{session.agent || "—"}</p>
      </Card>
      <Card size="sm" shadow="sm" class="!p-4">
        <p class="text-xs text-gray-500 dark:text-gray-400 mb-1">Model</p>
        <p class="text-sm font-medium text-gray-900 dark:text-white">{session.modelId || "—"}</p>
      </Card>
      <Card size="sm" shadow="sm" class="!p-4">
        <p class="text-xs text-gray-500 dark:text-gray-400 mb-1">Resume Mode</p>
        <p class="text-sm font-medium text-gray-900 dark:text-white">{session.resumeMode || "none"}</p>
      </Card>
      <Card size="sm" shadow="sm" class="!p-4">
        <p class="text-xs text-gray-500 dark:text-gray-400 mb-1">Pinned Runner</p>
        <p class="text-sm font-medium text-gray-900 dark:text-white truncate">{session.pinnedRunnerId || "—"}</p>
      </Card>
      <Card size="sm" shadow="sm" class="!p-4">
        <p class="text-xs text-gray-500 dark:text-gray-400 mb-1">Origin</p>
        {#if initialTask?.triggerName && initialTask.triggerType !== "event_callback"}
          <a href={resolve("/triggers/[name]", { name: initialTask.triggerName })} class="text-sm font-medium text-blue-600 dark:text-blue-400 hover:underline truncate">
            {initialTask.triggerName}
          </a>
        {:else if initialTask?.triggerName}
          <p class="text-sm font-medium text-gray-900 dark:text-white">Event callback: {initialTask.triggerName}</p>
        {:else}
          <p class="text-sm font-medium text-gray-900 dark:text-white">{submissionSourceLabel(initialTask?.submissionSource ?? "")}</p>
        {/if}
      </Card>
    </div>

    {#if session.pauseReason && (session.status === "paused" || session.status === "recoverable" || session.status === "paused_waiting_review")}
      <Alert color="yellow" class="mb-6">
        <p class="text-sm"><span class="font-semibold">Pause reason:</span> {session.pauseReason}</p>
      </Alert>
    {/if}

    {#if session.error}
      <Alert color="red" class="mb-6">
        <p class="text-sm font-mono">{session.error}</p>
      </Alert>
    {/if}

    {#if totalSessionTokens > 0n}
      <Card size="xl" class="mb-6 w-full !p-5" shadow="sm">
        <div class="grid grid-cols-2 md:grid-cols-2 gap-4">
          <div>
            <p class="text-xs text-gray-500 dark:text-gray-400">Total tokens</p>
            <p class="text-lg font-mono font-medium text-gray-900 dark:text-white">{fmtTokens(totalSessionTokens)}</p>
          </div>
          <div>
            <p class="text-xs text-gray-500 dark:text-gray-400">Est. cost</p>
            <p class="text-lg font-mono font-medium text-gray-900 dark:text-white">{fmtCost(totalSessionCost)}</p>
          </div>
        </div>
      </Card>
    {/if}

    <TableCard title={`Session runs (${runs.length})`}>
    <Table hoverable={true} shadow={false}>
      <TableHead>
        <TableHeadCell>Run ID</TableHeadCell>
        <TableHeadCell>Task</TableHeadCell>
        <TableHeadCell>Origin</TableHeadCell>
        <TableHeadCell>Status</TableHeadCell>
        <TableHeadCell>Tokens</TableHeadCell>
        <TableHeadCell>Summary</TableHeadCell>
        <TableHeadCell>Started</TableHeadCell>
      </TableHead>
      <TableBody>
        {#each runs as run (run.id)}
          {@const runTask = runTasks.get(run.taskId)}
          <TableBodyRow>
            <TableBodyCell><span class="font-mono text-gray-700 dark:text-gray-300 text-xs">{run.id.slice(0, 20)}…</span></TableBodyCell>
            <TableBodyCell>
              <a href={resolve("/tasks/[id]", { id: run.taskId })} class="font-mono text-blue-600 dark:text-blue-400 hover:underline text-xs">
                {run.taskId.slice(0, 20)}…
              </a>
            </TableBodyCell>
            <TableBodyCell>
              {#if runTask?.triggerName && runTask.triggerType !== "event_callback"}
                <a href={resolve("/triggers/[name]", { name: runTask.triggerName })} class="text-blue-600 dark:text-blue-400 hover:underline text-sm">
                  {runTask.triggerName}
                </a>
              {:else if runTask?.triggerName}
                <span class="text-gray-500 dark:text-gray-400 text-sm">Event callback: {runTask.triggerName}</span>
              {:else}
                <span class="text-gray-500 dark:text-gray-400 text-sm">{submissionSourceLabel(runTask?.submissionSource ?? "")}</span>
              {/if}
            </TableBodyCell>
            <TableBodyCell><StatusBadge status={run.status} /></TableBodyCell>
            <TableBodyCell>
              {#if runTokenUsages.has(run.taskId)}
                <span class="font-mono text-xs text-gray-900 dark:text-white">{fmtTokens(runTokenUsages.get(run.taskId)!.totalTokens)}</span>
              {:else}
                <span class="text-gray-400 dark:text-gray-600 text-xs">—</span>
              {/if}
            </TableBodyCell>
            <TableBodyCell class="max-w-xs"><span class="text-gray-500 dark:text-gray-400 truncate block">{run.summary || "—"}</span></TableBodyCell>
            <TableBodyCell><span class="text-gray-500 dark:text-gray-400 whitespace-nowrap">{formatTime(run.startedAt || "")}</span></TableBodyCell>
          </TableBodyRow>
        {:else}
          <TableBodyRow>
            <TableBodyCell colspan={7}>
              <div class="text-center text-gray-500 dark:text-gray-400 py-8">No runs recorded</div>
            </TableBodyCell>
          </TableBodyRow>
        {/each}
      </TableBody>
    </Table>
    </TableCard>
  {/if}
</div>

<Modal title="Resume Session" bind:open={showResume} size="md" onclose={() => showResume = false}>
  <div class="space-y-4">
    <div>
      <Label for="rs-prompt" class="mb-2">Follow-up prompt</Label>
      <Textarea id="rs-prompt" bind:value={resumePrompt} placeholder="Enter follow-up prompt for the agent" rows={4} class="w-full" />
    </div>
    <Button color="blue" disabled={!resumePrompt.trim() || resuming} onclick={doResume} class="w-full">
      {resuming ? "Resuming…" : "Resume"}
    </Button>
  </div>
</Modal>
