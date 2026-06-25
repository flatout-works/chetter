<script lang="ts">
  import { onMount } from "svelte";
  import { resolve } from "$app/paths";
  import { createClient } from "@connectrpc/connect";
  import { TriggerService, TaskService } from "$gen/proto/api/v1/api_pb";
  import type { Trigger, TriggerRun } from "$gen/proto/api/v1/api_pb";
  import { getTransport } from "$lib/api/client";
  import { formatTime } from "$lib/utils.svelte";
  import { addToast } from "$lib/stores/toast.svelte";
  import { confirm } from "$lib/stores/confirm.svelte";
  import StatusBadge from "$lib/components/StatusBadge.svelte";
  import { Alert, Badge, Button, Card, PaginationNav, Spinner, Table, TableHead, TableHeadCell, TableBody, TableBodyRow, TableBodyCell, Toggle } from "flowbite-svelte";

  let { params } = $props();
  let trigger = $state<Trigger | null>(null);
  let loading = $state(true);
  let error = $state<string | null>(null);

  let runs = $state<TriggerRun[]>([]);
  let loadingRuns = $state(false);
  let runsPage = $state(0);
  let runsPageSize = 10;
  let totalRunsPages = $derived(Math.max(1, Math.ceil(runs.length / runsPageSize)));
  let pagedRuns = $derived(runs.slice(runsPage * runsPageSize, (runsPage + 1) * runsPageSize));
  let runTokenTotals = $state<Map<string, bigint>>(new Map());

  let triggerConfig = $derived.by(() => {
    if (!trigger?.triggerConfig) return {};
    try { return JSON.parse(trigger.triggerConfig); } catch { return {}; }
  });

  function isGitManaged(): boolean {
    return !!trigger?.sourceId;
  }

  function triggerTarget(): string {
    if (!trigger) return "—";
    if (trigger.cronExpr) return trigger.cronExpr;
    return triggerConfig.repo || "—";
  }

  function fmtTokens(n: bigint): string {
    const v = Number(n);
    if (v >= 1_000_000) return `${(v / 1_000_000).toFixed(1)}M`;
    if (v >= 1_000) return `${(v / 1_000).toFixed(1)}K`;
    return v.toString();
  }

  async function loadTrigger() {
    loading = true;
    error = null;
    try {
      const client = createClient(TriggerService, getTransport());
      const resp = await client.listTriggers({});
      trigger = (resp.triggers ?? []).find((t) => t.name === params.name) ?? null;
      if (!trigger) error = "Trigger not found.";
    } catch (e) {
      error = e instanceof Error ? e.message : "Failed to load trigger.";
    } finally {
      loading = false;
    }
  }

  async function loadRuns() {
    loadingRuns = true;
    runsPage = 0;
    try {
      const client = createClient(TriggerService, getTransport());
      const resp = await client.listTriggerRuns({ triggerName: params.name, limit: 50 });
      runs = resp.runs ?? [];
      const taskClient = createClient(TaskService, getTransport());
      const tasks = await Promise.allSettled(
        runs.map(async (run) => {
          const taskResp = await taskClient.getTask({ taskId: run.taskId });
          return taskResp.task;
        })
      );
      const m = new Map<string, bigint>();
      tasks.forEach((result) => {
        if (result.status === "fulfilled" && result.value?.tokenUsage) {
          const tu = result.value.tokenUsage;
          m.set(result.value.id, (tu.inputTokens ?? 0n) + (tu.outputTokens ?? 0n) + (tu.reasoningTokens ?? 0n));
        }
      });
      runTokenTotals = m;
    } catch (e) {
      console.error(e);
    } finally {
      loadingRuns = false;
    }
  }

  async function toggleEnabled() {
    if (!trigger) return;
    error = null;
    try {
      const client = createClient(TriggerService, getTransport());
      await client.updateTrigger({ name: trigger.name, enabled: !trigger.enabled });
      trigger = { ...trigger, enabled: !trigger.enabled };
      addToast(`${trigger.name} ${trigger.enabled ? "enabled" : "disabled"}`, "success");
    } catch (e) {
      error = e instanceof Error ? e.message : "Failed to update trigger.";
      addToast(error, "error");
    }
  }

  async function runNow() {
    if (!trigger) return;
    const ok = await confirm({
      title: "Run Trigger",
      message: `Run trigger "${trigger.name}" now?`,
      confirmLabel: "Run",
    });
    if (!ok) return;
    error = null;
    try {
      const client = createClient(TriggerService, getTransport());
      await client.runTrigger({ name: trigger.name });
      addToast(`Trigger "${trigger.name}" started`, "success");
    } catch (e) {
      error = e instanceof Error ? e.message : "Failed to run trigger.";
      addToast(error, "error");
    }
  }

  async function deleteTrigger() {
    if (!trigger) return;
    const ok = await confirm({
      title: "Delete Trigger",
      message: `Delete trigger "${trigger.name}"? This cannot be undone.`,
      confirmLabel: "Delete",
    });
    if (!ok) return;
    try {
      const client = createClient(TriggerService, getTransport());
      await client.deleteTrigger({ name: trigger.name });
      addToast(`Trigger "${trigger.name}" deleted`, "success");
      window.location.href = resolve("/triggers");
    } catch (e) {
      error = e instanceof Error ? e.message : "Failed to delete trigger.";
      addToast(error, "error");
    }
  }

  onMount(() => {
    loadTrigger();
    loadRuns();
  });
</script>

<svelte:head>
  <title>{trigger?.name ?? "Trigger"} — Chetter</title>
</svelte:head>

<div class="p-6">
  <a href={resolve("/triggers")} class="inline-flex items-center gap-1 text-sm text-blue-600 dark:text-blue-400 hover:underline mb-4">
    &larr; Back to Triggers
  </a>

  {#if loading}
    <div class="flex items-center gap-2 text-gray-500 dark:text-gray-400"><Spinner size="4" /> Loading…</div>
  {:else if error}
    <Alert color="red">{error}</Alert>
  {:else if trigger}
    <div class="flex flex-wrap items-center justify-between mb-6 gap-3">
      <div class="flex items-center gap-3">
        <h1 class="text-2xl font-bold text-gray-900 dark:text-white">{trigger.name}</h1>
        <StatusBadge status={trigger.triggerType} />
        <StatusBadge status={trigger.enabled ? "enabled" : "disabled"} />
        {#if isGitManaged()}
          <Badge color="gray">git-managed</Badge>
        {/if}
      </div>
      <div class="flex items-center gap-2">
        <Toggle checked={trigger.enabled} onchange={toggleEnabled} color="gray" size="small" disabled={isGitManaged()} />
        <Button color="blue" size="sm" onclick={runNow}>Run Now</Button>
        <Button color="red" size="sm" onclick={deleteTrigger} disabled={isGitManaged()}>Delete</Button>
      </div>
    </div>

    <Card size="xl" shadow="sm" class="w-full !p-4 mb-6">
      <div class="grid grid-cols-1 sm:grid-cols-2 lg:grid-cols-3 gap-x-6 gap-y-3 text-sm">
        <div>
          <span class="text-xs text-gray-400 dark:text-gray-500">Type</span>
          <p class="text-gray-900 dark:text-white">{trigger.triggerType}</p>
        </div>
        <div>
          <span class="text-xs text-gray-400 dark:text-gray-500">Target</span>
          <p class="text-gray-900 dark:text-white font-mono">{triggerTarget()}</p>
        </div>
        {#if triggerConfig.repo}
          <div>
            <span class="text-xs text-gray-400 dark:text-gray-500">Repository</span>
            <p class="text-gray-900 dark:text-white"><a href={`https://github.com/${triggerConfig.repo}`} target="_blank" rel="noopener" class="text-blue-600 dark:text-blue-400 hover:underline">{triggerConfig.repo}</a></p>
          </div>
        {/if}
        {#if triggerConfig.event}
          <div>
            <span class="text-xs text-gray-400 dark:text-gray-500">Event</span>
            <p class="text-gray-900 dark:text-white">{triggerConfig.event}</p>
          </div>
        {/if}
        {#if triggerConfig.match_labels && triggerConfig.match_labels.length > 0}
          <div>
            <span class="text-xs text-gray-400 dark:text-gray-500">Match Labels</span>
            <p class="text-gray-900 dark:text-white">{triggerConfig.match_labels.join(", ")}</p>
          </div>
        {/if}
        {#if triggerConfig.session_mode}
          <div>
            <span class="text-xs text-gray-400 dark:text-gray-500">Session Mode</span>
            <p class="text-gray-900 dark:text-white">{triggerConfig.session_mode}</p>
          </div>
        {/if}
        {#if triggerConfig.ttl_hours}
          <div>
            <span class="text-xs text-gray-400 dark:text-gray-500">TTL</span>
            <p class="text-gray-900 dark:text-white">{triggerConfig.ttl_hours}h</p>
          </div>
        {/if}
        {#if triggerConfig.pause_reason}
          <div>
            <span class="text-xs text-gray-400 dark:text-gray-500">Pause Reason</span>
            <p class="text-gray-900 dark:text-white">{triggerConfig.pause_reason}</p>
          </div>
        {/if}
        {#if trigger.agent}
          <div>
            <span class="text-xs text-gray-400 dark:text-gray-500">Agent</span>
            <p class="text-gray-900 dark:text-white">{trigger.agent}</p>
          </div>
        {/if}
        {#if trigger.modelId}
          <div>
            <span class="text-xs text-gray-400 dark:text-gray-500">Model</span>
            <p class="text-gray-900 dark:text-white">{trigger.modelId}</p>
          </div>
        {/if}
        {#if trigger.providerId}
          <div>
            <span class="text-xs text-gray-400 dark:text-gray-500">Provider</span>
            <p class="text-gray-900 dark:text-white">{trigger.providerId}</p>
          </div>
        {/if}
        {#if trigger.harness}
          <div>
            <span class="text-xs text-gray-400 dark:text-gray-500">Harness</span>
            <p class="text-gray-900 dark:text-white">{trigger.harness}</p>
          </div>
        {/if}
        {#if trigger.variantId}
          <div>
            <span class="text-xs text-gray-400 dark:text-gray-500">Variant</span>
            <p class="text-gray-900 dark:text-white">{trigger.variantId}</p>
          </div>
        {/if}
        {#if trigger.skills && trigger.skills.length > 0}
          <div>
            <span class="text-xs text-gray-400 dark:text-gray-500">Skills</span>
            <p class="text-gray-900 dark:text-white">{trigger.skills.join(", ")}</p>
          </div>
        {/if}
        {#if trigger.gitUrl}
          <div class="sm:col-span-2">
            <span class="text-xs text-gray-400 dark:text-gray-500">Git URL</span>
            <p class="text-gray-900 dark:text-white font-mono text-xs">{trigger.gitUrl}</p>
          </div>
        {/if}
        {#if trigger.gitRef}
          <div>
            <span class="text-xs text-gray-400 dark:text-gray-500">Git Ref</span>
            <p class="text-gray-900 dark:text-white font-mono">{trigger.gitRef}</p>
          </div>
        {/if}
        {#if trigger.agentImage}
          <div>
            <span class="text-xs text-gray-400 dark:text-gray-500">Agent Image</span>
            <p class="text-gray-900 dark:text-white font-mono text-xs">{trigger.agentImage}</p>
          </div>
        {/if}
        {#if trigger.timeoutSec > 0}
          <div>
            <span class="text-xs text-gray-400 dark:text-gray-500">Timeout</span>
            <p class="text-gray-900 dark:text-white">{trigger.timeoutSec}s</p>
          </div>
        {/if}
        {#if trigger.lastRunAt}
          <div>
            <span class="text-xs text-gray-400 dark:text-gray-500">Last Run</span>
            <p class="text-gray-900 dark:text-white">{formatTime(trigger.lastRunAt)}</p>
          </div>
        {/if}
        {#if trigger.nextRunAt}
          <div>
            <span class="text-xs text-gray-400 dark:text-gray-500">Next Run</span>
            <p class="text-gray-900 dark:text-white">{formatTime(trigger.nextRunAt)}</p>
          </div>
        {/if}
        {#if trigger.sourceId}
          <div>
            <span class="text-xs text-gray-400 dark:text-gray-500">Source</span>
            <p class="text-gray-900 dark:text-white font-mono text-xs">{trigger.sourceId}</p>
          </div>
        {/if}
      </div>

      {#if trigger.prompt}
        <div class="mt-4">
          <span class="text-xs text-gray-400 dark:text-gray-500">Prompt</span>
          <pre class="mt-1 text-sm text-gray-900 dark:text-white whitespace-pre-wrap bg-white dark:bg-gray-700 p-2 rounded border border-gray-200 dark:border-gray-600">{trigger.prompt}</pre>
        </div>
      {/if}
    </Card>

    <Card size="xl" shadow="sm" class="w-full !p-0 mb-6">
      <div class="px-4 py-3 border-b border-gray-200 dark:border-gray-700">
        <h2 class="font-semibold text-gray-900 dark:text-white">Recent Runs</h2>
      </div>
      {#if loadingRuns}
        <div class="flex items-center gap-2 px-4 py-6 text-gray-500 dark:text-gray-400"><Spinner size="4" /> Loading runs…</div>
      {:else if runs.length === 0}
        <p class="px-4 py-6 text-center text-sm text-gray-500 dark:text-gray-400">No runs found</p>
      {:else}
        <div class="chetter-table overflow-x-auto">
        <Table hoverable={true} shadow={false}>
          <TableHead>
            <TableHeadCell>Run ID</TableHeadCell>
            <TableHeadCell>Task</TableHeadCell>
            <TableHeadCell>Status</TableHeadCell>
            <TableHeadCell>Tokens</TableHeadCell>
            <TableHeadCell>Triggered</TableHeadCell>
            <TableHeadCell>Created</TableHeadCell>
          </TableHead>
          <TableBody>
            {#each pagedRuns as run (run.id)}
              <TableBodyRow>
                <TableBodyCell class="font-mono text-xs">{run.id.slice(0, 16)}…</TableBodyCell>
                <TableBodyCell>
                  <a href={resolve("/tasks/[id]", { id: run.taskId })} class="font-mono text-xs text-blue-600 dark:text-blue-400 hover:underline">
                    {run.taskId.slice(0, 16)}…
                  </a>
                </TableBodyCell>
                <TableBodyCell><StatusBadge status={run.status} /></TableBodyCell>
                <TableBodyCell>
                  {#if runTokenTotals.has(run.taskId)}
                    <span class="font-mono text-xs text-gray-900 dark:text-white">{fmtTokens(runTokenTotals.get(run.taskId)!)}</span>
                  {:else}
                    <span class="text-gray-400 dark:text-gray-600 text-xs">—</span>
                  {/if}
                </TableBodyCell>
                <TableBodyCell class="text-xs text-gray-500 dark:text-gray-400">{formatTime(run.triggeredAt)}</TableBodyCell>
                <TableBodyCell class="text-xs text-gray-500 dark:text-gray-400">{formatTime(run.createdAt)}</TableBodyCell>
              </TableBodyRow>
            {/each}
          </TableBody>
        </Table>
        </div>
        <div class="flex items-center justify-between px-4 py-2 text-xs text-gray-500 dark:text-gray-400 border-t border-gray-200 dark:border-gray-700">
          <span>{runs.length > 0 ? runsPage * runsPageSize + 1 : 0}–{Math.min((runsPage + 1) * runsPageSize, runs.length)} of {runs.length}</span>
          <PaginationNav
            currentPage={runsPage + 1}
            totalPages={totalRunsPages}
            visiblePages={5}
            onPageChange={(nextPage) => { runsPage = nextPage - 1; }}
          />
        </div>
      {/if}
    </Card>
  {/if}
</div>
