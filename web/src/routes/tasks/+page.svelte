<script lang="ts">
  import { resolve } from "$app/paths";
  import { createClient } from "@connectrpc/connect";
  import { TaskService } from "$gen/proto/api/v1/api_pb";
  import { getTransport } from "$lib/api/client";
  import { refreshTasks, tasks } from "$lib/stores/tasks.svelte";
  import { formatDuration, formatTime, formatAge } from "$lib/utils.svelte";
  import { Table, TableHead, TableHeadCell, TableBody, TableBodyRow, TableBodyCell, Badge, Button } from "flowbite-svelte";

  type SortColumn = "id" | "status" | "agent" | "model" | "prompt" | "created" | "duration";
  let statusFilter = $state("");
  let taskList = $derived($tasks);
  let showSubmitForm = $state(false);
  let submitting = $state(false);
  let formError = $state<string | null>(null);
  let prompt = $state("");
  let gitUrl = $state("");
  let gitRef = $state("");
  let agentImage = $state("");
  let agent = $state("");
  let modelId = $state("");

  let page = $state(0);
  let pageSize = $state(25);
  let sortColumn = $state<SortColumn>("created");
  let sortDirection = $state<"asc" | "desc">("desc");

  let sortedTasks = $derived.by(() => {
    const sorted = [...taskList].sort((a, b) => {
      let cmp = 0;
      switch (sortColumn) {
        case "id": cmp = a.id.localeCompare(b.id); break;
        case "status": cmp = a.status.localeCompare(b.status); break;
        case "agent": cmp = (a.agent || "").localeCompare(b.agent || ""); break;
        case "model": cmp = (a.modelId || "").localeCompare(b.modelId || ""); break;
        case "prompt": cmp = a.prompt.localeCompare(b.prompt); break;
        case "created": cmp = a.createdAt.localeCompare(b.createdAt); break;
        case "duration": cmp = ((a.startedAt || "") < (b.startedAt || "") ? -1 : 1); break;
      }
      return sortDirection === "asc" ? cmp : -cmp;
    });
    return sorted;
  });

  let totalPages = $derived(Math.max(1, Math.ceil(sortedTasks.length / pageSize)));
  let pagedTasks = $derived(sortedTasks.slice(page * pageSize, (page + 1) * pageSize));

  function toggleSort(col: SortColumn) {
    if (sortColumn === col) { sortDirection = sortDirection === "asc" ? "desc" : "asc"; }
    else { sortColumn = col; sortDirection = "desc"; }
    page = 0;
  }

  function sortIcon(col: SortColumn): string {
    if (sortColumn !== col) return "↕";
    return sortDirection === "asc" ? "↑" : "↓";
  }

  function statusColor(status: string): "green" | "red" | "yellow" | "blue" | "gray" {
    const map: Record<string, "green" | "red" | "yellow" | "blue" | "gray"> = {
      running: "green", pending: "yellow", done: "blue", error: "red", cancelled: "gray",
    };
    return map[status] ?? "gray";
  }

  function applyFilter() {
    refreshTasks(statusFilter, 100);
    page = 0;
  }

  async function submitTask(e: Event) {
    e.preventDefault();
    formError = null;
    if (!prompt.trim()) { formError = "Prompt is required."; return; }
    submitting = true;
    try {
      const client = createClient(TaskService, getTransport());
      await client.submitTask({
        prompt: prompt.trim(), gitUrl: gitUrl.trim(), gitRef: gitRef.trim(),
        agentImage: agentImage.trim(), agent: agent.trim(), modelId: modelId.trim(),
      });
      prompt = ""; gitUrl = ""; gitRef = ""; agentImage = ""; agent = ""; modelId = "";
      showSubmitForm = false;
      await refreshTasks(statusFilter, 100);
    } catch (err) {
      formError = err instanceof Error ? err.message : "Failed to submit task.";
    } finally { submitting = false; }
  }
</script>

<svelte:head>
  <title>Tasks — Chetter</title>
</svelte:head>

<div class="p-6">
  <div class="flex items-center justify-between mb-6">
    <h1 class="text-2xl font-bold text-gray-900 dark:text-white">Tasks</h1>
    <div class="flex items-center gap-3">
      <select
        bind:value={statusFilter}
        onchange={applyFilter}
        class="px-3 py-2 border border-gray-300 dark:border-gray-600 rounded-lg bg-white dark:bg-gray-700 text-gray-900 dark:text-white text-sm"
      >
        <option value="">All statuses</option>
        <option value="running">Running</option>
        <option value="pending">Pending</option>
        <option value="done">Done</option>
        <option value="error">Error</option>
        <option value="cancelled">Cancelled</option>
      </select>
      <select
        bind:value={pageSize}
        onchange={() => { page = 0; }}
        class="px-3 py-2 border border-gray-300 dark:border-gray-600 rounded-lg bg-white dark:bg-gray-700 text-gray-900 dark:text-white text-sm"
      >
        <option value={10}>10 / page</option>
        <option value={25}>25 / page</option>
        <option value={50}>50 / page</option>
        <option value={100}>100 / page</option>
      </select>
      <Button
        color="blue"
        onclick={() => { showSubmitForm = !showSubmitForm; formError = null; }}
      >
        {showSubmitForm ? "Cancel" : "Submit Task"}
      </Button>
    </div>
  </div>

  {#if showSubmitForm}
    <form onsubmit={submitTask} class="mb-6 bg-white dark:bg-gray-800 rounded-lg shadow-sm border border-gray-200 dark:border-gray-700 p-4 space-y-4">
      <div>
        <label for="task-prompt" class="block text-sm font-medium text-gray-700 dark:text-gray-300 mb-1">Prompt</label>
        <textarea id="task-prompt" bind:value={prompt} rows="4" placeholder="Describe the task for the agent"
          class="w-full px-3 py-2 border border-gray-300 dark:border-gray-600 rounded-lg bg-white dark:bg-gray-700 text-gray-900 dark:text-white text-sm"
        ></textarea>
      </div>
      <div class="grid grid-cols-1 md:grid-cols-2 gap-3">
        <input bind:value={gitUrl} placeholder="Git URL (optional)" class="px-3 py-2 border border-gray-300 dark:border-gray-600 rounded-lg bg-white dark:bg-gray-700 text-gray-900 dark:text-white text-sm" />
        <input bind:value={gitRef} placeholder="Git ref (optional)" class="px-3 py-2 border border-gray-300 dark:border-gray-600 rounded-lg bg-white dark:bg-gray-700 text-gray-900 dark:text-white text-sm" />
        <input bind:value={agentImage} placeholder="Agent image override (optional)" class="px-3 py-2 border border-gray-300 dark:border-gray-600 rounded-lg bg-white dark:bg-gray-700 text-gray-900 dark:text-white text-sm" />
        <input bind:value={agent} placeholder="Agent (optional)" class="px-3 py-2 border border-gray-300 dark:border-gray-600 rounded-lg bg-white dark:bg-gray-700 text-gray-900 dark:text-white text-sm" />
        <input bind:value={modelId} placeholder="Model ID (optional)" class="px-3 py-2 border border-gray-300 dark:border-gray-600 rounded-lg bg-white dark:bg-gray-700 text-gray-900 dark:text-white text-sm" />
      </div>
      {#if formError}
        <p class="text-sm text-red-600 dark:text-red-400">{formError}</p>
      {/if}
      <Button type="submit" color="blue" disabled={submitting}>
        {submitting ? "Submitting…" : "Submit"}
      </Button>
    </form>
  {/if}

  <Table hoverable shadow>
    <TableHead>
      <TableHeadCell onclick={() => toggleSort("id")} class="cursor-pointer select-none">
        Task ID {sortIcon("id")}
      </TableHeadCell>
      <TableHeadCell onclick={() => toggleSort("status")} class="cursor-pointer select-none">
        Status {sortIcon("status")}
      </TableHeadCell>
      <TableHeadCell onclick={() => toggleSort("agent")} class="cursor-pointer select-none">
        Agent {sortIcon("agent")}
      </TableHeadCell>
      <TableHeadCell onclick={() => toggleSort("model")} class="cursor-pointer select-none">
        Model {sortIcon("model")}
      </TableHeadCell>
      <TableHeadCell onclick={() => toggleSort("prompt")} class="cursor-pointer select-none">
        Prompt {sortIcon("prompt")}
      </TableHeadCell>
      <TableHeadCell onclick={() => toggleSort("created")} class="cursor-pointer select-none">
        Created {sortIcon("created")}
      </TableHeadCell>
      <TableHeadCell onclick={() => toggleSort("created")} class="cursor-pointer select-none">
        Age {sortIcon("created")}
      </TableHeadCell>
      <TableHeadCell onclick={() => toggleSort("duration")} class="cursor-pointer select-none">
        Duration {sortIcon("duration")}
      </TableHeadCell>
    </TableHead>
    <TableBody>
      {#each pagedTasks as task (task.id)}
        <TableBodyRow>
          <TableBodyCell>
            <a href={resolve("/tasks/[id]", { id: task.id })} class="font-mono text-blue-600 dark:text-blue-400 hover:underline text-xs">
              {task.id.slice(0, 20)}…
            </a>
          </TableBodyCell>
          <TableBodyCell>
            <Badge color={statusColor(task.status)}>{task.status}</Badge>
          </TableBodyCell>
          <TableBodyCell><span class="text-gray-700 dark:text-gray-300">{task.agent || "—"}</span></TableBodyCell>
          <TableBodyCell><span class="text-gray-700 dark:text-gray-300">{task.modelId || "—"}</span></TableBodyCell>
          <TableBodyCell class="max-w-md">
            <span class="text-gray-500 dark:text-gray-400 truncate block">
              {task.prompt.slice(0, 60)}{task.prompt.length > 60 ? "…" : ""}
            </span>
          </TableBodyCell>
          <TableBodyCell><span class="text-gray-500 dark:text-gray-400 whitespace-nowrap">{formatTime(task.createdAt)}</span></TableBodyCell>
          <TableBodyCell><span class="text-gray-500 dark:text-gray-400 font-mono">{formatAge(task.createdAt)}</span></TableBodyCell>
          <TableBodyCell><span class="text-gray-500 dark:text-gray-400 font-mono">{formatDuration(task.startedAt, task.endedAt)}</span></TableBodyCell>
        </TableBodyRow>
      {:else}
        <TableBodyRow>
          <TableBodyCell colspan={8}>
            <div class="text-center text-gray-500 dark:text-gray-400 py-8">No tasks found</div>
          </TableBodyCell>
        </TableBodyRow>
      {/each}
    </TableBody>
  </Table>

  <!-- Pagination -->
  <div class="flex items-center justify-between mt-4 text-sm text-gray-500 dark:text-gray-400">
    <span>Showing {sortedTasks.length > 0 ? page * pageSize + 1 : 0}–{Math.min((page + 1) * pageSize, sortedTasks.length)} of {sortedTasks.length}</span>
    <div class="flex gap-2">
      <Button size="xs" color="alternative" disabled={page === 0} onclick={() => { page = Math.max(0, page - 1); }}>← Prev</Button>
      {#each { length: totalPages } as _, i}
        <Button
          size="xs"
          color={i === page ? "blue" : "alternative"}
          onclick={() => { page = i; }}
        >{i + 1}</Button>
      {/each}
      <Button size="xs" color="alternative" disabled={page >= totalPages - 1} onclick={() => { page = Math.min(totalPages - 1, page + 1); }}>Next →</Button>
    </div>
  </div>
</div>
