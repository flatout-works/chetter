<script lang="ts">
  import { onMount } from "svelte";
  import { resolve } from "$app/paths";
  import { createClient } from "@connectrpc/connect";
  import { SessionService, FleetService } from "$gen/proto/api/v1/api_pb";
  import type { AgentSession } from "$gen/proto/api/v1/api_pb";
  import { getTransport } from "$lib/api/client";
  import { formatTime } from "$lib/utils.svelte";
  import StatusBadge from "$lib/components/StatusBadge.svelte";
  import TableCard from "$lib/components/TableCard.svelte";
  import { Button, Input, Label, Modal, PaginationNav, Select, Spinner, Table, TableHead, TableHeadCell, TableBody, TableBodyRow, TableBodyCell, Textarea } from "flowbite-svelte";

  type SortColumn = "id" | "status" | "agent" | "model" | "runs" | "created";
  let sessions = $state<AgentSession[]>([]);
  let loading = $state(true);
  let activeRunners = $state<string[]>([]);
  let statusFilter = $state("");
  let search = $state("");
  let page = $state(0);
  let pageSize = $state(25);
  let sortColumn = $state<SortColumn>("created");
  let sortDirection = $state<"asc" | "desc">("desc");

  let filteredSessions = $derived.by(() => {
    if (!search.trim()) return sessions;
    const q = search.toLowerCase();
    return sessions.filter(s => s.id?.toLowerCase().includes(q) || s.agent?.toLowerCase().includes(q));
  });

  let sortedSessions = $derived.by(() => {
    return [...filteredSessions].sort((a, b) => {
      let cmp = 0;
      switch (sortColumn) {
        case "id": cmp = a.id.localeCompare(b.id); break;
        case "status": cmp = a.status.localeCompare(b.status); break;
        case "agent": cmp = (a.agent || "").localeCompare(b.agent || ""); break;
        case "model": cmp = (a.modelId || "").localeCompare(b.modelId || ""); break;
        case "runs": cmp = a.runCount - b.runCount; break;
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
      const [sessionResp, fleetResp] = await Promise.all([
        createClient(SessionService, getTransport()).listSessions({ status: statusFilter, limit: 50 }),
        createClient(FleetService, getTransport()).getRunnerHealth({ includeTasks: false }),
      ]);
      sessions = sessionResp.sessions ?? [];
      activeRunners = (fleetResp.health?.runners ?? []).map((r) => r.runnerId);
    } catch (e) { console.error(e); }
    finally { loading = false; }
  }

  function canResume(session: AgentSession): boolean {
    if (session.status !== "paused" && session.status !== "recoverable" && session.status !== "paused_waiting_review") return false;
    if (session.pinnedRunnerId && !activeRunners.includes(session.pinnedRunnerId)) return false;
    return true;
  }

  function pinnedRunnerOffline(session: AgentSession): boolean {
    return !!session.pinnedRunnerId && !activeRunners.includes(session.pinnedRunnerId);
  }

  onMount(load);

  let showResume = $state(false);
  let resumeTarget = $state("");
  let resumePrompt = $state("");
  let resuming = $state(false);

  async function resume(sessionId: string) {
    resumeTarget = sessionId;
    resumePrompt = "";
    showResume = true;
  }

  async function doResume() {
    if (!resumePrompt.trim()) return;
    resuming = true;
    try {
      const client = createClient(SessionService, getTransport());
      await client.resumeSession({ sessionId: resumeTarget, prompt: resumePrompt.trim() });
      showResume = false;
      await load();
    } catch (e) { console.error(e); }
    finally { resuming = false; }
  }
</script>

<svelte:head>
  <title>Sessions — Chetter</title>
</svelte:head>

<div class="p-6">
  <div class="flex flex-wrap items-center justify-between mb-6 gap-3">
    <h1 class="text-2xl font-bold text-gray-900 dark:text-white">Agent Sessions</h1>
    <div class="flex flex-wrap items-center gap-2">
      <Select bind:value={statusFilter} onchange={() => { page = 0; load(); }} class="!w-auto">
          <option value="">All</option>
          <option value="running">Running</option>
          <option value="paused">Paused</option>
          <option value="recoverable">Recoverable</option>
          <option value="completed">Completed</option>
          <option value="error">Error</option>
        </Select>
        <Input bind:value={search} placeholder="Search…" class="!w-44" />
        <Select bind:value={pageSize} onchange={() => { page = 0; }} class="!w-auto">
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
        <TableHeadCell onclick={() => toggleSort("runs")} class="cursor-pointer select-none">Runs {sortIcon("runs")}</TableHeadCell>
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
            <TableBodyCell><span class="text-gray-500 dark:text-gray-400 font-mono">{session.runCount}</span></TableBodyCell>
            <TableBodyCell><span class="text-gray-500 dark:text-gray-400">{formatTime(session.createdAt)}</span></TableBodyCell>
            <TableBodyCell class="text-right">
              {#if session.status === "paused" || session.status === "recoverable" || session.status === "paused_waiting_review"}
                <Button color="green" size="xs" onclick={() => resume(session.id)} disabled={pinnedRunnerOffline(session)}>
                  Resume{#if pinnedRunnerOffline(session)} (pinned runner offline){/if}
                </Button>
              {/if}
            </TableBodyCell>
          </TableBodyRow>
        {:else}
          <TableBodyRow>
            <TableBodyCell colspan={7}>
              <div class="text-center text-gray-500 dark:text-gray-400 py-8">No sessions found</div>
            </TableBodyCell>
          </TableBodyRow>
        {/each}
      </TableBody>
    </Table>
    </TableCard>

    <div class="flex flex-wrap items-center justify-between mt-4 text-sm text-gray-500 dark:text-gray-400 gap-3">
      <span>Showing {sortedSessions.length > 0 ? page * pageSize + 1 : 0}–{Math.min((page + 1) * pageSize, sortedSessions.length)} of {sortedSessions.length}</span>
      <PaginationNav
        currentPage={page + 1}
        {totalPages}
        visiblePages={5}
        onPageChange={(nextPage) => { page = nextPage - 1; }}
      />
    </div>
  {/if}
</div>

<Modal title="Resume Session" bind:open={showResume} size="md" onclose={() => showResume = false}>
  <div class="space-y-4">
    <div>
      <Label for="resume-prompt" class="mb-2">Follow-up prompt</Label>
      <Textarea id="resume-prompt" bind:value={resumePrompt} placeholder="Enter follow-up prompt for the agent" rows={4} class="w-full" />
    </div>
    <Button color="blue" disabled={!resumePrompt.trim() || resuming} onclick={doResume} class="w-full">
      {resuming ? "Resuming…" : "Resume"}
    </Button>
  </div>
</Modal>
