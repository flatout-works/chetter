<script lang="ts">
  import { onMount } from "svelte";
  import { resolve } from "$app/paths";
  import { createClient } from "@connectrpc/connect";
  import { SessionService } from "$gen/proto/api/v1/api_pb";
  import type { AgentSession, SessionRun } from "$gen/proto/api/v1/api_pb";
  import { getTransport } from "$lib/api/client";
  import { formatTime } from "$lib/utils.svelte";
  import StatusBadge from "$lib/components/StatusBadge.svelte";
  import TableCard from "$lib/components/TableCard.svelte";
  import { Alert, Button, Card, Spinner, Table, TableHead, TableHeadCell, TableBody, TableBodyRow, TableBodyCell } from "flowbite-svelte";

  let { params } = $props();
  let session = $state<AgentSession | null>(null);
  let runs = $state<SessionRun[]>([]);
  let loading = $state(true);
  let error = $state<string | null>(null);

  async function resume() {
    const followUpPrompt = window.prompt("Enter follow-up prompt:");
    if (!followUpPrompt) return;
    try {
      const client = createClient(SessionService, getTransport());
      await client.resumeSession({ sessionId: params.id, prompt: followUpPrompt });
      await load();
    } catch (e) {
      error = e instanceof Error ? e.message : "Failed to resume session.";
      console.error(e);
    }
  }

  async function load() {
    try {
      const client = createClient(SessionService, getTransport());
      const resp = await client.getSession({ sessionId: params.id });
      session = resp.session ?? null;
      runs = resp.runs ?? [];
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
        <Button color="green" size="sm" onclick={resume}>Resume</Button>
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

    <TableCard title={`Session runs (${runs.length})`}>
    <Table hoverable={true} shadow={false}>
      <TableHead>
        <TableHeadCell>Run ID</TableHeadCell>
        <TableHeadCell>Task</TableHeadCell>
        <TableHeadCell>Status</TableHeadCell>
        <TableHeadCell>Summary</TableHeadCell>
        <TableHeadCell>Started</TableHeadCell>
      </TableHead>
      <TableBody>
        {#each runs as run (run.id)}
          <TableBodyRow>
            <TableBodyCell><span class="font-mono text-gray-700 dark:text-gray-300 text-xs">{run.id.slice(0, 20)}…</span></TableBodyCell>
            <TableBodyCell>
              <a href={resolve("/tasks/[id]", { id: run.taskId })} class="font-mono text-blue-600 dark:text-blue-400 hover:underline text-xs">
                {run.taskId.slice(0, 20)}…
              </a>
            </TableBodyCell>
            <TableBodyCell><StatusBadge status={run.status} /></TableBodyCell>
            <TableBodyCell class="max-w-xs"><span class="text-gray-500 dark:text-gray-400 truncate block">{run.summary || "—"}</span></TableBodyCell>
            <TableBodyCell><span class="text-gray-500 dark:text-gray-400 whitespace-nowrap">{formatTime(run.startedAt || "")}</span></TableBodyCell>
          </TableBodyRow>
        {:else}
          <TableBodyRow>
            <TableBodyCell colspan={5}>
              <div class="text-center text-gray-500 dark:text-gray-400 py-8">No runs recorded</div>
            </TableBodyCell>
          </TableBodyRow>
        {/each}
      </TableBody>
    </Table>
    </TableCard>
  {/if}
</div>
