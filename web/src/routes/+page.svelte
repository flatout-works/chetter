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

  const communityLinks = [
    {
      href: "https://github.com/flatout-works/chetter",
      label: "GitHub",
      action: "View on GitHub",
      description: "Star the repo or open an issue",
      color: "dark",
      icon: "M12 2C6.477 2 2 6.59 2 12.253c0 4.526 2.865 8.357 6.839 9.709.5.094.683-.221.683-.489 0-.242-.009-.883-.014-1.733-2.782.617-3.369-1.367-3.369-1.367-.455-1.181-1.11-1.496-1.11-1.496-.908-.635.069-.622.069-.622 1.004.072 1.532 1.052 1.532 1.052.892 1.56 2.341 1.11 2.91.849.091-.659.349-1.11.635-1.365-2.221-.258-4.555-1.133-4.555-5.042 0-1.114.39-2.024 1.029-2.737-.103-.258-.446-1.295.098-2.699 0 0 .84-.274 2.75 1.046A9.385 9.385 0 0112 6.994c.85.004 1.705.117 2.504.345 1.909-1.32 2.747-1.046 2.747-1.046.546 1.404.203 2.441.1 2.699.64.713 1.028 1.623 1.028 2.737 0 3.919-2.338 4.781-4.566 5.034.359.316.678.939.678 1.893 0 1.366-.012 2.469-.012 2.804 0 .271.18.588.688.488C21.139 20.607 24 16.777 24 12.253 24 6.59 19.523 2 14 2h-2z",
    },
    {
      href: "https://discord.gg/KkZxKwSTvF",
      label: "Discord",
      action: "Join Discord",
      description: "Join the self-hosting community",
      color: "purple",
      icon: "M20.317 4.369A19.791 19.791 0 0015.344 2.8a13.79 13.79 0 00-.637 1.318 18.27 18.27 0 00-5.414 0A13.79 13.79 0 008.656 2.8a19.736 19.736 0 00-4.977 1.572C.533 9.116-.32 13.741.106 18.301a19.9 19.9 0 006.103 3.101 14.72 14.72 0 001.307-2.147 12.94 12.94 0 01-2.06-.994c.173-.129.342-.263.505-.402a14.194 14.194 0 0012.078 0c.165.139.334.273.507.402-.66.393-1.35.727-2.064.996a14.63 14.63 0 001.307 2.145 19.862 19.862 0 006.105-3.101c.5-5.291-.854-9.874-3.577-13.932zM8.02 15.496c-1.188 0-2.164-1.116-2.164-2.489 0-1.372.957-2.489 2.164-2.489 1.214 0 2.183 1.127 2.164 2.489 0 1.373-.957 2.489-2.164 2.489zm7.96 0c-1.188 0-2.164-1.116-2.164-2.489 0-1.372.957-2.489 2.164-2.489 1.214 0 2.183 1.127 2.164 2.489 0 1.373-.95 2.489-2.164 2.489z",
    },
  ] as const;

  function handleCardClick(card: typeof cards[number]) {
    if (card.runners) { goto(resolve("/runners")); }
    else if (activeFilter === card.filter) { activeFilter = ""; statusFilter.set(""); }
    else { activeFilter = card.filter; statusFilter.set(card.filter); }
  }
</script>

<svelte:head>
  <title>Dashboard — Chetter</title>
</svelte:head>

<div class="space-y-6 p-4 sm:p-6 lg:p-8">
  <Card size="xl" class="!p-0 overflow-hidden border-slate-200/80 bg-white/90 shadow-xl shadow-slate-200/70 backdrop-blur dark:border-slate-800 dark:bg-slate-900/85 dark:shadow-slate-950/40">
    <div class="relative overflow-hidden p-6 sm:p-8 lg:p-10">
      <div class="absolute right-0 top-0 h-48 w-48 rounded-full bg-cyan-400/20 blur-3xl"></div>
      <div class="absolute bottom-0 right-24 h-40 w-40 rounded-full bg-indigo-500/20 blur-3xl"></div>

      <div class="relative grid gap-8 lg:grid-cols-[1fr_22rem] lg:items-center">
        <div>
          <p class="mb-3 text-sm font-semibold uppercase tracking-[0.24em] text-cyan-600 dark:text-cyan-400">Chetter Control Plane</p>
          <h1 class="max-w-3xl text-4xl font-black tracking-tight text-slate-950 dark:text-white sm:text-5xl">
            Agent fleet health, live work, and GitHub-native outcomes in one place.
          </h1>
          <p class="mt-4 max-w-2xl text-base leading-7 text-slate-600 dark:text-slate-300 sm:text-lg">
            Watch autonomous development tasks move through isolated runners, inspect recent sessions, and keep the community links close while operating your self-hosted fleet.
          </p>
          <div class="mt-6 flex flex-col gap-3 sm:flex-row">
            {#each communityLinks as link (link.href)}
              <Button href={link.href} target="_blank" rel="noopener noreferrer" color={link.color} size="lg" class="justify-center gap-2">
                <svg class="h-5 w-5" fill="currentColor" viewBox="0 0 24 24" aria-hidden="true">
                  <path d={link.icon} />
                </svg>
                {link.action}
              </Button>
            {/each}
          </div>
        </div>

        <div class="grid gap-3">
          {#each communityLinks as link (link.href)}
            <Card href={link.href} target="_blank" rel="noopener noreferrer" size="xl" class="!p-4 border-slate-200/80 bg-slate-50/90 transition hover:-translate-y-0.5 hover:border-cyan-300 hover:bg-white hover:shadow-lg dark:border-slate-700 dark:bg-slate-800/80 dark:hover:border-cyan-500 dark:hover:bg-slate-800">
              <div class="flex items-center gap-3">
                <div class="flex h-11 w-11 items-center justify-center rounded-xl bg-slate-950 text-white dark:bg-slate-700">
                  <svg class="h-5 w-5" fill="currentColor" viewBox="0 0 24 24" aria-hidden="true">
                    <path d={link.icon} />
                  </svg>
                </div>
                <div>
                  <p class="font-semibold text-slate-950 dark:text-white">{link.label}</p>
                  <p class="text-sm text-slate-500 dark:text-slate-400">{link.description}</p>
                </div>
              </div>
            </Card>
          {/each}
        </div>
      </div>
    </div>
  </Card>

  <div class="grid grid-cols-2 gap-4 md:grid-cols-3 lg:grid-cols-6">
    {#each cards as card (card.label)}
      <Card
        size="sm"
        shadow="sm"
        href="#"
        class="!p-4 w-full border-slate-200/80 bg-white/90 text-left shadow-sm shadow-slate-200/60 transition hover:-translate-y-0.5 hover:shadow-lg dark:border-slate-800 dark:bg-slate-900/85 dark:shadow-slate-950/30 {card.filter && activeFilter === card.filter ? 'ring-2 ring-blue-500 dark:ring-blue-400' : ''}"
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
