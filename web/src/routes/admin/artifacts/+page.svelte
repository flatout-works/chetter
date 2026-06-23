<script lang="ts">
  import { onMount } from "svelte";
  import { resolve } from "$app/paths";
  import { createClient } from "@connectrpc/connect";
  import { AdminService } from "$gen/proto/api/v1/api_pb";
  import type { TaskArtifact } from "$gen/proto/api/v1/api_pb";
  import { getTransport } from "$lib/api/client";
  import { formatTime } from "$lib/utils.svelte";
  import StatusBadge from "$lib/components/StatusBadge.svelte";
  import TableCard from "$lib/components/TableCard.svelte";
  import { Alert, Button, Input, PaginationNav, Select, Spinner, Table, TableHead, TableHeadCell, TableBody, TableBodyRow, TableBodyCell } from "flowbite-svelte";

  type SortColumn = "type" | "artifact" | "task" | "ref" | "discovered";
  let artifacts = $state<TaskArtifact[]>([]);
  let loading = $state(true);
  let error = $state<string | null>(null);
  let taskId = $state("");
  let artifactType = $state("");
  let repo = $state("");
  let limit = $state(100);
  let offset = $state(0);
  let sortColumn = $state<SortColumn>("discovered");
  let sortDirection = $state<"asc" | "desc">("desc");

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

  let currentPage = $derived(Math.floor(offset / limit) + 1);
  let totalPages = $derived(currentPage + (artifacts.length >= limit ? 1 : 0));

  function toggleSort(col: SortColumn) {
    if (sortColumn === col) { sortDirection = sortDirection === "asc" ? "desc" : "asc"; }
    else { sortColumn = col; sortDirection = col === "discovered" ? "desc" : "asc"; }
  }

  function sortIcon(col: SortColumn): string {
    if (sortColumn !== col) return "↕";
    return sortDirection === "asc" ? "↑" : "↓";
  }

  async function load() {
    loading = true; error = null;
    try {
      const client = createClient(AdminService, getTransport());
      const resp = await client.listTaskArtifacts({
        taskId: taskId.trim(), artifactType, repo: repo.trim(), limit, offset,
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
  <div class="flex items-center justify-between mb-6">
    <h1 class="text-2xl font-bold text-gray-900 dark:text-white">Task Artifacts</h1>
    <div class="flex items-center gap-3">
      <Input bind:value={taskId} placeholder="Task ID" class="w-40" />
      <Input bind:value={repo} placeholder="Repository" class="w-44" />
      <Select bind:value={artifactType} class="min-w-44">
        <option value="">All artifact types</option>
        <option value="issue">Issue</option>
        <option value="pull_request">Pull Request</option>
        <option value="issue_comment">Issue Comment</option>
        <option value="pr_review">PR Review</option>
      </Select>
      <Input type="number" bind:value={limit} min="1" max="500" class="w-20" placeholder="Limit" />
      <Button color="blue" size="sm" onclick={() => { offset = 0; load(); }}>Search</Button>
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
      <span>Showing {artifacts.length > 0 ? offset + 1 : 0}–{offset + artifacts.length} of {artifacts.length < limit ? offset + artifacts.length : `${offset + artifacts.length}+`}</span>
      <PaginationNav
        {currentPage}
        {totalPages}
        visiblePages={5}
        onPageChange={(nextPage) => { offset = (nextPage - 1) * limit; load(); }}
      />
    </div>
  {/if}
</div>
