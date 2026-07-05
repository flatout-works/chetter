<script lang="ts">
  import { teamFilter, setTeamOptions, toggleTeam, selectAllTeams, setRepoOptions, repoFilter, addRepo, removeRepo } from "$lib/stores/filter.svelte";
  import { Badge, Button, Input, Select } from "flowbite-svelte";

  let { teams, repos = [] }: { teams: { id: string; name: string }[]; repos?: string[] } = $props();

  let teamOptions = $derived($teamFilter);
  let activeRepos = $derived($repoFilter);
  let repoInput = $state("");
  let repoSelect = $state("");

  $effect(() => {
    if (teams.length > 0 && teamOptions.length === 0) {
      setTeamOptions(teams.map((t) => ({ id: t.id, name: t.name, selected: true })));
    }
  });

  let unusedRepos = $derived(repos.filter((r) => !activeRepos.includes(r)));

  function addRepoFromSelect() {
    const trimmed = repoSelect.trim();
    if (trimmed) {
      addRepo(trimmed);
      repoSelect = "";
    }
  }

  function addRepoFromInput() {
    const trimmed = repoInput.trim();
    if (trimmed) {
      addRepo(trimmed);
      repoInput = "";
    }
  }

  function anyFilterActive(): boolean {
    return teamOptions.some((t) => !t.selected) || activeRepos.length > 0;
  }

  function clearAll() {
    selectAllTeams();
    setRepoOptions([]);
  }
</script>

<section class="border-t border-gray-200 dark:border-gray-700 px-3 py-4 space-y-4">
  <div class="flex items-center justify-between gap-2">
    <h2 class="text-xs font-semibold uppercase tracking-wide text-gray-500 dark:text-gray-400">Global filters</h2>
    {#if anyFilterActive()}
      <Button color="alternative" size="xs" class="!px-2 !py-1 !text-xs" onclick={clearAll}>Clear</Button>
    {/if}
  </div>

  <div class="space-y-2">
    {#if teams.length > 0}
      <div class="flex items-center justify-between gap-2">
        <span class="text-xs font-medium text-gray-500 dark:text-gray-400">Teams</span>
        <Button color="alternative" size="xs" class="!px-2 !py-1 !text-xs" onclick={selectAllTeams}>All</Button>
      </div>
      <div class="flex flex-wrap gap-1.5">
      {#each teamOptions as opt (opt.id)}
        <Button
          onclick={() => toggleTeam(opt.name)}
          color={opt.selected ? "blue" : "alternative"}
          size="xs"
          class="!rounded-full !px-2.5 !py-1 !text-xs"
        >
          {opt.name}
        </Button>
      {/each}
      </div>
    {:else}
      <p class="text-xs font-medium text-gray-500 dark:text-gray-400">Teams <span class="text-gray-400 dark:text-gray-500 font-normal">all (admin)</span></p>
    {/if}
  </div>

  <div class="space-y-2">
    <span class="text-xs font-medium text-gray-500 dark:text-gray-400">Repos</span>

    {#if activeRepos.length > 0}
      <div class="flex flex-wrap gap-1.5">
    {#each activeRepos as repo (repo)}
      <Badge color="blue" class="!cursor-pointer" onclick={() => removeRepo(repo)}>
        {repo} &times;
      </Badge>
    {/each}
      </div>
    {/if}

    {#if unusedRepos.length > 0}
      <Select bind:value={repoSelect} onchange={addRepoFromSelect} size="sm" class="!h-8 !text-xs">
        <option value="">Add repo…</option>
        {#each unusedRepos as repo (repo)}
          <option value={repo}>{repo}</option>
        {/each}
      </Select>
    {/if}

    <form onsubmit={(e) => { e.preventDefault(); addRepoFromInput(); }}
          class="flex items-center gap-1">
      <Input
        bind:value={repoInput}
        placeholder="Type org/repo…"
        size="sm"
        class="!h-8 !text-xs"
        onkeydown={(e) => { if (e.key === "Enter") addRepoFromInput(); }}
      />
      <Button color="alternative" size="xs" class="!h-8 !px-2 !text-xs" onclick={addRepoFromInput}>+</Button>
    </form>
  </div>
</section>
