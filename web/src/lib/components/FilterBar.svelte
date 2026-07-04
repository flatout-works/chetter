<script lang="ts">
  import { createClient } from "@connectrpc/connect";
  import { TaskService } from "$gen/proto/api/v1/api_pb";
  import { getTransport } from "$lib/api/client";
  import { teamFilter, setTeamOptions, toggleTeam, selectAllTeams, deselectAllTeams, setRepoOptions, repoFilter, addRepo, removeRepo, hasRepoFilter, selectedRepos } from "$lib/stores/filter.svelte";
  import { Badge, Button, Input } from "flowbite-svelte";

  let { teams }: { teams: { id: string; name: string }[] } = $props();

  let teamOptions = $derived($teamFilter);
  let activeRepos = $derived($repoFilter);
  let repoInput = $state("");

  $effect(() => {
    if (teams.length > 0 && teamOptions.length === 0) {
      setTeamOptions(teams.map((t) => ({ id: t.id, name: t.name, selected: true })));
    }
  });

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

{#if teams.length > 0 || activeRepos.length > 0}
  <div class="flex flex-wrap items-center gap-2 px-6 py-2 bg-gray-50 dark:bg-gray-900 border-b border-gray-200 dark:border-gray-700 text-sm">
    {#if teams.length > 0}
      <span class="text-xs font-medium text-gray-500 dark:text-gray-400 shrink-0">Teams:</span>
      {#each teamOptions as opt (opt.id)}
        <button
          onclick={() => toggleTeam(opt.name)}
          class="px-2.5 py-0.5 rounded-full text-xs font-medium border transition-colors cursor-pointer"
          class:bg-blue-100:text-blue-800:border-blue-300={opt.selected}
          class:dark:bg-blue-900:dark:text-blue-200:dark:border-blue-700={opt.selected}
          class:bg-gray-100:text-gray-500:border-gray-200={!opt.selected}
          class:dark:bg-gray-800:dark:text-gray-400:dark:border-gray-600={!opt.selected}
        >
          {opt.name}
        </button>
      {/each}
    {/if}

    {#if activeRepos.length > 0 || teams.length > 0}
      <span class="text-xs font-medium text-gray-500 dark:text-gray-400 shrink-0 ml-2">Repos:</span>
    {/if}

    {#each activeRepos as repo (repo)}
      <Badge color="blue" class="!cursor-pointer" onclick={() => removeRepo(repo)}>
        {repo} &times;
      </Badge>
    {/each}

    <form onsubmit={(e) => { e.preventDefault(); addRepoFromInput(); }}
          class="inline-flex items-center gap-1 ml-1">
      <Input
        bind:value={repoInput}
        placeholder="org/repo"
        size="sm"
        class="!w-28 !h-7 !text-xs"
        onkeydown={(e) => { if (e.key === "Enter") addRepoFromInput(); }}
      />
      <Button color="alternative" size="xs" class="!h-7 !px-2 !text-xs" onclick={addRepoFromInput}>+</Button>
    </form>

    {#if anyFilterActive()}
      <button
        onclick={clearAll}
        class="ml-auto text-xs text-blue-600 dark:text-blue-400 hover:underline shrink-0 cursor-pointer"
      >
        Clear filters
      </button>
    {/if}
  </div>
{/if}
