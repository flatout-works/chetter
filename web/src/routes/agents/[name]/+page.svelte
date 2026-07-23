<script lang="ts">
  import { onMount } from "svelte";
  import { resolve } from "$app/paths";
  import { createClient } from "@connectrpc/connect";
  import { CatalogService, TaskService } from "$gen/proto/api/v1/api_pb";
  import type { AgentDefinition, Task } from "$gen/proto/api/v1/api_pb";
  import { getTransport } from "$lib/api/client";
  import { effectiveTeamIDs, effectiveRepos } from "$lib/stores/filter.svelte";
  import { formatDuration, formatTime } from "$lib/utils.svelte";
  import StatusBadge from "$lib/components/StatusBadge.svelte";
  import { Alert, Badge, Card, PaginationNav, Spinner, Table, TableBody, TableBodyCell, TableBodyRow, TableHead, TableHeadCell } from "flowbite-svelte";

  let { params } = $props();
  let agent = $state<AgentDefinition | null>(null);
  let tasks = $state.raw<Task[]>([]);
  let loading = $state(true);
  let error = $state<string | null>(null);
  let page = $state(0);
  const pageSize = 10;
  const recentTaskLimit = pageSize * 2;
  let totalPages = $derived(Math.max(1, Math.ceil(tasks.length / pageSize)));
  let pagedTasks = $derived(tasks.slice(page * pageSize, (page + 1) * pageSize));
  let completedTasks = $derived(tasks.filter((task) => task.status === "done").length);
  let failedTasks = $derived(tasks.filter((task) => task.status === "error").length);
  let totalTokens = $derived(tasks.reduce((total, task) => {
    const usage = task.tokenUsage;
    return total + (usage?.inputTokens ?? 0n) + (usage?.outputTokens ?? 0n) + (usage?.reasoningTokens ?? 0n);
  }, 0n));
  let totalCost = $derived(tasks.reduce((total, task) => total + (task.tokenUsage?.costCents ?? 0n), 0n));
  let definitionBody = $derived(agent ? agent.content.replace(/^---\s*\n[\s\S]*?\n---\s*\n?/, "") : "");

  function fmtTokens(value: bigint): string {
    const amount = Number(value);
    if (amount >= 1_000_000) return `${(amount / 1_000_000).toFixed(1)}M`;
    if (amount >= 1_000) return `${(amount / 1_000).toFixed(1)}K`;
    return amount.toString();
  }

  function fmtCost(cents: bigint): string {
    return `$${(Number(cents) / 100).toFixed(2)}`;
  }

  function taskTokens(task: Task): bigint {
    const usage = task.tokenUsage;
    return (usage?.inputTokens ?? 0n) + (usage?.outputTokens ?? 0n) + (usage?.reasoningTokens ?? 0n);
  }

  function sourceFileUrl(): string | null {
    if (!agent?.sourceRepoUrl || !agent.path) return null;
    const branch = agent.sourceBranch || "main";
    const base = agent.sourceRepoUrl.replace(/\.git$/, "").replace(/\/$/, "");
    return `${base}/blob/${branch}/${agent.path}`;
  }

  async function load() {
    loading = true;
    error = null;
    try {
      const filters = {
        ...(effectiveTeamIDs().length > 0 ? { teamIds: effectiveTeamIDs() } : {}),
        ...(effectiveRepos().length > 0 ? { repos: effectiveRepos() } : {}),
      };
      const catalogClient = createClient(CatalogService, getTransport());
      const taskClient = createClient(TaskService, getTransport());
      const [agentResponse, taskResponse] = await Promise.all([
        catalogClient.listAgentDefinitions({ ...filters, name: params.name }),
        taskClient.listTasks({ ...filters, agent: params.name, limit: recentTaskLimit }),
      ]);
      agent = (agentResponse.agents ?? []).find((item) => item.name === params.name) ?? null;
      tasks = taskResponse.tasks ?? [];
      if (!agent) error = "Agent definition not found.";
    } catch (e) {
      error = e instanceof Error ? e.message : "Failed to load agent.";
    } finally {
      loading = false;
    }
  }

  onMount(load);
</script>

<svelte:head>
  <title>{agent?.name ?? "Agent"} - Chetter</title>
</svelte:head>

<div class="p-6">
  <a href={resolve("/agents")} class="inline-flex items-center gap-1 mb-4 text-sm text-blue-600 dark:text-blue-400 hover:underline">&larr; Back to Agents</a>

  {#if loading}
    <div class="flex items-center gap-2 text-gray-500 dark:text-gray-400"><Spinner size="4" /> Loading agent...</div>
  {:else if error}
    <Alert color="red">{error}</Alert>
  {:else if agent}
    <div class="mb-6">
      <div class="flex flex-wrap items-center gap-3">
        <h1 class="text-2xl font-bold text-gray-900 dark:text-white">{agent.name}</h1>
        <Badge color={agent.scope === "global" ? "blue" : agent.scope === "team" ? "purple" : "green"}>{agent.scope}</Badge>
        {#if agent.mode}<Badge color="gray">{agent.mode}</Badge>{/if}
      </div>
      {#if agent.description}<p class="mt-2 text-gray-600 dark:text-gray-300">{agent.description}</p>{/if}
    </div>

    <div class="grid grid-cols-2 gap-3 mb-6 lg:grid-cols-5">
      <Card size="xl" shadow="sm" class="!p-4"><p class="text-xs text-gray-500 dark:text-gray-400">Recent tasks</p><p class="mt-1 text-2xl font-semibold text-gray-900 dark:text-white">{tasks.length}</p></Card>
      <Card size="xl" shadow="sm" class="!p-4"><p class="text-xs text-gray-500 dark:text-gray-400">Completed</p><p class="mt-1 text-2xl font-semibold text-green-600 dark:text-green-400">{completedTasks}</p></Card>
      <Card size="xl" shadow="sm" class="!p-4"><p class="text-xs text-gray-500 dark:text-gray-400">Failed</p><p class="mt-1 text-2xl font-semibold text-red-600 dark:text-red-400">{failedTasks}</p></Card>
      <Card size="xl" shadow="sm" class="!p-4"><p class="text-xs text-gray-500 dark:text-gray-400">Recent tokens</p><p class="mt-1 text-2xl font-mono font-semibold text-gray-900 dark:text-white">{fmtTokens(totalTokens)}</p></Card>
      <Card size="xl" shadow="sm" class="!p-4"><p class="text-xs text-gray-500 dark:text-gray-400">Recent cost</p><p class="mt-1 text-2xl font-mono font-semibold text-gray-900 dark:text-white">{fmtCost(totalCost)}</p></Card>
    </div>

    <Card size="xl" shadow="sm" class="w-full !p-4 mb-6">
      <div class="grid grid-cols-1 gap-x-6 gap-y-3 text-sm sm:grid-cols-2 lg:grid-cols-4">
        <div><span class="text-xs text-gray-400 dark:text-gray-500">Identity</span><p class="text-gray-900 dark:text-white">{agent.identity}</p></div>
        <div><span class="text-xs text-gray-400 dark:text-gray-500">Provider</span><p class="text-gray-900 dark:text-white">{agent.provider || "Default"}</p></div>
        <div><span class="text-xs text-gray-400 dark:text-gray-500">Model</span><p class="font-mono text-gray-900 dark:text-white">{agent.model || "Default"}</p></div>
        <div><span class="text-xs text-gray-400 dark:text-gray-500">Updated</span><p class="text-gray-900 dark:text-white">{formatTime(agent.updatedAt)}</p></div>
        <div>
          <span class="text-xs text-gray-400 dark:text-gray-500">Source</span>
          {#if sourceFileUrl()}
            <p><a href={sourceFileUrl()} target="_blank" rel="noopener noreferrer" class="font-mono text-xs text-blue-600 dark:text-blue-400 hover:underline">{agent.path}</a></p>
          {:else}
            <p class="font-mono text-xs text-gray-900 dark:text-white">{agent.path || agent.sourceId}</p>
          {/if}
        </div>
        <div><span class="text-xs text-gray-400 dark:text-gray-500">Commit</span><p class="font-mono text-xs text-gray-900 dark:text-white">{agent.sourceCommit.slice(0, 12)}</p></div>
        {#if agent.repo}<div><span class="text-xs text-gray-400 dark:text-gray-500">Repository scope</span><p class="text-gray-900 dark:text-white">{agent.repo}</p></div>{/if}
        {#if agent.mcpEndpoints.length > 0}<div><span class="text-xs text-gray-400 dark:text-gray-500">MCP endpoints</span><p class="text-gray-900 dark:text-white">{agent.mcpEndpoints.join(", ")}</p></div>{/if}
      </div>
    </Card>

    {#if definitionBody.trim()}
      <Card size="xl" shadow="sm" class="w-full !p-5 mb-6">
        <h2 class="mb-3 font-semibold text-gray-900 dark:text-white">Definition</h2>
        <pre class="overflow-x-auto whitespace-pre-wrap font-sans text-sm leading-6 text-gray-700 dark:text-gray-300">{definitionBody}</pre>
      </Card>
    {/if}

    <Card size="xl" shadow="sm" class="w-full !p-0">
      <div class="px-4 py-3 border-b border-gray-200 dark:border-gray-700"><h2 class="font-semibold text-gray-900 dark:text-white">Recent Work</h2></div>
      <Table hoverable={true} shadow={false}>
        <TableHead>
          <TableHeadCell>Task</TableHeadCell><TableHeadCell>Status</TableHeadCell><TableHeadCell>Summary</TableHeadCell><TableHeadCell>Model</TableHeadCell><TableHeadCell>Tokens</TableHeadCell><TableHeadCell>Cost</TableHeadCell><TableHeadCell>Duration</TableHeadCell><TableHeadCell>Created</TableHeadCell>
        </TableHead>
        <TableBody>
          {#each pagedTasks as task (task.id)}
            <TableBodyRow>
              <TableBodyCell><a href={resolve("/tasks/[id]", { id: task.id })} class="font-mono text-xs text-blue-600 dark:text-blue-400 hover:underline">{task.id.slice(0, 16)}...</a></TableBodyCell>
              <TableBodyCell><StatusBadge status={task.status} /></TableBodyCell>
              <TableBodyCell class="max-w-sm"><span class="block truncate text-sm text-gray-600 dark:text-gray-300">{task.summary || task.prompt}</span></TableBodyCell>
              <TableBodyCell><span class="font-mono text-xs text-gray-600 dark:text-gray-300">{task.modelId || "-"}</span></TableBodyCell>
              <TableBodyCell><span class="font-mono text-xs text-gray-900 dark:text-white">{fmtTokens(taskTokens(task))}</span></TableBodyCell>
              <TableBodyCell><span class="font-mono text-xs text-gray-900 dark:text-white">{fmtCost(task.tokenUsage?.costCents ?? 0n)}</span></TableBodyCell>
              <TableBodyCell><span class="font-mono text-xs text-gray-500 dark:text-gray-400">{formatDuration(task.startedAt, task.endedAt)}</span></TableBodyCell>
              <TableBodyCell><span class="whitespace-nowrap text-xs text-gray-500 dark:text-gray-400">{formatTime(task.createdAt)}</span></TableBodyCell>
            </TableBodyRow>
          {:else}
            <TableBodyRow><TableBodyCell colspan={8}><div class="py-8 text-center text-gray-500 dark:text-gray-400">No work recorded for this agent</div></TableBodyCell></TableBodyRow>
          {/each}
        </TableBody>
      </Table>
      {#if tasks.length > pageSize}
        <div class="flex items-center justify-between px-4 py-2 text-xs text-gray-500 border-t border-gray-200 dark:text-gray-400 dark:border-gray-700">
          <span>{page * pageSize + 1}-{Math.min((page + 1) * pageSize, tasks.length)} of {tasks.length}</span>
          <PaginationNav currentPage={page + 1} {totalPages} visiblePages={5} onPageChange={(nextPage) => { page = nextPage - 1; }} />
        </div>
      {/if}
    </Card>
  {/if}
</div>
