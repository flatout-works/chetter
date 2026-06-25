<script lang="ts">
  import { onDestroy, onMount } from "svelte";
  import { createClient } from "@connectrpc/connect";
  import { AdminService } from "$gen/proto/api/v1/api_pb";
  import type { AuditEvent } from "$gen/proto/api/v1/api_pb";
  import { getTransport } from "$lib/api/client";
  import { formatTime } from "$lib/utils.svelte";
  import StatusBadge from "$lib/components/StatusBadge.svelte";
  import TableCard from "$lib/components/TableCard.svelte";
  import { Button, Input, PaginationNav, Select, Spinner, Toggle, Table, TableHead, TableHeadCell, TableBody, TableBodyRow, TableBodyCell } from "flowbite-svelte";

  type SortColumn = "time" | "event" | "source" | "target" | "detail";
  let events = $state<AuditEvent[]>([]);
  let loading = $state(true);
  let eventTypeFilter = $state("");
  let sourceTypeFilter = $state("");
  let sinceHours = $state(24);
  let limit = $state(100);
  let offset = $state(0);
  let sortColumn = $state<SortColumn>("time");
  let sortDirection = $state<"asc" | "desc">("desc");

  let showSync = $state(false);
  let showTriggers = $state(true);
  let showResumes = $state(true);
  let expandedDetailId = $state<string | null>(null);

  function sourceLink(event: AuditEvent): string | null {
    if (!event.sourceType || !event.sourceId) return null;
    if (event.sourceType === "task") return `/tasks/${event.sourceId}`;
    if (event.sourceType === "trigger") return "/triggers";
    return null;
  }

  function targetLink(event: AuditEvent): string | null {
    if (!event.targetType || !event.targetId) return null;
    if (event.targetType === "task") return `/tasks/${event.targetId}`;
    if (event.targetType === "agent_session") return `/sessions/${event.targetId}`;
    return null;
  }

  function targetGitHubLink(event: AuditEvent): string | null {
    if (!event.targetId || !event.targetType) return null;
    const hashIdx = event.targetId.lastIndexOf("#");
    if (hashIdx < 0) return null;
    const number = event.targetId.slice(hashIdx + 1);
    const repo = event.targetId.slice(0, hashIdx);
    if (event.targetType === "issue") return `https://github.com/${repo}/issues/${number}`;
    if (event.targetType === "pull_request" || event.targetType === "pr") return `https://github.com/${repo}/pull/${number}`;
    return null;
  }

  function toggleDetail(id: string) {
    expandedDetailId = expandedDetailId === id ? null : id;
  }

  const excludedTypes = $derived(new Set([
    ...(showSync ? [] : ["definitions_synced"]),
    ...(showTriggers ? [] : ["trigger_run"]),
    ...(showResumes ? [] : ["session_resumed"]),
  ]));

  let sortedEvents = $derived.by(() => {
    return [...events].sort((a, b) => {
      let cmp = 0;
      switch (sortColumn) {
        case "time": cmp = a.createdAt.localeCompare(b.createdAt); break;
        case "event": cmp = a.eventType.localeCompare(b.eventType); break;
        case "source": cmp = (a.sourceType || "").localeCompare(b.sourceType || ""); break;
        case "target": cmp = (a.targetType || "").localeCompare(b.targetType || ""); break;
        case "detail": cmp = (a.detail || "").localeCompare(b.detail || ""); break;
      }
      return sortDirection === "asc" ? cmp : -cmp;
    });
  });

  let currentPage = $derived(Math.floor(offset / limit) + 1);
  let totalPages = $derived(currentPage + (events.length >= limit ? 1 : 0));

  function toggleSort(col: SortColumn) {
    if (sortColumn === col) { sortDirection = sortDirection === "asc" ? "desc" : "asc"; }
    else { sortColumn = col; sortDirection = col === "time" ? "desc" : "asc"; }
  }

  function sortIcon(col: SortColumn): string {
    if (sortColumn !== col) return "↕";
    return sortDirection === "asc" ? "↑" : "↓";
  }

  async function load() {
    loading = true;
    try {
      const client = createClient(AdminService, getTransport());
      const resp = await client.listAuditEvents({
        eventType: eventTypeFilter || undefined, sourceType: sourceTypeFilter || undefined,
        sinceHours, limit, offset,
      });
      let filtered = resp.events ?? [];
      filtered = filtered.filter((e) => !excludedTypes.has(e.eventType));
      events = filtered;
    } catch (e) { console.error(e); }
    finally { loading = false; }
  }

  let refreshInterval: ReturnType<typeof setInterval> | undefined;

  onMount(() => {
    load();
    refreshInterval = setInterval(load, 15_000);
  });

  onDestroy(() => {
    if (refreshInterval) clearInterval(refreshInterval);
  });
</script>

<svelte:head>
  <title>Audit Log — Chetter</title>
</svelte:head>

<div class="p-6">
  <div class="flex flex-wrap items-center justify-between mb-6 gap-3">
    <h1 class="text-2xl font-bold text-gray-900 dark:text-white">Audit Log</h1>
    <div class="flex flex-wrap items-center gap-2">
      <Select bind:value={eventTypeFilter} placeholder="" onchange={() => { offset = 0; load(); }} class="!w-auto min-w-40">
        <option value="">All types</option>
        <option value="webhook_received">Webhook Received</option>
        <option value="webhook_author_gate_denied">Webhook Author Gate Denied</option>
        <option value="task_submitted">Task Submitted</option>
        <option value="trigger_matched">Trigger Matched</option>
        <option value="artifact_discovered">Artifact Discovered</option>
      </Select>
      <Select bind:value={sourceTypeFilter} placeholder="" onchange={() => { offset = 0; load(); }} class="!w-auto min-w-40">
        <option value="">All sources</option>
        <option value="webhook">Webhook</option>
        <option value="trigger">Trigger</option>
        <option value="task">Task</option>
      </Select>
      <Select bind:value={sinceHours} placeholder="" onchange={() => { offset = 0; load(); }} class="!w-auto min-w-44">
        <option value={1}>Last hour</option>
        <option value={6}>Last 6 hours</option>
        <option value={24}>Last 24 hours</option>
        <option value={72}>Last 3 days</option>
        <option value={168}>Last 7 days</option>
      </Select>
      <Input type="number" bind:value={limit} placeholder="Limit" class="!w-20" />
      <Button color="blue" size="sm" onclick={() => { offset = 0; load(); }}>Refresh</Button>
      <div class="flex items-center gap-3 ml-2 border-l border-gray-300 dark:border-gray-600 pl-3">
        <Toggle bind:checked={showSync} onchange={() => { offset = 0; load(); }} color="gray" size="small">Syncs</Toggle>
        <Toggle bind:checked={showTriggers} onchange={() => { offset = 0; load(); }} color="gray" size="small">Triggers</Toggle>
        <Toggle bind:checked={showResumes} onchange={() => { offset = 0; load(); }} color="gray" size="small">Resumes</Toggle>
      </div>
    </div>
  </div>

  {#if loading}
    <div class="flex items-center gap-2 text-gray-500 dark:text-gray-400"><Spinner size="4" /> Loading…</div>
  {:else}
    <TableCard title="Audit events" subtitle="Server-side event history for webhook, task, trigger, and artifact activity.">
    <Table hoverable={true} shadow={false}>
      <TableHead>
        <TableHeadCell onclick={() => toggleSort("time")} class="cursor-pointer select-none">Time {sortIcon("time")}</TableHeadCell>
        <TableHeadCell onclick={() => toggleSort("event")} class="cursor-pointer select-none">Event Type {sortIcon("event")}</TableHeadCell>
        <TableHeadCell>Repo</TableHeadCell>
        <TableHeadCell onclick={() => toggleSort("source")} class="cursor-pointer select-none">Source {sortIcon("source")}</TableHeadCell>
        <TableHeadCell onclick={() => toggleSort("target")} class="cursor-pointer select-none">Target {sortIcon("target")}</TableHeadCell>
        <TableHeadCell onclick={() => toggleSort("detail")} class="cursor-pointer select-none">Detail {sortIcon("detail")}</TableHeadCell>
      </TableHead>
      <TableBody>
        {#each sortedEvents as event (event.id)}
          <TableBodyRow>
            <TableBodyCell><span class="text-gray-500 dark:text-gray-400 whitespace-nowrap">{formatTime(event.createdAt)}</span></TableBodyCell>
            <TableBodyCell><StatusBadge status={event.eventType} /></TableBodyCell>
            <TableBodyCell>
              <span class="text-gray-700 dark:text-gray-300 text-sm">{event.repo || "—"}</span>
            </TableBodyCell>
            <TableBodyCell>
              <span class="text-gray-700 dark:text-gray-300">
                {#if event.sourceType}
                  {#if sourceLink(event)}
                    <a href={sourceLink(event)!} class="font-medium text-blue-600 dark:text-blue-400 hover:underline">{event.sourceType}</a>
                  {:else}
                    <span class="font-medium">{event.sourceType}</span>
                  {/if}
                  {#if event.sourceId}
                    <span class="text-gray-500" title={event.sourceId}>: {event.sourceId.slice(0, 24)}</span>
                  {/if}
                {:else}
                  <span class="text-gray-400">—</span>
                {/if}
              </span>
            </TableBodyCell>
            <TableBodyCell>
              <span class="text-gray-700 dark:text-gray-300">
                {#if event.targetType}
                  {#if targetLink(event)}
                    <a href={targetLink(event)!} class="font-medium text-blue-600 dark:text-blue-400 hover:underline">{event.targetType}</a>
                  {:else if targetGitHubLink(event)}
                    <a href={targetGitHubLink(event)!} target="_blank" rel="noopener" class="font-medium text-blue-600 dark:text-blue-400 hover:underline">{event.targetType}</a>
                  {:else}
                    <span class="font-medium">{event.targetType}</span>
                  {/if}
                  {#if event.targetId}
                    <span class="text-gray-500" title={event.targetId}>: {event.targetId.slice(0, 24)}</span>
                  {/if}
                {:else}
                  <span class="text-gray-400">—</span>
                {/if}
              </span>
            </TableBodyCell>
            <TableBodyCell class="max-w-xs">
              {#if expandedDetailId === event.id}
                <span class="text-gray-500 dark:text-gray-400 whitespace-pre-wrap break-words block mb-1">{event.detail || "—"}</span>
                <button class="text-blue-600 dark:text-blue-400 cursor-pointer text-xs bg-transparent border-0 p-0" onclick={() => toggleDetail(event.id)}>Show less</button>
              {:else}
                <span class="text-gray-500 dark:text-gray-400 truncate block" title={event.detail}>{event.detail || "—"}</span>
                {#if (event.detail?.length ?? 0) > 60}
                  <button class="text-blue-600 dark:text-blue-400 cursor-pointer text-xs bg-transparent border-0 p-0" onclick={() => toggleDetail(event.id)}>Show more</button>
                {/if}
              {/if}
            </TableBodyCell>
          </TableBodyRow>
        {:else}
          <TableBodyRow>
            <TableBodyCell colspan={6}>
              <div class="text-center text-gray-500 dark:text-gray-400 py-8">No audit events found</div>
            </TableBodyCell>
          </TableBodyRow>
        {/each}
      </TableBody>
    </Table>
    </TableCard>

    <div class="flex items-center justify-between mt-4 text-sm text-gray-500 dark:text-gray-400">
      <span>Showing {events.length > 0 ? offset + 1 : 0}–{offset + events.length} of {events.length < limit ? offset + events.length : `${offset + events.length}+`}</span>
      <PaginationNav
        {currentPage}
        {totalPages}
        visiblePages={5}
        onPageChange={(nextPage) => { offset = (nextPage - 1) * limit; load(); }}
      />
    </div>
  {/if}
</div>
