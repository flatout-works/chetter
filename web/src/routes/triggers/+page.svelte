<script lang="ts">
  import { onMount } from "svelte";
  import { createClient } from "@connectrpc/connect";
  import { TriggerService } from "$gen/proto/api/v1/api_pb";
  import type { Trigger } from "$gen/proto/api/v1/api_pb";
  import { getTransport } from "$lib/api/client";

  let triggers = $state<Trigger[]>([]);
  let loading = $state(true);

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
    try {
      const client = createClient(TriggerService, getTransport());
      await client.updateTrigger({ name: trigger.name, enabled: !trigger.enabled });
      await load();
    } catch (e) {
      console.error(e);
    }
  }

  async function runNow(name: string) {
    try {
      const client = createClient(TriggerService, getTransport());
      await client.runTrigger({ name });
    } catch (e) {
      console.error(e);
    }
  }

  async function deleteTrigger(name: string) {
    if (!confirm(`Delete trigger "${name}"?`)) return;
    try {
      const client = createClient(TriggerService, getTransport());
      await client.deleteTrigger({ name });
      await load();
    } catch (e) {
      console.error(e);
    }
  }
</script>

<svelte:head>
  <title>Triggers — Chetter</title>
</svelte:head>

<div class="p-6">
  <h1 class="text-2xl font-bold text-gray-900 dark:text-white mb-6">Triggers</h1>

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
                {trigger.cronExpr || JSON.parse(trigger.triggerConfig || "{}").repo || "—"}
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
