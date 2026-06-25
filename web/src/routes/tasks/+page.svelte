<script lang="ts">
  import { resolve } from "$app/paths";
  import { onMount } from "svelte";
  import { get } from "svelte/store";
  import { createClient } from "@connectrpc/connect";
  import { TaskService, CatalogService } from "$gen/proto/api/v1/api_pb";
  import type { CatalogProvider } from "$gen/proto/api/v1/api_pb";
  import { getTransport } from "$lib/api/client";
  import { refreshTasks, tasks, statusFilter } from "$lib/stores/tasks.svelte";
  import { formatDuration, formatTime, formatAge } from "$lib/utils.svelte";
  import StatusBadge from "$lib/components/StatusBadge.svelte";
  import TableCard from "$lib/components/TableCard.svelte";
  import { Alert, Button, Card, Dropdown, DropdownItem, Input, Label, PaginationNav, Select, Table, TableHead, TableHeadCell, TableBody, TableBodyRow, TableBodyCell, Textarea } from "flowbite-svelte";

  type SortColumn = "id" | "status" | "agent" | "model" | "prompt" | "created" | "duration";
  let selectedStatus = $state("");
  let search = $state("");
  let taskList = $derived($tasks);
  let filteredTasks = $derived.by(() => {
    if (!search.trim()) return taskList;
    const q = search.toLowerCase();
    return taskList.filter(t => 
      t.prompt?.toLowerCase().includes(q) ||
      t.id?.toLowerCase().includes(q) ||
      t.summary?.toLowerCase().includes(q)
    );
  });
  let showSubmitForm = $state(false);
  let submitting = $state(false);
  let formError = $state<string | null>(null);
  let prompt = $state("");
  let gitUrl = $state("");
  let gitRef = $state("");
  let agentImage = $state("");
  let agent = $state("");
  let providerId = $state("");
  let modelId = $state("");
  let harness = $state("");
  let sessionMode = $state("");
  let pauseReason = $state("");
  let ttlHours = $state(72);

  let providers = $state.raw<CatalogProvider[]>([]);
  let defaultProvider = $state("");
  let defaultModel = $state("");

  const harnessOptions = [
    { value: "", label: "Default" },
    { value: "opencode", label: "OpenCode" },
    { value: "claude-code", label: "Claude Code" },
    { value: "pi", label: "Pi" },
  ];

  let selectedProvider = $derived(providers.find((p) => p.id === providerId));

  let page = $state(0);
  let pageSize = $state(25);
  let sortColumn = $state<SortColumn>("created");
  let sortDirection = $state<"asc" | "desc">("desc");

  let sortedTasks = $derived.by(() => {
    const sorted = [...filteredTasks].sort((a, b) => {
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

  function applyFilter() {
    statusFilter.set(selectedStatus);
    refreshTasks(selectedStatus, 100);
    page = 0;
  }

  function applyTemplate(kind: "review" | "fix" | "docs") {
    const templates = {
      review: "Review the current branch for bugs, regressions, missing tests, and maintainability risks. Prioritize findings with file and line references.",
      fix: "Diagnose and fix the failing behavior. Keep the change minimal, run the relevant checks, and summarize the root cause.",
      docs: "Update the relevant documentation to match the current behavior. Keep examples concise and verify links or commands where possible.",
    } satisfies Record<string, string>;
    prompt = templates[kind];
    showSubmitForm = true;
  }

  async function loadCatalog() {
    try {
      const client = createClient(CatalogService, getTransport());
      const resp = await client.getModelCatalog({});
      providers = resp.providers ?? [];
      defaultProvider = resp.defaultProvider;
      defaultModel = resp.defaultModel;
      if (!providerId) providerId = defaultProvider;
      if (!modelId) modelId = defaultModel;
    } catch (e) {
      console.error("Failed to load model catalog:", e);
    }
  }

  onMount(() => {
    selectedStatus = get(statusFilter);
    if (selectedStatus) refreshTasks(selectedStatus, 100);
    loadCatalog();
  });

  function onProviderChange() {
    const p = providers.find((p) => p.id === providerId);
    if (p && p.models.length > 0) {
      modelId = p.models[0];
    } else {
      modelId = "";
    }
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
        agentImage: agentImage.trim(), agent: agent.trim(),
        providerId: providerId.trim(), modelId: modelId.trim(),
        harness: harness.trim(),
        sessionMode: sessionMode || "",
        pauseReason: sessionMode === "resumable" ? pauseReason.trim() || "" : "",
        ttlHours: sessionMode === "resumable" ? ttlHours : 0,
      });
      prompt = ""; gitUrl = ""; gitRef = ""; agentImage = ""; agent = "";
      providerId = defaultProvider; modelId = defaultModel; harness = "";
      sessionMode = ""; pauseReason = ""; ttlHours = 72;
      showSubmitForm = false;
      await refreshTasks(selectedStatus, 100);
    } catch (err) {
      formError = err instanceof Error ? err.message : "Failed to submit task.";
    } finally { submitting = false; }
  }
</script>

<svelte:head>
  <title>Tasks — Chetter</title>
</svelte:head>

<div class="p-6">
  <div class="flex flex-wrap items-center justify-between mb-6 gap-3">
    <h1 class="text-2xl font-bold text-gray-900 dark:text-white">Tasks</h1>
    <div class="flex flex-wrap items-center gap-2">
      <Select
        bind:value={selectedStatus}
        onchange={applyFilter}
        class="!w-auto"
      >
        <option value="">All statuses</option>
        <option value="running">Running</option>
        <option value="pending">Pending</option>
        <option value="done">Done</option>
        <option value="error">Error</option>
        <option value="cancelled">Cancelled</option>
      </Select>
      <Input bind:value={search} placeholder="Search…" class="!w-44" />
      <Select
        bind:value={pageSize}
        onchange={() => { page = 0; }}
        class="!w-auto"
      >
        <option value={10}>10 / page</option>
        <option value={25}>25 / page</option>
        <option value={50}>50 / page</option>
        <option value={100}>100 / page</option>
      </Select>
      <Button
        color="blue"
        onclick={() => { showSubmitForm = !showSubmitForm; formError = null; }}
      >
        {showSubmitForm ? "Cancel" : "Submit Task"}
      </Button>
      <Button id="task-template-menu" color="alternative">Templates</Button>
      <Dropdown triggeredBy="#task-template-menu">
        <DropdownItem onclick={() => applyTemplate("review")}>Code review</DropdownItem>
        <DropdownItem onclick={() => applyTemplate("fix")}>Bug fix</DropdownItem>
        <DropdownItem onclick={() => applyTemplate("docs")}>Docs update</DropdownItem>
      </Dropdown>
    </div>
  </div>

  {#if showSubmitForm}
    <Card class="mb-6 w-full !p-5" size="xl" shadow="sm">
      <form onsubmit={submitTask} class="space-y-4">
        <div>
          <Label for="task-prompt" class="mb-1">Prompt</Label>
          <Textarea id="task-prompt" bind:value={prompt} placeholder="Describe the task for the agent" rows={4} class="w-full" />
        </div>
        <div class="grid grid-cols-1 md:grid-cols-3 gap-4">
          <div>
            <Label for="task-provider" class="mb-1">Provider</Label>
            <Select id="task-provider" bind:value={providerId} onchange={onProviderChange}>
              {#each providers as p (p.id)}
                <option value={p.id}>{p.name || p.id}</option>
              {/each}
            </Select>
          </div>
          <div>
            <Label for="task-model" class="mb-1">Model</Label>
            <Select id="task-model" bind:value={modelId}>
              {#if selectedProvider}
                {#each selectedProvider.models as m (m)}
                  <option value={m}>{m}</option>
                {/each}
              {:else}
                <option value="">—</option>
              {/if}
            </Select>
          </div>
          <div>
            <Label for="task-harness" class="mb-1">Harness</Label>
            <Select id="task-harness" bind:value={harness}>
              {#each harnessOptions as opt (opt.value)}
                <option value={opt.value}>{opt.label}</option>
              {/each}
            </Select>
          </div>
        </div>
        <div class="grid grid-cols-1 md:grid-cols-3 gap-4">
          <div>
            <Label for="task-session-mode" class="mb-1">Session Mode</Label>
            <Select id="task-session-mode" bind:value={sessionMode}>
              <option value="">None (one-shot)</option>
              <option value="resumable">Resumable</option>
            </Select>
          </div>
          {#if sessionMode === "resumable"}
            <div>
              <Label for="task-pause-reason" class="mb-1">Pause Reason (optional)</Label>
              <Input id="task-pause-reason" bind:value={pauseReason} placeholder="e.g. awaiting review" />
            </div>
            <div>
              <Label for="task-ttl" class="mb-1">TTL (hours)</Label>
              <Input id="task-ttl" type="number" bind:value={ttlHours} min={1} max={720} />
            </div>
          {/if}
        </div>
        <div class="grid grid-cols-1 md:grid-cols-2 gap-4">
          <Input bind:value={gitUrl} placeholder="Git URL (optional)" />
          <Input bind:value={gitRef} placeholder="Git ref (optional)" />
          <Input bind:value={agentImage} placeholder="Agent image override (optional)" />
          <Input bind:value={agent} placeholder="Agent (optional)" />
        </div>
      {#if formError}
        <Alert color="red">{formError}</Alert>
      {/if}
      <Button type="submit" color="blue" disabled={submitting}>
        {submitting ? "Submitting…" : "Submit"}
      </Button>
      </form>
    </Card>
  {/if}

  <TableCard title="Tasks" subtitle="Sorted by creation time, newest first unless you choose another column.">
  <Table hoverable={true} shadow={false}>
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
            <StatusBadge status={task.status} />
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
  </TableCard>

  <div class="flex items-center justify-between mt-4 text-sm text-gray-500 dark:text-gray-400">
    <span>Showing {sortedTasks.length > 0 ? page * pageSize + 1 : 0}–{Math.min((page + 1) * pageSize, sortedTasks.length)} of {sortedTasks.length}</span>
    <PaginationNav
      currentPage={page + 1}
      {totalPages}
      visiblePages={5}
      onPageChange={(nextPage) => { page = nextPage - 1; }}
    />
  </div>
</div>
