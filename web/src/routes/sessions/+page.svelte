<script lang="ts">
  import { onMount } from "svelte";
  import { resolve } from "$app/paths";
  import { createClient } from "@connectrpc/connect";
  import { SessionService } from "$gen/proto/api/v1/api_pb";
  import type { AgentSession } from "$gen/proto/api/v1/api_pb";
  import { getTransport } from "$lib/api/client";
  import { formatTime } from "$lib/utils.svelte";
  import StatusBadge from "$lib/components/StatusBadge.svelte";
  import TableCard from "$lib/components/TableCard.svelte";
  import { Button, Select, Spinner, Table, TableHead, TableHeadCell, TableBody, TableBodyRow, TableBodyCell } from "flowbite-svelte";

  type SortColumn = "id" | "status" | "agent" | "model" | "created";
  let sessions = $state<AgentSession[]>([]);
  let loading = $state(true);
  let statusFilter = $state("");
  let page = $state(0);
  let pageSize = $state(25);
  let sortColumn = $state<SortColumn>("created");
  let sortDirection = $state<"asc" | "desc">("desc");

  let sortedSessions = $derived.by(() => {
    return [...sessions].sort((a, b) => {
      let cmp = 0;
      switch (sortColumn) {
        case "id": cmp = a.id.localeCompare(b.id); break;
        case "status": cmp = a.status.localeCompare(b.status); break;
        case "agent": cmp = (a.agent || "").localeCompare(b.agent || ""); break;
        case "model": cmp = (a.modelId || "").localeCompare(b.modelId || ""); break;
        case "created": cmp = a.createdAt.localeCompare(b.createdAt); break;
      }
      return sortDirection === "asc" ? cmp : -cmp;
    });
  });

  let totalPages = $derived(Math.max(1, Math.ceil(sortedSessions.length / pageSize)));
  let pagedSessions = $derived(sortedSessions.slice(page * pageSize, (page + 1) * pageSize));

  function toggleSort(col: SortColumn) {
    if (sortColumn === col) { sortDirection = sortDirection === "asc" ? "desc" : "asc"; }
    else { sortColumn = col; sortDirection = col === "created" ? "desc" : "asc"; }
    page = 0;
  }

  function sortIcon(col: SortColumn): string {
    if (sortColumn !== col) return "↕";
    return sortDirection === "asc" ? "↑" : "↓";
  }

  async function load() {
    try {
      const client = createClient(SessionService, getTransport());
      const resp = await client.listSessions({ status: statusFilter, limit: 50 });
      sessions = resp.sessions ?? [];
    } catch (e) { console.error(e); }
    finally { loading = false; }
  }

  onMount(load);

  async function resume(sessionId: string) {
    const followUpPrompt = window.prompt("Enter follow-up prompt:");
    if (!followUpPrompt) return;
    try {
      const client = createClient(SessionService, getTransport());
      await client.resumeSession({ sessionId, prompt: followUpPrompt });
      await load();
    } catch (e) { console.error(e); }
  }
</script>

<svelte:head>
  <title>Sessions — Chetter</title>
</svelte:head>

<div class="p-6">
  <div class="flex items-center justify-between mb-6">
    <h1 class="text-2xl font-bold text-gray-900 dark:text-white">Agent Sessions</h1>
    <div class="flex items-center gap-3">
      <Select bind:value={statusFilter} onchange={() => { page = 0; load(); }}>
          <option value="">All</option>
          <option value="running">Running</option>
          <option value="paused_waiting_review">Paused</option>
          <option value="completed">Completed</option>
          <option value="error">Error</option>
        </Select>
        <Select bind:value={pageSize} onchange={() => { page = 0; }}>
          <option value={10}>10 / page</option>
          <option value={25}>25 / page</option>
          <option value={50}>50 / page</option>
        </Select>
    </div>
  </div>

  {#if loading}
    <div class="flex items-center gap-2 text-gray-500 dark:text-gray-400"><Spinner size="4" /> Loading…</div>
  {:else}
    <TableCard title="Agent sessions" subtitle="Recent resumable agent sessions, newest first.">
    <Table hoverable={true} shadow={false}>
      <TableHead>
        <TableHeadCell onclick={() => toggleSort("id")} class="cursor-pointer select-none">Session ID {sortIcon("id")}</TableHeadCell>
        <TableHeadCell onclick={() => toggleSort("status")} class="cursor-pointer select-none">Status {sortIcon("status")}</TableHeadCell>
        <TableHeadCell onclick={() => toggleSort("agent")} class="cursor-pointer select-none">Agent {sortIcon("agent")}</TableHeadCell>
        <TableHeadCell onclick={() => toggleSort("model")} class="cursor-pointer select-none">Model {sortIcon("model")}</TableHeadCell>
        <TableHeadCell onclick={() => toggleSort("created")} class="cursor-pointer select-none">Created {sortIcon("created")}</TableHeadCell>
        <TableHeadCell class="text-right">Actions</TableHeadCell>
      </TableHead>
      <TableBody>
        {#each pagedSessions as session (session.id)}
          <TableBodyRow>
            <TableBodyCell>
              <a href={resolve("/sessions/[id]", { id: session.id })} class="font-mono text-blue-600 dark:text-blue-400 hover:underline text-xs">
                {session.id.slice(0, 20)}…
              </a>
            </TableBodyCell>
            <TableBodyCell><StatusBadge status={session.status} /></TableBodyCell>
            <TableBodyCell><span class="text-gray-700 dark:text-gray-300">{session.agent || "—"}</span></TableBodyCell>
            <TableBodyCell><span class="text-gray-700 dark:text-gray-300">{session.modelId || "—"}</span></TableBodyCell>
            <TableBodyCell><span class="text-gray-500 dark:text-gray-400">{formatTime(session.createdAt)}</span></TableBodyCell>
            <TableBodyCell class="text-right">
              {#if session.status === "paused_waiting_review"}
                <Button color="green" size="xs" onclick={() => resume(session.id)}>Resume</Button>
              {/if}
            </TableBodyCell>
          </TableBodyRow>
        {:else}
          <TableBodyRow>
            <TableBodyCell colspan={6}>
              <div class="text-center text-gray-500 dark:text-gray-400 py-8">No sessions found</div>
            </TableBodyCell>
          </TableBodyRow>
        {/each}
      </TableBody>
    </Table>
    </TableCard>

    <div class="flex items-center justify-between mt-4 text-sm text-gray-500 dark:text-gray-400">
      <span>Showing {sortedSessions.length > 0 ? page * pageSize + 1 : 0}–{Math.min((page + 1) * pageSize, sortedSessions.length)} of {sortedSessions.length}</span>
      <div class="flex gap-2">
        <Button size="xs" color="alternative" onclick={() => { page = Math.max(0, page - 1); }} disabled={page === 0}>← Prev</Button>
        {#each { length: totalPages } as _, i}
          <Button size="xs" color={i === page ? "blue" : "alternative"} onclick={() => { page = i; }}>{i + 1}</Button>
        {/each}
        <Button size="xs" color="alternative" onclick={() => { page = Math.min(totalPages - 1, page + 1); }} disabled={page >= totalPages - 1}>Next →</Button>
      </div>
    </div>
  {/if}
</div>
