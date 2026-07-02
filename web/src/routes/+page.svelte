<script lang="ts">
  import { goto } from "$app/navigation";
  import { resolve } from "$app/paths";
  import { onMount } from "svelte";
  import { get } from "svelte/store";
  import { tasks, fleetHealth, statusFilter } from "$lib/stores/tasks.svelte";
  import { formatTime, formatAge, formatDuration } from "$lib/utils.svelte";
  import StatusBadge from "$lib/components/StatusBadge.svelte";
  import TableCard from "$lib/components/TableCard.svelte";
  import { Button, Card, Table, TableHead, TableHeadCell, TableBody, TableBodyRow, TableBodyCell } from "flowbite-svelte";

  let health = $derived($fleetHealth);
  let allTasks = $derived($tasks);

  let activeFilter = $state("");

  onMount(() => {
    activeFilter = get(statusFilter);
  });

  let filteredTasks = $derived.by(() => {
    let list = allTasks;
    if (activeFilter) {
      list = list.filter((t) => t.status === activeFilter);
    }
    return [...list].sort((a, b) => b.createdAt.localeCompare(a.createdAt));
  });

  const cards = $derived([
    { label: "Running", value: health.runningTasks, color: "text-green-600 dark:text-green-400", filter: "running" },
    { label: "Pending", value: health.pendingTasks, color: "text-yellow-600 dark:text-yellow-400", filter: "pending" },
    { label: "Done", value: health.doneTasks, color: "text-blue-600 dark:text-blue-400", filter: "done" },
    { label: "Error", value: health.errorTasks, color: "text-red-600 dark:text-red-400", filter: "error" },
    { label: "Stale", value: health.staleTasks, color: "text-orange-600 dark:text-orange-400", filter: "stale" },
    { label: "Runners", value: health.runnerCount, color: health.fleetActive ? "text-green-600 dark:text-green-400" : "text-red-600 dark:text-red-400", filter: "", runners: true },
  ]);

  function handleCardClick(card: typeof cards[number]) {
    if (card.runners) { goto(resolve("/runners")); }
    else if (activeFilter === card.filter) { activeFilter = ""; statusFilter.set(""); }
    else { activeFilter = card.filter; statusFilter.set(card.filter); }
  }
</script>

<svelte:head>
  <title>Dashboard — Chetter</title>
</svelte:head>

<div class="p-6">
  <h1 class="text-2xl font-bold text-gray-900 dark:text-white mb-6">Dashboard</h1>

  <div class="grid grid-cols-2 md:grid-cols-3 lg:grid-cols-6 gap-4 mb-8">
    {#each cards as card (card.label)}
      <Card
        size="sm"
        shadow="sm"
        href="#"
        class="!p-4 text-left hover:shadow-md transition-shadow w-full {card.filter && activeFilter === card.filter ? 'ring-2 ring-blue-500 dark:ring-blue-400' : ''}"
        title={card.runners ? "View runner fleet" : `Filter by ${card.label.toLowerCase()} tasks`}
        onclick={(e) => { e.preventDefault(); handleCardClick(card); }}
      >
        <p class="text-sm text-gray-500 dark:text-gray-400 mb-1">{card.label}</p>
        <p class="text-2xl font-bold {card.color}">{card.value}</p>
      </Card>
    {/each}
  </div>

  {#if activeFilter}
    <p class="mb-4 text-sm text-gray-500 dark:text-gray-400">
      Showing <strong class="text-gray-700 dark:text-gray-200">{activeFilter}</strong> tasks.
      <Button color="blue" size="xs" onclick={() => { activeFilter = ""; statusFilter.set(""); }}>Clear filter</Button>
    </p>
  {/if}

  <TableCard title="Recent tasks" subtitle="Newest tasks first. Click a top panel to filter by status.">
  <Table hoverable={true} shadow={false}>
    <TableHead>
      <TableHeadCell>Task</TableHeadCell>
      <TableHeadCell>Status</TableHeadCell>
      <TableHeadCell>Agent</TableHeadCell>
      <TableHeadCell>Created</TableHeadCell>
      <TableHeadCell>Age</TableHeadCell>
      <TableHeadCell>Duration</TableHeadCell>
    </TableHead>
    <TableBody>
      {#each filteredTasks.slice(0, 15) as task (task.id)}
        <TableBodyRow>
          <TableBodyCell>
            <a href={resolve(`/tasks/${task.id}`)} class="block">
              <p class="font-medium text-gray-900 dark:text-white truncate max-w-xs">
                {task.prompt.slice(0, 60)}{task.prompt.length > 60 ? "…" : ""}
              </p>
              <p class="font-mono text-blue-600 dark:text-blue-400 text-xs mt-0.5">
                {task.id.slice(0, 24)}…
              </p>
            </a>
          </TableBodyCell>
          <TableBodyCell>
            <StatusBadge status={task.status} />
          </TableBodyCell>
          <TableBodyCell><span class="text-gray-700 dark:text-gray-300">{task.agent || "—"}</span></TableBodyCell>
          <TableBodyCell><span class="text-gray-500 dark:text-gray-400 whitespace-nowrap">{formatTime(task.createdAt)}</span></TableBodyCell>
          <TableBodyCell><span class="text-gray-500 dark:text-gray-400 font-mono">{formatAge(task.createdAt)}</span></TableBodyCell>
          <TableBodyCell><span class="text-gray-500 dark:text-gray-400 font-mono">{formatDuration(task.startedAt, task.endedAt)}</span></TableBodyCell>
        </TableBodyRow>
      {:else}
        <TableBodyRow>
          <TableBodyCell colspan={6}>
            <div class="text-center text-gray-500 dark:text-gray-400 py-8">No tasks found</div>
          </TableBodyCell>
        </TableBodyRow>
      {/each}
    </TableBody>
  </Table>
  </TableCard>
</div>
