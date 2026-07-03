<script lang="ts">
  import { onMount } from "svelte";
  import { resolve } from "$app/paths";
  import { page } from "$app/stores";
  import { goto } from "$app/navigation";
  import { createClient } from "@connectrpc/connect";
  import { AdminService } from "$gen/proto/api/v1/api_pb";
  import type { TaskArtifact } from "$gen/proto/api/v1/api_pb";
  import { getTransport } from "$lib/api/client";
  import { formatTime } from "$lib/utils.svelte";
  import StatusBadge from "$lib/components/StatusBadge.svelte";
  import TableCard from "$lib/components/TableCard.svelte";
  import { Alert, Button, Input, PaginationNav, Search, Select, Spinner, Table, TableHead, TableHeadCell, TableBody, TableBodyRow, TableBodyCell } from "flowbite-svelte";

  const initialUrl = new URL($page.url);
  const u = $derived($page.url);

  function initialParam(name: string): string {
    return initialUrl.searchParams.get(name) || "";
  }

  function initialNumberParam(name: string, fallback: number): number {
    return Number(initialUrl.searchParams.get(name)) || fallback;
  }

  function initialArtifactType(): string {
    const type = initialParam("type");
    return type === "pull_request" ? "pr" : type;
  }

  type SortColumn = "type" | "artifact" | "task" | "ref" | "discovered";
  let artifacts = $state<TaskArtifact[]>([]);
  let loading = $state(true);
  let error = $state<string | null>(null);
  let taskId = $state(initialParam("task"));
  let artifactType = $state(initialArtifactType());
  let repo = $state(initialParam("repo"));
  let pageNum = $state(initialNumberParam("page", 0));
  let pageSize = $state(initialNumberParam("size", 25));
  let search = $state(initialParam("q"));
  let offset = $derived(pageNum * pageSize);
  let sortColumn = $state<SortColumn>("discovered");
  let sortDirection = $state<"asc" | "desc">("desc");

  function syncURL() {
    const url = new URL(u);
    const s = (k: string, v: string, d: string = "") => v && v !== d ? url.searchParams.set(k, v) : url.searchParams.delete(k);
    s("task", taskId.trim());
    s("type", artifactType);
    s("repo", repo.trim());
    s("page", String(pageNum), "0");
    s("size", String(pageSize), "25");
    s("q", search.trim());
    if (url.href !== u.href) goto(`${resolve("/admin/artifacts")}${url.search}${url.hash}` as Parameters<typeof goto>[0], { replaceState: true, noScroll: true, keepFocus: true });
  }

  $effect(() => { taskId; artifactType; repo; pageNum; pageSize; search; syncURL(); });

  let sortedArtifacts = $derived.by(() => {
    return [...artifacts].sort((a, b) => {
      let cmp = 0;
      switch (sortColumn) {
        case "type": cmp = a.artifactType.localeCompare(b.artifactType); break;
        case "artifact": cmp = `${a.repo}#${a.number}`.localeCompare(`${b.repo}#${b.number}`); break;
        case "task": cmp = a.taskId.localeCompare(b.taskId); break;
        case "ref": cmp = (a.ref || a.sha || "").localeCompare(b.ref || b.sha || ""); break;
        case "discovered": cmp = a.discoveredAt.localeCompare(b.discoveredAt); break;
      }
      return sortDirection === "asc" ? cmp : -cmp;
    });
  });

  let currentPage = $derived(pageNum + 1);
  let totalPages = $derived(currentPage + (artifacts.length >= pageSize ? 1 : 0));

  function toggleSort(col: SortColumn) {
    if (sortColumn === col) { sortDirection = sortDirection === "asc" ? "desc" : "asc"; }
    else { sortColumn = col; sortDirection = col === "discovered" ? "desc" : "asc"; }
  }

  function sortIcon(col: SortColumn): string {
    if (sortColumn !== col) return "↕";
    return sortDirection === "asc" ? "↑" : "↓";
  }

  function reloadFromFirstPage() {
    pageNum = 0;
    load();
  }

  async function load() {
    loading = true; error = null;
    try {
      const client = createClient(AdminService, getTransport());
      const resp = await client.listTaskArtifacts({
        taskId: taskId.trim(), artifactType, repo: repo.trim(), search: search.trim(), limit: pageSize, offset,
      });
      artifacts = resp.artifacts ?? [];
    } catch (e) {
      error = e instanceof Error ? e.message : "Failed to load artifacts.";
      console.error(e);
    } finally { loading = false; }
  }

  onMount(load);
</script>

<svelte:head>
  <title>Artifacts — Chetter</title>
</svelte:head>

<div class="p-6">
  <div class="mb-4">
    <h1 class="text-2xl font-bold text-gray-900 dark:text-white">Task Artifacts</h1>
  </div>

  <div class="flex flex-wrap items-center justify-between mb-6 gap-3">
    <Search
      bind:value={search}
      placeholder="Search artifacts…"
      class="!w-72"
      size="md"
      onkeydown={(e) => { if (e.key === "Enter") { pageNum = 0; load(); } }}
    />
    <div class="flex flex-wrap items-center gap-2">
      <Input bind:value={taskId} placeholder="Task ID" class="!w-40" onkeydown={(e) => { if (e.key === "Enter") reloadFromFirstPage(); }} />
      <Input bind:value={repo} placeholder="Repository" class="!w-44" onkeydown={(e) => { if (e.key === "Enter") reloadFromFirstPage(); }} />
      <Select bind:value={artifactType} onchange={reloadFromFirstPage} class="!w-auto min-w-44">
        <option value="">All artifact types</option>
        <option value="issue">Issue</option>
        <option value="pr">Pull Request</option>
        <option value="issue_comment">Issue Comment</option>
        <option value="pr_review">PR Review</option>
      </Select>
      <Select bind:value={pageSize} onchange={() => { pageNum = 0; load(); }} class="!w-auto">
        <option value={10}>10 / page</option>
        <option value={25}>25 / page</option>
        <option value={50}>50 / page</option>
        <option value={100}>100 / page</option>
      </Select>
    </div>
  </div>

  {#if error}
    <Alert color="red" class="mb-4">{error}</Alert>
  {/if}

  {#if loading}
    <div class="flex items-center gap-2 text-gray-500 dark:text-gray-400"><Spinner size="4" /> Loading…</div>
  {:else}
    <TableCard title="Task artifacts" subtitle="GitHub issues, PRs, comments, and reviews created by Chetter tasks.">
    <Table hoverable={true} shadow={false}>
      <TableHead>
        <TableHeadCell onclick={() => toggleSort("type")} class="cursor-pointer select-none">Type {sortIcon("type")}</TableHeadCell>
        <TableHeadCell onclick={() => toggleSort("artifact")} class="cursor-pointer select-none">Artifact {sortIcon("artifact")}</TableHeadCell>
        <TableHeadCell onclick={() => toggleSort("task")} class="cursor-pointer select-none">Task {sortIcon("task")}</TableHeadCell>
        <TableHeadCell onclick={() => toggleSort("ref")} class="cursor-pointer select-none">Ref {sortIcon("ref")}</TableHeadCell>
        <TableHeadCell onclick={() => toggleSort("discovered")} class="cursor-pointer select-none">Discovered {sortIcon("discovered")}</TableHeadCell>
      </TableHead>
      <TableBody>
        {#each sortedArtifacts as artifact (artifact.id)}
          <TableBodyRow>
            <TableBodyCell><StatusBadge status={artifact.artifactType === "pr_review" ? "pr_review_artifact" : artifact.artifactType} label={artifact.artifactType} /></TableBodyCell>
            <TableBodyCell>
              {#if artifact.url}
                <Button color="alternative" size="xs" onclick={() => window.open(artifact.url, "_blank", "noopener,noreferrer")}>
                  {artifact.repo}#{artifact.number || "?"}
                </Button>
              {:else}
                <span class="text-gray-700 dark:text-gray-300">{artifact.repo}#{artifact.number || "?"}</span>
              {/if}
            </TableBodyCell>
            <TableBodyCell>
              <a href={resolve("/tasks/[id]", { id: artifact.taskId })} class="font-mono text-blue-600 dark:text-blue-400 hover:underline text-xs">
                {artifact.taskId.slice(0, 20)}…
              </a>
            </TableBodyCell>
            <TableBodyCell class="max-w-xs">
              <span class="text-gray-500 dark:text-gray-400 font-mono truncate block">{artifact.ref || artifact.sha || "—"}</span>
            </TableBodyCell>
            <TableBodyCell><span class="text-gray-500 dark:text-gray-400 whitespace-nowrap">{formatTime(artifact.discoveredAt)}</span></TableBodyCell>
          </TableBodyRow>
        {:else}
          <TableBodyRow>
            <TableBodyCell colspan={5}>
              <div class="text-center text-gray-500 dark:text-gray-400 py-8">No artifacts found</div>
            </TableBodyCell>
          </TableBodyRow>
        {/each}
      </TableBody>
    </Table>
    </TableCard>

    <div class="flex items-center justify-between mt-4 text-sm text-gray-500 dark:text-gray-400">
      <span>Page {currentPage} of {totalPages} — {artifacts.length} artifacts</span>
      <PaginationNav
        {currentPage}
        {totalPages}
        visiblePages={5}
        onPageChange={(nextPage) => { pageNum = nextPage - 1; load(); }}
      />
    </div>
  {/if}
</div>
