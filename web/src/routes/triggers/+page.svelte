<script lang="ts">
  import { onMount } from "svelte";
  import { createClient } from "@connectrpc/connect";
  import { TriggerService } from "$gen/proto/api/v1/api_pb";
  import type { Trigger } from "$gen/proto/api/v1/api_pb";
  import { getTransport } from "$lib/api/client";

  let triggers = $state<Trigger[]>([]);
  let loading = $state(true);
  let showCreateForm = $state(false);
  let creating = $state(false);
  let actionError = $state<string | null>(null);
  let name = $state("");
  let triggerType = $state("cron");
  let cronExpr = $state("@hourly");
  let repo = $state("");
  let event = $state("");
  let prompt = $state("");
  let gitUrl = $state("");
  let gitRef = $state("");
  let agent = $state("");
  let modelId = $state("");

  async function load() {
    try {
      const client = createClient(TriggerService, getTransport());
      const resp = await client.listTriggers({});
      triggers = resp.triggers ?? [];
    } catch (e) {
      console.error(e);
    } finally {
      loading = false;
    }
  }

  onMount(load);

  async function toggleEnabled(trigger: Trigger) {
    actionError = null;
    try {
      const client = createClient(TriggerService, getTransport());
      await client.updateTrigger({ name: trigger.name, enabled: !trigger.enabled });
      await load();
    } catch (e) {
      actionError = e instanceof Error ? e.message : "Failed to update trigger.";
      console.error(e);
    }
  }

  async function runNow(name: string) {
    actionError = null;
    try {
      const client = createClient(TriggerService, getTransport());
      await client.runTrigger({ name });
    } catch (e) {
      actionError = e instanceof Error ? e.message : "Failed to run trigger.";
      console.error(e);
    }
  }

  async function deleteTrigger(name: string) {
    if (!confirm(`Delete trigger "${name}"?`)) return;
    actionError = null;
    try {
      const client = createClient(TriggerService, getTransport());
      await client.deleteTrigger({ name });
      await load();
    } catch (e) {
      actionError = e instanceof Error ? e.message : "Failed to delete trigger.";
      console.error(e);
    }
  }

  async function createTrigger(e: Event) {
    e.preventDefault();
    actionError = null;
    if (!name.trim()) {
      actionError = "Name is required.";
      return;
    }
    if (triggerType === "cron" && !cronExpr.trim()) {
      actionError = "Cron expression is required for cron triggers.";
      return;
    }
    if (triggerType !== "cron" && !repo.trim()) {
      actionError = "Repository is required for webhook triggers.";
      return;
    }

    creating = true;
    try {
      const client = createClient(TriggerService, getTransport());
      await client.createTrigger({
        name: name.trim(),
        triggerType,
        cronExpr: cronExpr.trim(),
        repo: repo.trim(),
        event: event.trim(),
        prompt: prompt.trim(),
        gitUrl: gitUrl.trim(),
        gitRef: gitRef.trim(),
        agent: agent.trim(),
        modelId: modelId.trim(),
      });
      name = "";
      triggerType = "cron";
      cronExpr = "@hourly";
      repo = "";
      event = "";
      prompt = "";
      gitUrl = "";
      gitRef = "";
      agent = "";
      modelId = "";
      showCreateForm = false;
      await load();
    } catch (err) {
      actionError = err instanceof Error ? err.message : "Failed to create trigger.";
    } finally {
      creating = false;
    }
  }

  function triggerTarget(trigger: Trigger): string {
    if (trigger.cronExpr) return trigger.cronExpr;
    try {
      return JSON.parse(trigger.triggerConfig || "{}").repo || "—";
    } catch {
      return "—";
    }
  }
</script>

<svelte:head>
  <title>Triggers — Chetter</title>
</svelte:head>

<div class="p-6">
  <div class="flex items-center justify-between mb-6">
    <h1 class="text-2xl font-bold text-gray-900 dark:text-white">Triggers</h1>
    <button
      onclick={() => { showCreateForm = !showCreateForm; actionError = null; }}
      class="px-4 py-2 bg-blue-600 hover:bg-blue-700 text-white text-sm font-medium rounded-lg"
    >
      {showCreateForm ? "Cancel" : "Create Trigger"}
    </button>
  </div>

  {#if actionError}
    <div class="mb-4 p-3 bg-red-50 dark:bg-red-900/20 text-red-700 dark:text-red-400 rounded-lg text-sm">{actionError}</div>
  {/if}

  {#if showCreateForm}
    <form onsubmit={createTrigger} class="mb-6 bg-white dark:bg-gray-800 rounded-lg shadow-sm border border-gray-200 dark:border-gray-700 p-4 space-y-4">
      <div class="grid grid-cols-1 md:grid-cols-3 gap-3">
        <input bind:value={name} placeholder="Name" class="px-3 py-2 border border-gray-300 dark:border-gray-600 rounded-lg bg-white dark:bg-gray-700 text-gray-900 dark:text-white text-sm" />
        <select bind:value={triggerType} class="px-3 py-2 border border-gray-300 dark:border-gray-600 rounded-lg bg-white dark:bg-gray-700 text-gray-900 dark:text-white text-sm">
          <option value="cron">Cron</option>
          <option value="pr_review">PR Review</option>
          <option value="issue">Issue</option>
        </select>
        <input bind:value={cronExpr} placeholder="Cron expression" disabled={triggerType !== "cron"} class="px-3 py-2 border border-gray-300 dark:border-gray-600 rounded-lg bg-white dark:bg-gray-700 disabled:bg-gray-100 dark:disabled:bg-gray-800 text-gray-900 dark:text-white text-sm" />
      </div>
      <div class="grid grid-cols-1 md:grid-cols-2 gap-3">
        <input bind:value={repo} placeholder="Repository, e.g. org/repo" class="px-3 py-2 border border-gray-300 dark:border-gray-600 rounded-lg bg-white dark:bg-gray-700 text-gray-900 dark:text-white text-sm" />
        <input bind:value={event} placeholder="Event (optional)" class="px-3 py-2 border border-gray-300 dark:border-gray-600 rounded-lg bg-white dark:bg-gray-700 text-gray-900 dark:text-white text-sm" />
        <input bind:value={gitUrl} placeholder="Git URL (optional)" class="px-3 py-2 border border-gray-300 dark:border-gray-600 rounded-lg bg-white dark:bg-gray-700 text-gray-900 dark:text-white text-sm" />
        <input bind:value={gitRef} placeholder="Git ref (optional)" class="px-3 py-2 border border-gray-300 dark:border-gray-600 rounded-lg bg-white dark:bg-gray-700 text-gray-900 dark:text-white text-sm" />
        <input bind:value={agent} placeholder="Agent (optional)" class="px-3 py-2 border border-gray-300 dark:border-gray-600 rounded-lg bg-white dark:bg-gray-700 text-gray-900 dark:text-white text-sm" />
        <input bind:value={modelId} placeholder="Model ID (optional)" class="px-3 py-2 border border-gray-300 dark:border-gray-600 rounded-lg bg-white dark:bg-gray-700 text-gray-900 dark:text-white text-sm" />
      </div>
      <textarea bind:value={prompt} rows="3" placeholder="Prompt override (optional)" class="w-full px-3 py-2 border border-gray-300 dark:border-gray-600 rounded-lg bg-white dark:bg-gray-700 text-gray-900 dark:text-white text-sm"></textarea>
      <button type="submit" disabled={creating} class="px-4 py-2 bg-blue-600 hover:bg-blue-700 disabled:bg-blue-400 text-white text-sm font-medium rounded-lg">
        {creating ? "Creating…" : "Create"}
      </button>
    </form>
  {/if}

  {#if loading}
    <p class="text-gray-500 dark:text-gray-400">Loading…</p>
  {:else}
    <div class="bg-white dark:bg-gray-800 rounded-lg shadow-sm border border-gray-200 dark:border-gray-700 overflow-hidden">
      <table class="w-full">
        <thead class="bg-gray-50 dark:bg-gray-700/50">
          <tr>
            <th class="px-4 py-3 text-left text-xs font-medium text-gray-500 dark:text-gray-400 uppercase">Name</th>
            <th class="px-4 py-3 text-left text-xs font-medium text-gray-500 dark:text-gray-400 uppercase">Type</th>
            <th class="px-4 py-3 text-left text-xs font-medium text-gray-500 dark:text-gray-400 uppercase">Cron/Repo</th>
            <th class="px-4 py-3 text-left text-xs font-medium text-gray-500 dark:text-gray-400 uppercase">Agent</th>
            <th class="px-4 py-3 text-left text-xs font-medium text-gray-500 dark:text-gray-400 uppercase">Enabled</th>
            <th class="px-4 py-3 text-right text-xs font-medium text-gray-500 dark:text-gray-400 uppercase">Actions</th>
          </tr>
        </thead>
        <tbody class="divide-y divide-gray-200 dark:divide-gray-700">
          {#each triggers as trigger (trigger.id)}
            <tr class="hover:bg-gray-50 dark:hover:bg-gray-700/50">
              <td class="px-4 py-3 text-sm font-medium text-gray-900 dark:text-white">{trigger.name}</td>
              <td class="px-4 py-3">
                <span class="px-2 py-0.5 rounded text-xs font-medium bg-purple-100 text-purple-800 dark:bg-purple-900/30 dark:text-purple-400">
                  {trigger.triggerType}
                </span>
              </td>
              <td class="px-4 py-3 text-sm text-gray-700 dark:text-gray-300">
                {triggerTarget(trigger)}
              </td>
              <td class="px-4 py-3 text-sm text-gray-700 dark:text-gray-300">{trigger.agent || "—"}</td>
              <td class="px-4 py-3">
                <button
                  onclick={() => toggleEnabled(trigger)}
                  aria-label={`${trigger.enabled ? "Disable" : "Enable"} ${trigger.name}`}
                  class="relative w-10 h-5 rounded-full transition-colors {trigger.enabled ? 'bg-green-500' : 'bg-gray-300 dark:bg-gray-600'}"
                >
                  <span class="absolute top-0.5 left-0.5 w-4 h-4 bg-white rounded-full transition-transform {trigger.enabled ? 'translate-x-5' : ''}"></span>
                </button>
              </td>
              <td class="px-4 py-3 text-right">
                <div class="flex justify-end gap-2">
                  <button onclick={() => runNow(trigger.name)} class="px-2 py-1 text-xs bg-blue-600 hover:bg-blue-700 text-white rounded">Run</button>
                  <button onclick={() => deleteTrigger(trigger.name)} class="px-2 py-1 text-xs bg-red-600 hover:bg-red-700 text-white rounded">Delete</button>
                </div>
              </td>
            </tr>
          {:else}
            <tr>
              <td colspan="6" class="px-4 py-8 text-center text-gray-500 dark:text-gray-400">No triggers found</td>
            </tr>
          {/each}
        </tbody>
      </table>
    </div>
  {/if}
</div>
