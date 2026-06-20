<script lang="ts">
  import { onMount } from "svelte";
  import { resolve } from "$app/paths";
  import { createClient } from "@connectrpc/connect";
  import { SessionService } from "$gen/proto/api/v1/api_pb";
  import type { AgentSession } from "$gen/proto/api/v1/api_pb";
  import { getTransport } from "$lib/api/client";

  let sessions = $state<AgentSession[]>([]);
  let loading = $state(true);
  let statusFilter = $state("");

  async function load() {
    try {
      const client = createClient(SessionService, getTransport());
      const resp = await client.listSessions({ status: statusFilter, limit: 50 });
      sessions = resp.sessions ?? [];
    } catch (e) {
      console.error(e);
    } finally {
      loading = false;
    }
  }

  onMount(load);

  const statusColors: Record<string, string> = {
    running: "bg-green-100 text-green-800 dark:bg-green-900/30 dark:text-green-400",
    paused_waiting_review: "bg-yellow-100 text-yellow-800 dark:bg-yellow-900/30 dark:text-yellow-400",
    completed: "bg-blue-100 text-blue-800 dark:bg-blue-900/30 dark:text-blue-400",
    error: "bg-red-100 text-red-800 dark:bg-red-900/30 dark:text-red-400",
    resuming: "bg-purple-100 text-purple-800 dark:bg-purple-900/30 dark:text-purple-400",
  };

  async function resume(sessionId: string) {
    const followUpPrompt = window.prompt("Enter follow-up prompt:");
    if (!followUpPrompt) return;
    try {
      const client = createClient(SessionService, getTransport());
      await client.resumeSession({ sessionId, prompt: followUpPrompt });
      await load();
    } catch (e) {
      console.error(e);
    }
  }
</script>

<svelte:head>
  <title>Sessions — Chetter</title>
</svelte:head>

<div class="p-6">
  <div class="flex items-center justify-between mb-6">
    <h1 class="text-2xl font-bold text-gray-900 dark:text-white">Agent Sessions</h1>
    <select
      bind:value={statusFilter}
      onchange={load}
      class="px-3 py-2 border border-gray-300 dark:border-gray-600 rounded-lg bg-white dark:bg-gray-700 text-gray-900 dark:text-white text-sm"
    >
      <option value="">All</option>
      <option value="running">Running</option>
      <option value="paused_waiting_review">Paused</option>
      <option value="completed">Completed</option>
      <option value="error">Error</option>
    </select>
  </div>

  {#if loading}
    <p class="text-gray-500 dark:text-gray-400">Loading…</p>
  {:else}
    <div class="bg-white dark:bg-gray-800 rounded-lg shadow-sm border border-gray-200 dark:border-gray-700 overflow-hidden">
      <table class="w-full">
        <thead class="bg-gray-50 dark:bg-gray-700/50">
          <tr>
            <th class="px-4 py-3 text-left text-xs font-medium text-gray-500 dark:text-gray-400 uppercase">Session ID</th>
            <th class="px-4 py-3 text-left text-xs font-medium text-gray-500 dark:text-gray-400 uppercase">Status</th>
            <th class="px-4 py-3 text-left text-xs font-medium text-gray-500 dark:text-gray-400 uppercase">Agent</th>
            <th class="px-4 py-3 text-left text-xs font-medium text-gray-500 dark:text-gray-400 uppercase">Model</th>
            <th class="px-4 py-3 text-left text-xs font-medium text-gray-500 dark:text-gray-400 uppercase">Created</th>
            <th class="px-4 py-3 text-right text-xs font-medium text-gray-500 dark:text-gray-400 uppercase">Actions</th>
          </tr>
        </thead>
        <tbody class="divide-y divide-gray-200 dark:divide-gray-700">
          {#each sessions as session (session.id)}
            <tr class="hover:bg-gray-50 dark:hover:bg-gray-700/50">
              <td class="px-4 py-3">
                <a href={resolve("/sessions/[id]", { id: session.id })} class="text-sm font-mono text-blue-600 dark:text-blue-400 hover:underline">
                  {session.id.slice(0, 20)}…
                </a>
              </td>
              <td class="px-4 py-3">
                <span class={`px-2 py-0.5 rounded text-xs font-medium ${statusColors[session.status] || statusColors.running}`}>
                  {session.status}
                </span>
              </td>
              <td class="px-4 py-3 text-sm text-gray-700 dark:text-gray-300">{session.agent || "—"}</td>
              <td class="px-4 py-3 text-sm text-gray-700 dark:text-gray-300">{session.modelId || "—"}</td>
              <td class="px-4 py-3 text-sm text-gray-500 dark:text-gray-400">{new Date(session.createdAt).toLocaleDateString()}</td>
              <td class="px-4 py-3 text-right">
                {#if session.status === "paused_waiting_review"}
                  <button onclick={() => resume(session.id)} class="px-2 py-1 text-xs bg-green-600 hover:bg-green-700 text-white rounded">
                    Resume
                  </button>
                {/if}
              </td>
            </tr>
          {:else}
            <tr>
              <td colspan="6" class="px-4 py-8 text-center text-gray-500 dark:text-gray-400">No sessions found</td>
            </tr>
          {/each}
        </tbody>
      </table>
    </div>
  {/if}
</div>
