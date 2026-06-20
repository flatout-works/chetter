<script lang="ts">
  import { onMount } from "svelte";
  import { createClient } from "@connectrpc/connect";
  import { AdminService } from "$gen/proto/api/v1/api_pb";
  import type { TokenInfo, TeamInfo, UserInfo } from "$gen/proto/api/v1/api_pb";
  import { getTransport } from "$lib/api/client";

  let tokens = $state<TokenInfo[]>([]);
  let teams = $state<TeamInfo[]>([]);
  let users = $state<UserInfo[]>([]);
  let loading = $state(true);
  let showTokenForm = $state(false);
  let newTeam = $state("");
  let newUser = $state("");
  let newTokenName = $state("");
  let createdToken = $state<string | null>(null);

  async function load() {
    try {
      const client = createClient(AdminService, getTransport());
      const [tokenResp, teamResp, userResp] = await Promise.all([
        client.listTokens({}),
        client.listTeams({}),
        client.listUsers({}),
      ]);
      tokens = tokenResp.tokens ?? [];
      teams = teamResp.teams ?? [];
      users = userResp.users ?? [];
    } catch (e) {
      console.error(e);
    } finally {
      loading = false;
    }
  }

  onMount(load);

  async function createToken() {
    try {
      const client = createClient(AdminService, getTransport());
      const resp = await client.createToken({
        teamName: newTeam,
        userName: newUser,
        tokenName: newTokenName,
      });
      createdToken = resp.token;
      showTokenForm = false;
      newTeam = "";
      newUser = "";
      newTokenName = "";
      await load();
    } catch (e) {
      console.error(e);
    }
  }

  async function deleteToken(name: string) {
    if (!confirm(`Delete token "${name}"?`)) return;
    try {
      const client = createClient(AdminService, getTransport());
      await client.deleteToken({ name });
      await load();
    } catch (e) {
      console.error(e);
    }
  }
</script>

<svelte:head>
  <title>Admin — Chetter</title>
</svelte:head>

<div class="p-6">
  <h1 class="text-2xl font-bold text-gray-900 dark:text-white mb-6">Admin</h1>

  {#if loading}
    <p class="text-gray-500 dark:text-gray-400">Loading…</p>
  {:else}
    <!-- Created token alert -->
    {#if createdToken}
      <div class="mb-6 p-4 bg-green-50 dark:bg-green-900/20 border border-green-200 dark:border-green-800 rounded-lg">
        <p class="text-sm font-medium text-green-800 dark:text-green-400 mb-2">Token created — copy it now (shown only once):</p>
        <div class="flex gap-2">
          <code class="flex-1 px-3 py-2 bg-white dark:bg-gray-800 rounded font-mono text-sm text-gray-900 dark:text-white break-all">{createdToken}</code>
          <button onclick={() => { navigator.clipboard.writeText(createdToken!); }} class="px-3 py-2 bg-blue-600 hover:bg-blue-700 text-white rounded text-sm">Copy</button>
        </div>
        <button onclick={() => createdToken = null} class="mt-2 text-xs text-gray-500 hover:text-gray-700 dark:hover:text-gray-300">Dismiss</button>
      </div>
    {/if}

    <div class="grid grid-cols-1 lg:grid-cols-2 gap-6">
      <!-- Tokens -->
      <div class="bg-white dark:bg-gray-800 rounded-lg shadow-sm border border-gray-200 dark:border-gray-700 overflow-hidden">
        <div class="px-4 py-3 border-b border-gray-200 dark:border-gray-700 flex items-center justify-between">
          <h2 class="font-semibold text-gray-900 dark:text-white">API Tokens</h2>
          <button onclick={() => showTokenForm = !showTokenForm} class="text-sm text-blue-600 dark:text-blue-400 hover:underline">
            {showTokenForm ? "Cancel" : "+ New"}
          </button>
        </div>
        {#if showTokenForm}
          <div class="p-4 border-b border-gray-200 dark:border-gray-700 space-y-2">
            <input bind:value={newTeam} placeholder="Team name" class="w-full px-3 py-1.5 text-sm border border-gray-300 dark:border-gray-600 rounded bg-white dark:bg-gray-700 text-gray-900 dark:text-white" />
            <input bind:value={newUser} placeholder="User name" class="w-full px-3 py-1.5 text-sm border border-gray-300 dark:border-gray-600 rounded bg-white dark:bg-gray-700 text-gray-900 dark:text-white" />
            <input bind:value={newTokenName} placeholder="Token name" class="w-full px-3 py-1.5 text-sm border border-gray-300 dark:border-gray-600 rounded bg-white dark:bg-gray-700 text-gray-900 dark:text-white" />
            <button onclick={createToken} class="w-full px-3 py-1.5 text-sm bg-blue-600 hover:bg-blue-700 text-white rounded">Create Token</button>
          </div>
        {/if}
        <div class="divide-y divide-gray-200 dark:divide-gray-700">
          {#each tokens as token (token.name)}
            <div class="px-4 py-3 flex items-center justify-between">
              <div>
                <p class="text-sm font-medium text-gray-900 dark:text-white">{token.name}</p>
                <p class="text-xs text-gray-500 dark:text-gray-400">{token.userName} · {token.teamName}</p>
              </div>
              <button onclick={() => deleteToken(token.name)} class="text-xs text-red-600 dark:text-red-400 hover:underline">Delete</button>
            </div>
          {:else}
            <p class="px-4 py-4 text-center text-sm text-gray-500 dark:text-gray-400">No tokens</p>
          {/each}
        </div>
      </div>

      <!-- Teams -->
      <div class="bg-white dark:bg-gray-800 rounded-lg shadow-sm border border-gray-200 dark:border-gray-700 overflow-hidden">
        <div class="px-4 py-3 border-b border-gray-200 dark:border-gray-700">
          <h2 class="font-semibold text-gray-900 dark:text-white">Teams</h2>
        </div>
        <div class="divide-y divide-gray-200 dark:divide-gray-700">
          {#each teams as team (team.id)}
            <div class="px-4 py-3">
              <p class="text-sm font-medium text-gray-900 dark:text-white">{team.name}</p>
              <p class="text-xs text-gray-500 dark:text-gray-400">{team.id}</p>
            </div>
          {:else}
            <p class="px-4 py-4 text-center text-sm text-gray-500 dark:text-gray-400">No teams</p>
          {/each}
        </div>
      </div>
    </div>
  {/if}
</div>
