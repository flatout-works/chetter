<script lang="ts">
  import { onMount } from "svelte";
  import { createClient } from "@connectrpc/connect";
  import { AdminService } from "$gen/proto/api/v1/api_pb";
  import type { AuditEvent } from "$gen/proto/api/v1/api_pb";
  import { getTransport } from "$lib/api/client";

  let events = $state<AuditEvent[]>([]);
  let loading = $state(true);
  let eventTypeFilter = $state("");
  let sourceTypeFilter = $state("");
  let sinceHours = $state(24);
  let limit = $state(100);

  async function load() {
    loading = true;
    try {
      const client = createClient(AdminService, getTransport());
      const resp = await client.listAuditEvents({
        eventType: eventTypeFilter || undefined,
        sourceType: sourceTypeFilter || undefined,
        sinceHours,
        limit,
      });
      events = resp.events ?? [];
    } catch (e) {
      console.error(e);
    } finally {
      loading = false;
    }
  }

  onMount(load);

  const eventColors: Record<string, string> = {
    webhook_received: "bg-green-100 text-green-800 dark:bg-green-900/30 dark:text-green-400",
    task_submitted: "bg-blue-100 text-blue-800 dark:bg-blue-900/30 dark:text-blue-400",
    trigger_matched: "bg-purple-100 text-purple-800 dark:bg-purple-900/30 dark:text-purple-400",
    artifact_discovered: "bg-yellow-100 text-yellow-800 dark:bg-yellow-900/30 dark:text-yellow-400",
  };
</script>

<svelte:head>
  <title>Audit Log — Chetter</title>
</svelte:head>

<div class="p-6">
  <div class="flex items-center justify-between mb-6">
    <h1 class="text-2xl font-bold text-gray-900 dark:text-white">Audit Log</h1>
    <div class="flex items-center gap-3">
      <select bind:value={eventTypeFilter} onchange={load} class="px-3 py-2 border border-gray-300 dark:border-gray-600 rounded-lg bg-white dark:bg-gray-700 text-gray-900 dark:text-white text-sm">
        <option value="">All types</option>
        <option value="webhook_received">Webhook Received</option>
        <option value="task_submitted">Task Submitted</option>
        <option value="trigger_matched">Trigger Matched</option>
        <option value="artifact_discovered">Artifact Discovered</option>
      </select>
      <select bind:value={sourceTypeFilter} onchange={load} class="px-3 py-2 border border-gray-300 dark:border-gray-600 rounded-lg bg-white dark:bg-gray-700 text-gray-900 dark:text-white text-sm">
        <option value="">All sources</option>
        <option value="webhook">Webhook</option>
        <option value="trigger">Trigger</option>
        <option value="task">Task</option>
      </select>
      <select bind:value={sinceHours} onchange={load} class="px-3 py-2 border border-gray-300 dark:border-gray-600 rounded-lg bg-white dark:bg-gray-700 text-gray-900 dark:text-white text-sm">
        <option value={1}>Last hour</option>
        <option value={6}>Last 6 hours</option>
        <option value={24}>Last 24 hours</option>
        <option value={72}>Last 3 days</option>
        <option value={168}>Last 7 days</option>
      </select>
      <input type="number" bind:value={limit} placeholder="Limit" class="w-20 px-3 py-2 border border-gray-300 dark:border-gray-600 rounded-lg bg-white dark:bg-gray-700 text-gray-900 dark:text-white text-sm" />
      <button onclick={load} class="px-4 py-2 bg-blue-600 hover:bg-blue-700 text-white text-sm font-medium rounded-lg">Refresh</button>
    </div>
  </div>

  {#if loading}
    <p class="text-gray-500 dark:text-gray-400">Loading…</p>
  {:else}
    <div class="bg-white dark:bg-gray-800 rounded-lg shadow-sm border border-gray-200 dark:border-gray-700 overflow-hidden">
      <table class="w-full">
        <thead class="bg-gray-50 dark:bg-gray-700/50">
          <tr>
            <th class="px-4 py-3 text-left text-xs font-medium text-gray-500 dark:text-gray-400 uppercase">Time</th>
            <th class="px-4 py-3 text-left text-xs font-medium text-gray-500 dark:text-gray-400 uppercase">Event Type</th>
            <th class="px-4 py-3 text-left text-xs font-medium text-gray-500 dark:text-gray-400 uppercase">Source</th>
            <th class="px-4 py-3 text-left text-xs font-medium text-gray-500 dark:text-gray-400 uppercase">Target</th>
            <th class="px-4 py-3 text-left text-xs font-medium text-gray-500 dark:text-gray-400 uppercase">Detail</th>
          </tr>
        </thead>
        <tbody class="divide-y divide-gray-200 dark:divide-gray-700">
          {#each events as event (event.id)}
            <tr class="hover:bg-gray-50 dark:hover:bg-gray-700/50">
              <td class="px-4 py-3 text-sm text-gray-500 dark:text-gray-400 whitespace-nowrap">{new Date(event.createdAt).toLocaleString()}</td>
              <td class="px-4 py-3">
                <span class={`px-2 py-0.5 rounded text-xs font-medium ${eventColors[event.eventType] || "bg-gray-100 text-gray-800 dark:bg-gray-700 dark:text-gray-300"}`}>
                  {event.eventType}
                </span>
              </td>
              <td class="px-4 py-3 text-sm text-gray-700 dark:text-gray-300">
                {#if event.sourceType}
                  <span class="font-medium">{event.sourceType}</span>
                  {#if event.sourceId}
                    <span class="text-gray-500">: {event.sourceId.slice(0, 24)}</span>
                  {/if}
                {:else}
                  <span class="text-gray-400">—</span>
                {/if}
              </td>
              <td class="px-4 py-3 text-sm text-gray-700 dark:text-gray-300">
                {#if event.targetType}
                  <span class="font-medium">{event.targetType}</span>
                  {#if event.targetId}
                    <span class="text-gray-500">: {event.targetId.slice(0, 24)}</span>
                  {/if}
                {:else}
                  <span class="text-gray-400">—</span>
                {/if}
              </td>
              <td class="px-4 py-3 text-sm text-gray-500 dark:text-gray-400 max-w-xs truncate">{event.detail || "—"}</td>
            </tr>
          {:else}
            <tr>
              <td colspan="5" class="px-4 py-8 text-center text-gray-500 dark:text-gray-400">No audit events found</td>
            </tr>
          {/each}
        </tbody>
      </table>
    </div>
  {/if}
</div>
