<script lang="ts">
  import { onMount } from "svelte";
  import { resolve } from "$app/paths";
  import { createClient } from "@connectrpc/connect";
  import { CatalogService } from "$gen/proto/api/v1/api_pb";
  import type { AgentDefinition } from "$gen/proto/api/v1/api_pb";
  import { getTransport } from "$lib/api/client";
  import { effectiveTeamIDs, effectiveRepos } from "$lib/stores/filter.svelte";
  import { formatTime } from "$lib/utils.svelte";
  import TableCard from "$lib/components/TableCard.svelte";
  import { Alert, Badge, PaginationNav, Select, Spinner, Table, TableBody, TableBodyCell, TableBodyRow, TableHead, TableHeadCell } from "flowbite-svelte";

  let agents = $state.raw<AgentDefinition[]>([]);
  let loading = $state(true);
  let error = $state<string | null>(null);
  let page = $state(0);
  let pageSize = $state(25);
  let totalPages = $derived(Math.max(1, Math.ceil(agents.length / pageSize)));
  let pagedAgents = $derived(agents.slice(page * pageSize, (page + 1) * pageSize));

  async function load() {
    loading = true;
    error = null;
    try {
      const client = createClient(CatalogService, getTransport());
      const response = await client.listAgentDefinitions({
        ...(effectiveTeamIDs().length > 0 ? { teamIds: effectiveTeamIDs() } : {}),
        ...(effectiveRepos().length > 0 ? { repos: effectiveRepos() } : {}),
      });
      agents = response.agents ?? [];
    } catch (e) {
      error = e instanceof Error ? e.message : "Failed to load agents.";
    } finally {
      loading = false;
    }
  }

  onMount(load);
</script>

<svelte:head>
  <title>Agents - Chetter</title>
</svelte:head>

<div class="p-6">
  <div class="flex flex-wrap items-center justify-between gap-3 mb-6">
    <div>
      <h1 class="text-2xl font-bold text-gray-900 dark:text-white">Agents</h1>
      <p class="mt-1 text-sm text-gray-500 dark:text-gray-400">Git-managed agent definitions available to Chetter tasks.</p>
    </div>
    <Select bind:value={pageSize} onchange={() => { page = 0; }} class="!w-auto">
      <option value={10}>10 / page</option>
      <option value={25}>25 / page</option>
      <option value={50}>50 / page</option>
      <option value={100}>100 / page</option>
    </Select>
  </div>

  {#if error}
    <Alert color="red" class="mb-4">{error}</Alert>
  {/if}

  {#if loading}
    <div class="flex items-center gap-2 text-gray-500 dark:text-gray-400"><Spinner size="4" /> Loading agents...</div>
  {:else}
    <TableCard title="Agent Definitions" subtitle="Select an agent to inspect its configuration, recent work, and token usage.">
      <Table hoverable={true} shadow={false}>
        <TableHead>
          <TableHeadCell>Name</TableHeadCell>
          <TableHeadCell>Description</TableHeadCell>
          <TableHeadCell>Model</TableHeadCell>
          <TableHeadCell>Identity</TableHeadCell>
          <TableHeadCell>Scope</TableHeadCell>
          <TableHeadCell>Updated</TableHeadCell>
        </TableHead>
        <TableBody>
          {#each pagedAgents as agent (agent.id)}
            <TableBodyRow>
              <TableBodyCell>
                <a href={resolve("/agents/[name]", { name: agent.name })} class="font-medium text-blue-600 dark:text-blue-400 hover:underline">{agent.name}</a>
              </TableBodyCell>
              <TableBodyCell class="max-w-md"><span class="block truncate text-gray-600 dark:text-gray-300">{agent.description || "-"}</span></TableBodyCell>
              <TableBodyCell><span class="font-mono text-xs text-gray-700 dark:text-gray-300">{agent.model || "default"}</span></TableBodyCell>
              <TableBodyCell><span class="text-gray-700 dark:text-gray-300">{agent.identity}</span></TableBodyCell>
              <TableBodyCell>
                <Badge color={agent.scope === "global" ? "blue" : agent.scope === "team" ? "purple" : "green"}>{agent.scope}</Badge>
                {#if agent.repo}<span class="ml-1 text-xs text-gray-500 dark:text-gray-400">{agent.repo}</span>{/if}
              </TableBodyCell>
              <TableBodyCell><span class="whitespace-nowrap text-sm text-gray-500 dark:text-gray-400">{formatTime(agent.updatedAt)}</span></TableBodyCell>
            </TableBodyRow>
          {:else}
            <TableBodyRow>
              <TableBodyCell colspan={6}><div class="py-8 text-center text-gray-500 dark:text-gray-400">No agent definitions found</div></TableBodyCell>
            </TableBodyRow>
          {/each}
        </TableBody>
      </Table>
    </TableCard>
    <div class="flex items-center justify-between mt-4 text-sm text-gray-500 dark:text-gray-400">
      <span>Showing {agents.length > 0 ? page * pageSize + 1 : 0}-{Math.min((page + 1) * pageSize, agents.length)} of {agents.length}</span>
      <PaginationNav currentPage={page + 1} {totalPages} visiblePages={5} onPageChange={(nextPage) => { page = nextPage - 1; }} />
    </div>
  {/if}
</div>
