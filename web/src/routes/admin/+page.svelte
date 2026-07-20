<script lang="ts">
  import { onMount } from "svelte";
  import { createClient } from "@connectrpc/connect";
  import { AdminService } from "$gen/proto/api/v1/api_pb";
  import type { GitIdentity, TokenInfo, TeamInfo, UserInfo } from "$gen/proto/api/v1/api_pb";
  import { getTransport } from "$lib/api/client";
  import { addToast } from "$lib/stores/toast.svelte";
  import { confirm } from "$lib/stores/confirm.svelte";
  import { Alert, Badge, Button, Card, Input, Spinner } from "flowbite-svelte";

  let tokens = $state<TokenInfo[]>([]);
  let teams = $state<TeamInfo[]>([]);
  let users = $state<UserInfo[]>([]);
  let identities = $state<GitIdentity[]>([]);
  let loading = $state(true);
  let showTokenForm = $state(false);
  let showTeamForm = $state(false);
  let newTeam = $state("");
  let newUser = $state("");
  let newTokenName = $state("");
  let createdToken = $state<string | null>(null);
  let newTeamName = $state("");
  let showIdentityForm = $state(false);
  let newIdentityTeam = $state("");
  let newIdentityName = $state("");
  let newGitAuthorName = $state("");
  let newGitAuthorEmail = $state("");
  let actionError = $state<string | null>(null);

  async function load() {
    try {
      const client = createClient(AdminService, getTransport());
      const [tokenResp, teamResp, userResp, identityResp] = await Promise.all([
        client.listTokens({}),
        client.listTeams({}),
        client.listUsers({}),
        client.listGitIdentities({}),
      ]);
      tokens = tokenResp.tokens ?? [];
      teams = teamResp.teams ?? [];
      users = userResp.users ?? [];
      identities = identityResp.identities ?? [];
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
      const teamNames = newTeam.split(",").map((t) => t.trim()).filter(Boolean);
      const resp = await client.createToken({
        teamNames,
        userName: newUser,
        tokenName: newTokenName,
      });
      createdToken = resp.token;
      addToast("Token created successfully", "success");
      showTokenForm = false;
      newTeam = "";
      newUser = "";
      newTokenName = "";
      await load();
    } catch (e) {
      actionError = e instanceof Error ? e.message : "Failed to create token.";
      addToast(actionError, "error");
      console.error(e);
    }
  }

  async function deleteToken(name: string) {
    const ok = await confirm({
      title: "Delete Token",
      message: `Delete token "${name}"?`,
      confirmLabel: "Delete",
    });
    if (!ok) return;
    actionError = null;
    try {
      const client = createClient(AdminService, getTransport());
      await client.deleteToken({ name });
      addToast(`Token "${name}" deleted`, "success");
      await load();
    } catch (e) {
      actionError = e instanceof Error ? e.message : "Failed to delete token.";
      addToast(actionError, "error");
      console.error(e);
    }
  }

  async function createTeamAction() {
    if (!newTeamName.trim()) return;
    actionError = null;
    try {
      const client = createClient(AdminService, getTransport());
      await client.createTeam({ name: newTeamName.trim() });
      addToast(`Team "${newTeamName.trim()}" created`, "success");
      newTeamName = "";
      showTeamForm = false;
      await load();
    } catch (e) {
      actionError = e instanceof Error ? e.message : "Failed to create team.";
      console.error(e);
    }
  }

  async function deleteTeamAction(name: string) {
    const ok = await confirm({
      title: "Delete Team",
      message: `Delete team "${name}" and all users/tokens/tasks? This cannot be undone.`,
      confirmLabel: "Delete Team",
    });
    if (!ok) return;
    actionError = null;
    try {
      const client = createClient(AdminService, getTransport());
      await client.deleteTeam({ name });
      addToast(`Team "${name}" deleted`, "success");
      await load();
    } catch (e) {
      actionError = e instanceof Error ? e.message : "Failed to delete team.";
      addToast(actionError, "error");
      console.error(e);
    }
  }

  async function createIdentity() {
    actionError = null;
    try {
      const client = createClient(AdminService, getTransport());
      await client.createGitIdentity({
        teamName: newIdentityTeam.trim(),
        name: newIdentityName.trim(),
        gitAuthorName: newGitAuthorName.trim(),
        gitAuthorEmail: newGitAuthorEmail.trim(),
        credentialType: "github_app",
      });
      addToast(`Git identity "${newIdentityName.trim()}" created`, "success");
      showIdentityForm = false;
      newIdentityTeam = "";
      newIdentityName = "";
      newGitAuthorName = "";
      newGitAuthorEmail = "";
      await load();
    } catch (e) {
      actionError = e instanceof Error ? e.message : "Failed to create Git identity.";
      addToast(actionError, "error");
    }
  }

  async function deleteIdentity(identity: GitIdentity) {
    const ok = await confirm({
      title: "Delete Git Identity",
      message: `Delete Git identity "${identity.name}"? Identities referenced by tasks cannot be deleted.`,
      confirmLabel: "Delete",
    });
    if (!ok) return;
    actionError = null;
    try {
      const client = createClient(AdminService, getTransport());
      await client.deleteGitIdentity({ teamId: identity.teamId, name: identity.name });
      addToast(`Git identity "${identity.name}" deleted`, "success");
      await load();
    } catch (e) {
      actionError = e instanceof Error ? e.message : "Failed to delete Git identity.";
      addToast(actionError, "error");
    }
  }

  async function setDefaultIdentity(identity: GitIdentity) {
    actionError = null;
    try {
      const client = createClient(AdminService, getTransport());
      await client.setGitIdentityDefault({ teamId: identity.teamId, name: identity.name });
      addToast(`Git identity "${identity.name}" is now the default`, "success");
      await load();
    } catch (e) {
      actionError = e instanceof Error ? e.message : "Failed to set the default Git identity.";
      addToast(actionError, "error");
    }
  }
</script>

<svelte:head>
  <title>Admin — Chetter</title>
</svelte:head>

<div class="p-6">
  <h1 class="text-2xl font-bold text-gray-900 dark:text-white mb-6">Admin</h1>

  {#if actionError}
    <Alert color="red" class="mb-4">{actionError}</Alert>
  {/if}

  {#if loading}
    <div class="flex items-center gap-2 text-gray-500 dark:text-gray-400">
      <Spinner size="4" /> Loading…
    </div>
  {:else}
    {#if createdToken}
      <Alert color="green" class="mb-6">
        <p class="text-sm font-medium mb-2">Token created — copy it now (shown only once):</p>
        <div class="flex gap-2">
          <code class="flex-1 px-3 py-2 bg-white dark:bg-gray-800 rounded font-mono text-sm text-gray-900 dark:text-white break-all">{createdToken}</code>
          <Button color="blue" onclick={() => { navigator.clipboard.writeText(createdToken!); }}>Copy</Button>
        </div>
        <Button color="alternative" size="xs" class="mt-2" onclick={() => createdToken = null}>Dismiss</Button>
      </Alert>
    {/if}

    <div class="grid grid-cols-1 lg:grid-cols-2 gap-6">
      <Card shadow="sm">
        <div class="px-4 py-3 border-b border-gray-200 dark:border-gray-700 flex items-center justify-between">
          <h2 class="font-semibold text-gray-900 dark:text-white">API Tokens</h2>
          <Button color="alternative" size="xs" onclick={() => showTokenForm = !showTokenForm}>
            {showTokenForm ? "Cancel" : "+ New"}
          </Button>
        </div>
        {#if showTokenForm}
          <div class="p-4 border-b border-gray-200 dark:border-gray-700 space-y-2">
            <Input bind:value={newTeam} placeholder="Team name(s), comma-separated" />
            <Input bind:value={newUser} placeholder="User name" />
            <Input bind:value={newTokenName} placeholder="Token name" />
            <Button color="blue" class="w-full" size="xs" onclick={createToken}>Create Token</Button>
          </div>
        {/if}
        <div class="divide-y divide-gray-200 dark:divide-gray-700">
          {#each tokens as token (token.name)}
            <div class="px-4 py-3 flex items-center justify-between">
              <div>
                <p class="text-sm font-medium text-gray-900 dark:text-white">{token.name}</p>
                <p class="text-xs text-gray-500 dark:text-gray-400">{token.userName} · {(token.teamNames ?? [token.teamName]).join(", ")}</p>
              </div>
              <Button color="red" size="xs" outline onclick={() => deleteToken(token.name)}>Delete</Button>
            </div>
          {:else}
            <p class="px-4 py-4 text-center text-sm text-gray-500 dark:text-gray-400">No tokens</p>
          {/each}
        </div>
      </Card>

      <Card shadow="sm">
        <div class="px-4 py-3 border-b border-gray-200 dark:border-gray-700 flex items-center justify-between">
          <h2 class="font-semibold text-gray-900 dark:text-white">Teams</h2>
          <Button color="alternative" size="xs" onclick={() => showTeamForm = !showTeamForm}>
            {showTeamForm ? "Cancel" : "+ New"}
          </Button>
        </div>
        {#if showTeamForm}
          <div class="p-4 border-b border-gray-200 dark:border-gray-700 space-y-2">
            <Input bind:value={newTeamName} placeholder="Team name" />
            <Button color="blue" class="w-full" size="xs" onclick={createTeamAction}>Create Team</Button>
          </div>
        {/if}
        <div class="divide-y divide-gray-200 dark:divide-gray-700">
          {#each teams as team (team.id)}
            <div class="px-4 py-3 flex items-center justify-between">
              <div>
                <p class="text-sm font-medium text-gray-900 dark:text-white">{team.name}</p>
                <p class="text-xs text-gray-500 dark:text-gray-400">{team.id}</p>
              </div>
              <Button color="red" size="xs" outline onclick={() => deleteTeamAction(team.name)}>Delete</Button>
            </div>
          {:else}
            <p class="px-4 py-4 text-center text-sm text-gray-500 dark:text-gray-400">No teams</p>
          {/each}
        </div>
      </Card>

      <Card shadow="sm">
        <div class="px-4 py-3 border-b border-gray-200 dark:border-gray-700 flex items-center justify-between">
          <div>
            <h2 class="font-semibold text-gray-900 dark:text-white">Git Identities</h2>
            <p class="text-xs text-gray-500 dark:text-gray-400">Credentials stay server-managed through the GitHub App.</p>
          </div>
          <Button color="alternative" size="xs" onclick={() => showIdentityForm = !showIdentityForm}>
            {showIdentityForm ? "Cancel" : "+ New"}
          </Button>
        </div>
        {#if showIdentityForm}
          <div class="p-4 border-b border-gray-200 dark:border-gray-700 space-y-2">
            <Input bind:value={newIdentityName} placeholder="Identity reference, e.g. primary-bot" />
            <Input bind:value={newGitAuthorName} placeholder="Git author name" />
            <Input type="email" bind:value={newGitAuthorEmail} placeholder="Git author email" />
            <Input bind:value={newIdentityTeam} placeholder="Team name (optional; global when empty)" />
            <Button color="blue" class="w-full" size="xs" onclick={createIdentity}>Create Identity</Button>
          </div>
        {/if}
        <div class="divide-y divide-gray-200 dark:divide-gray-700">
          {#each identities as identity (identity.id)}
            <div class="px-4 py-3 flex items-center justify-between gap-3">
              <div class="min-w-0">
                <div class="flex items-center gap-2">
                  <p class="text-sm font-medium text-gray-900 dark:text-white">{identity.name}</p>
                  {#if identity.isDefault}
                    <Badge color="blue" size="small">Default</Badge>
                  {/if}
                </div>
                <p class="text-xs text-gray-500 dark:text-gray-400 truncate">{identity.gitAuthorName} &lt;{identity.gitAuthorEmail}&gt;</p>
                <p class="text-xs text-gray-500 dark:text-gray-400">{identity.teamId ? `Team: ${identity.teamId}` : "Global"}</p>
              </div>
              <div class="flex gap-2">
                {#if !identity.isDefault}
                  <Button color="alternative" size="xs" onclick={() => setDefaultIdentity(identity)}>Set Default</Button>
                {/if}
                <Button color="red" size="xs" outline onclick={() => deleteIdentity(identity)}>Delete</Button>
              </div>
            </div>
          {:else}
            <p class="px-4 py-4 text-center text-sm text-gray-500 dark:text-gray-400">No Git identities</p>
          {/each}
        </div>
      </Card>
    </div>
  {/if}
</div>
