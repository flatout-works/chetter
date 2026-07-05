<script lang="ts">
  import { onDestroy, onMount } from "svelte";
  import { page } from "$app/stores";
  import { goto } from "$app/navigation";
  import { createClient } from "@connectrpc/connect";
  import { AdminService } from "$gen/proto/api/v1/api_pb";
  import type { AuditEvent } from "$gen/proto/api/v1/api_pb";
  import { getTransport } from "$lib/api/client";
  import { formatTime } from "$lib/utils.svelte";
  import StatusBadge from "$lib/components/StatusBadge.svelte";
  import TableCard from "$lib/components/TableCard.svelte";
  import { Button, Input, PaginationNav, Search, Select, Spinner, Toggle, Table, TableHead, TableHeadCell, TableBody, TableBodyRow, TableBodyCell } from "flowbite-svelte";

  const initialUrl = new URL($page.url);
  let p = $derived($page.url);

  function initialParam(name: string): string {
    return initialUrl.searchParams.get(name) || "";
  }

  function initialNumberParam(name: string, fallback: number): number {
    return Number(initialUrl.searchParams.get(name)) || fallback;
  }

  function initialBoolParam(name: string, def = true): boolean {
    const v = initialUrl.searchParams.get(name);
    if (v === null) return def;
    return v === "1";
  }

  type SortColumn = "time" | "event" | "source" | "target" | "detail";
  let events = $state<AuditEvent[]>([]);
  let loading = $state(true);
  let firstLoad = $state(true);
  let eventTypeFilter = $state(initialParam("event"));
  let sourceTypeFilter = $state(initialParam("source"));
  let sinceHours = $state(initialNumberParam("hours", 24));
  let pageNum = $state(initialNumberParam("page", 0));
  let pageSize = $state(initialNumberParam("size", 25));
  let search = $state(initialParam("q"));
  let offset = $derived(pageNum * pageSize);
  let sortColumn = $state<SortColumn>("time");
  let sortDirection = $state<"asc" | "desc">("desc");

  let showSync = $state(initialBoolParam("sync", true));
  let showTriggers = $state(initialBoolParam("trigger", true));
  let showResumes = $state(initialBoolParam("resume", true));
  let showGate = $state(initialBoolParam("gate", true));

  function syncURL() {
    const u = new URL(p);
    const s = (k: string, v: string, def: string = "") => v && v !== def ? u.searchParams.set(k, v) : u.searchParams.delete(k);
    s("event", eventTypeFilter);
    s("source", sourceTypeFilter);
    s("hours", String(sinceHours), "24");
    s("size", String(pageSize), "25");
    s("page", String(pageNum), "0");
    s("q", search.trim());
    s("sync", showSync ? "" : "0");
    s("trigger", showTriggers ? "" : "0");
    s("resume", showResumes ? "" : "0");
    s("gate", showGate ? "" : "0");
    if (u.href !== p.href) goto(u, { replaceState: true, noScroll: true, keepFocus: true });
  }

  $effect(() => { eventTypeFilter; sourceTypeFilter; sinceHours; pageSize; pageNum; search; showSync; showTriggers; showResumes; showGate; syncURL(); });
  let expandedDetailId = $state<string | null>(null);

  function sourceLink(event: AuditEvent): string | null {
    if (!event.sourceType || !event.sourceId) return null;
    if (event.sourceType === "task") return `/tasks/${event.sourceId}`;
    if (event.sourceType === "trigger") return `/triggers/${event.sourceId}`;
    if (event.sourceType === "agent_session" || event.sourceType === "session") return `/sessions/${event.sourceId}`;
    return parseIdLink(event.sourceId)?.href ?? null;
  }

  function targetLink(event: AuditEvent): string | null {
    if (!event.targetType || !event.targetId) return null;
    if (event.targetType === "task") return `/tasks/${event.targetId}`;
    if (event.targetType === "trigger") return `/triggers/${event.targetId}`;
    if (event.targetType === "agent_session" || event.targetType === "session") return `/sessions/${event.targetId}`;
    return parseIdLink(event.targetId)?.href ?? null;
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

  function repoLink(repo: string): string {
    return `https://github.com/${repo}`;
  }

  function parseIdLink(id: string): { label: string; href: string; external: boolean } | null {
    if (!id) return null;
    const triggerMatch = id.match(/^trigger\s*:\s*(\S+)/i);
    if (triggerMatch) return { label: id, href: `/triggers/${triggerMatch[1]}`, external: false };
    const sessionMatch = id.match(/^session\s*:\s*(\S+)/i);
    if (sessionMatch) return { label: id, href: `/sessions/${sessionMatch[1]}`, external: false };
    const repoPrefixMatch = id.match(/^repo\s*:\s*([\w.-]+\/[\w.-]+)/i);
    if (repoPrefixMatch) return { label: id, href: `https://github.com/${repoPrefixMatch[1]}`, external: true };
    const ghIssueMatch = id.match(/^(.+)#(\d+)$/);
    if (ghIssueMatch) return { label: id, href: `https://github.com/${ghIssueMatch[1]}/issues/${ghIssueMatch[2]}`, external: true };
    const bareRepoMatch = id.match(/^[\w.-]+\/[\w.-]+$/);
    if (bareRepoMatch) return { label: id, href: `https://github.com/${bareRepoMatch[0]}`, external: true };
    return null;
  }

  type DetailSegment = { type: "text"; text: string } | { type: "link"; text: string; href: string; external: boolean };

  function linkifyDetail(detail: string | undefined): DetailSegment[] {
    if (!detail) return [{ type: "text", text: "—" }];
    const regex = /(cron|trigger|session|repo)\s*:\s*(\S+)/gi;
    const segments: DetailSegment[] = [];
    let lastIndex = 0;
    let match: RegExpExecArray | null;
    while ((match = regex.exec(detail)) !== null) {
      const keyword = match[1].toLowerCase();
      const value = match[2];
      if (match.index > lastIndex) {
        segments.push({ type: "text", text: detail.slice(lastIndex, match.index) });
      }
      let href: string;
      let external = false;
      switch (keyword) {
        case "cron": href = "/triggers"; break;
        case "trigger": href = `/triggers/${value}`; break;
        case "session": href = `/sessions/${value}`; break;
        case "repo": href = `https://github.com/${value}`; external = true; break;
        default: href = "#";
      }
      segments.push({ type: "link", text: match[0], href, external });
      lastIndex = regex.lastIndex;
    }
    if (lastIndex < detail.length) {
      segments.push({ type: "text", text: detail.slice(lastIndex) });
    }
    return segments.length > 0 ? segments : [{ type: "text", text: detail }];
  }

  function toggleDetail(id: string) {
    expandedDetailId = expandedDetailId === id ? null : id;
  }

  const excludeTypes = $derived([
    ...(showSync ? [] : ["definitions_synced"]),
    ...(showTriggers ? [] : ["trigger_run", "trigger_updated"]),
    ...(showResumes ? [] : ["session_resumed"]),
    ...(showGate ? [] : ["webhook_author_gate_denied"]),
  ]);

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

  let currentPage = $derived(pageNum + 1);
  let totalPages = $derived(currentPage + (events.length >= pageSize ? 1 : 0));

  function toggleSort(col: SortColumn) {
    if (sortColumn === col) { sortDirection = sortDirection === "asc" ? "desc" : "asc"; }
    else { sortColumn = col; sortDirection = col === "time" ? "desc" : "asc"; }
  }

  function sortIcon(col: SortColumn): string {
    if (sortColumn !== col) return "↕";
    return sortDirection === "asc" ? "↑" : "↓";
  }

  async function load(silent = false) {
    if (!silent) loading = true;
    try {
      const client = createClient(AdminService, getTransport());
      const resp = await client.listAuditEvents({
        ...(eventTypeFilter ? { eventType: eventTypeFilter } : {}),
        ...(sourceTypeFilter ? { sourceType: sourceTypeFilter } : {}),
        ...(search ? { search } : {}),
        ...(excludeTypes.length > 0 ? { excludeTypes } : {}),
        sinceHours, limit: pageSize, offset,
      });
      events = resp.events ?? [];
    } catch (e) { console.error(e); }
    finally {
      loading = false;
      firstLoad = false;
    }
  }

  let refreshInterval: ReturnType<typeof setInterval> | undefined;

  onMount(() => {
    load();
    refreshInterval = setInterval(() => load(true), 15_000);
  });

  onDestroy(() => {
    if (refreshInterval) clearInterval(refreshInterval);
  });
</script>

<svelte:head>
  <title>Audit Log — Chetter</title>
</svelte:head>

<div class="p-6">
  <div class="mb-4">
    <h1 class="text-2xl font-bold text-gray-900 dark:text-white">Audit Log</h1>
  </div>

  <div class="flex flex-wrap items-center justify-between mb-6 gap-3">
    <Search
      bind:value={search}
      placeholder="Search audit log…"
      class="!w-72"
      size="md"
      onkeydown={(e) => { if (e.key === "Enter") { pageNum = 0; load(); } }}
    />
    <div class="flex flex-wrap items-center gap-2">
      <Select bind:value={eventTypeFilter} placeholder="" onchange={() => { pageNum = 0; load(); }} class="!w-auto min-w-48">
        <option value="">All types</option>
        <option value="webhook_received">Webhook Received</option>
        <option value="webhook_author_gate_denied">Webhook Author Gate Denied</option>
        <option value="task_submitted">Task Submitted</option>
        <option value="task_cancelled">Task Cancelled</option>
        <option value="trigger_run">Trigger Run</option>
        <option value="trigger_updated">Trigger Updated</option>
        <option value="github_artifact_created">GitHub Artifact Created</option>
      </Select>
      <Select bind:value={sourceTypeFilter} placeholder="" onchange={() => { pageNum = 0; load(); }} class="!w-auto min-w-48">
        <option value="">All sources</option>
        <option value="webhook">Webhook</option>
        <option value="trigger">Trigger</option>
        <option value="api">API</option>
        <option value="cron">Cron</option>
        <option value="task">Task</option>
        <option value="rpc">RPC</option>
      </Select>
      <Select bind:value={sinceHours} placeholder="" onchange={() => { pageNum = 0; load(); }} class="!w-auto min-w-44">
        <option value={1}>Last hour</option>
        <option value={6}>Last 6 hours</option>
        <option value={24}>Last 24 hours</option>
        <option value={72}>Last 3 days</option>
        <option value={168}>Last 7 days</option>
      </Select>
      <Select bind:value={pageSize} onchange={() => { pageNum = 0; load(); }} class="!w-auto">
        <option value={10}>10 / page</option>
        <option value={25}>25 / page</option>
        <option value={50}>50 / page</option>
        <option value={100}>100 / page</option>
      </Select>
    </div>
  </div>

  <div class="flex flex-wrap items-center gap-3 mb-6 text-sm text-gray-600 dark:text-gray-400">
    <Toggle bind:checked={showSync} onchange={() => { pageNum = 0; load(); }} color="gray" size="small">Sync</Toggle>
    <Toggle bind:checked={showTriggers} onchange={() => { pageNum = 0; load(); }} color="gray" size="small">Trigger</Toggle>
    <Toggle bind:checked={showResumes} onchange={() => { pageNum = 0; load(); }} color="gray" size="small">Resume</Toggle>
    <Toggle bind:checked={showGate} onchange={() => { pageNum = 0; load(); }} color="gray" size="small">Auth Gate</Toggle>
  </div>

  {#if firstLoad && loading}
    <div class="flex items-center gap-2 text-gray-500 dark:text-gray-400"><Spinner size="4" /> Loading…</div>
  {:else}
    <TableCard title="Audit events" subtitle="Server-side event history for webhook, task, trigger, and artifact activity.">
    <Table hoverable={true} shadow={false}>
      <TableHead>
        <TableHeadCell onclick={() => toggleSort("time")} class="cursor-pointer select-none">Time {sortIcon("time")}</TableHeadCell>
        <TableHeadCell onclick={() => toggleSort("event")} class="cursor-pointer select-none">Event Type {sortIcon("event")}</TableHeadCell>
        <TableHeadCell>Source</TableHeadCell>
        <TableHeadCell>Target</TableHeadCell>
        <TableHeadCell>Token</TableHeadCell>
        <TableHeadCell>Repo</TableHeadCell>
        <TableHeadCell onclick={() => toggleSort("detail")} class="cursor-pointer select-none">Detail {sortIcon("detail")}</TableHeadCell>
      </TableHead>
      <TableBody>
        {#each sortedEvents as event (event.id)}
          <TableBodyRow>
            <TableBodyCell><span class="text-gray-500 dark:text-gray-400 whitespace-nowrap">{formatTime(event.createdAt)}</span></TableBodyCell>
            <TableBodyCell><StatusBadge status={event.eventType} /></TableBodyCell>
            <TableBodyCell>
              <span class="text-gray-700 dark:text-gray-300">
                {#if event.sourceType}
                  {#if sourceLink(event)}
                    {@const href = sourceLink(event)!}
                    {@const ext = href.startsWith("http")}
                    <a href={href} target={ext ? "_blank" : undefined} rel={ext ? "noopener" : undefined} class="font-medium text-blue-600 dark:text-blue-400 hover:underline">{event.sourceType}: {event.sourceId.slice(0, 24)}</a>
                  {:else}
                    <span class="font-medium">{event.sourceType}</span>
                    {#if event.sourceId}
                      <span class="text-gray-500" title={event.sourceId}>: {event.sourceId.slice(0, 24)}</span>
                    {/if}
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
                    {@const href = targetLink(event)!}
                    {@const ext = href.startsWith("http")}
                    <a href={href} target={ext ? "_blank" : undefined} rel={ext ? "noopener" : undefined} class="font-medium text-blue-600 dark:text-blue-400 hover:underline">{event.targetType}: {event.targetId.slice(0, 24)}</a>
                  {:else if targetGitHubLink(event)}
                    {@const ghHref = targetGitHubLink(event)!}
                    <a href={ghHref} target="_blank" rel="noopener" class="font-medium text-blue-600 dark:text-blue-400 hover:underline">{event.targetType}: {event.targetId.slice(0, 24)}</a>
                  {:else}
                    <span class="font-medium">{event.targetType}</span>
                    {#if event.targetId}
                      <span class="text-gray-500" title={event.targetId}>: {event.targetId.slice(0, 24)}</span>
                    {/if}
                  {/if}
                {:else}
                  <span class="text-gray-400">—</span>
                {/if}
              </span>
            </TableBodyCell>
            <TableBodyCell>
              {#if event.tokenName}
                <span class="text-gray-700 dark:text-gray-300" title={event.tokenId}>{event.tokenName}</span>
              {:else}
                <span class="text-gray-400 text-sm">—</span>
              {/if}
            </TableBodyCell>
            <TableBodyCell>
              {#if event.repo}
                <a href={repoLink(event.repo)} target="_blank" rel="noopener" class="text-blue-600 dark:text-blue-400 hover:underline text-sm">{event.repo}</a>
              {:else}
                <span class="text-gray-400 text-sm">—</span>
              {/if}
            </TableBodyCell>
            <TableBodyCell class="max-w-xs">
              {#if expandedDetailId === event.id}
                {@const detailSegments = linkifyDetail(event.detail)}
                <span class="text-gray-500 dark:text-gray-400 whitespace-pre-wrap break-words block mb-1">
                  {#each detailSegments as seg, i (`${seg.type}:${seg.text}:${i}`)}
                    {#if seg.type === "link"}
                      <a href={seg.href} target={seg.external ? "_blank" : undefined} rel={seg.external ? "noopener" : undefined} class="text-blue-600 dark:text-blue-400 hover:underline">{seg.text}</a>
                    {:else}
                      {seg.text}
                    {/if}
                  {/each}
                </span>
                <button class="text-blue-600 dark:text-blue-400 cursor-pointer text-xs bg-transparent border-0 p-0" onclick={() => toggleDetail(event.id)}>Show less</button>
              {:else}
                {@const detailSegments = linkifyDetail(event.detail)}
                <span class="text-gray-500 dark:text-gray-400 truncate block" title={event.detail}>
                  {#each detailSegments as seg, i (`${seg.type}:${seg.text}:${i}`)}
                    {#if seg.type === "link"}
                      <a href={seg.href} target={seg.external ? "_blank" : undefined} rel={seg.external ? "noopener" : undefined} class="text-blue-600 dark:text-blue-400 hover:underline">{seg.text}</a>
                    {:else}
                      {seg.text}
                    {/if}
                  {/each}
                </span>
                {#if (event.detail?.length ?? 0) > 60}
                  <button class="text-blue-600 dark:text-blue-400 cursor-pointer text-xs bg-transparent border-0 p-0" onclick={() => toggleDetail(event.id)}>Show more</button>
                {/if}
              {/if}
            </TableBodyCell>
          </TableBodyRow>
        {:else}
          <TableBodyRow>
            <TableBodyCell colspan={7}>
              <div class="text-center text-gray-500 dark:text-gray-400 py-8">No audit events found</div>
            </TableBodyCell>
          </TableBodyRow>
        {/each}
      </TableBody>
    </Table>
    </TableCard>

    <div class="flex items-center justify-between mt-4 text-sm text-gray-500 dark:text-gray-400">
      <span>Page {currentPage} of {totalPages} — {events.length} events</span>
      <PaginationNav
        {currentPage}
        {totalPages}
        visiblePages={5}
        onPageChange={(nextPage) => { pageNum = nextPage - 1; load(); }}
      />
    </div>
  {/if}
</div>
