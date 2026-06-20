<script lang="ts">
  import { onMount } from "svelte";
  import { resolve } from "$app/paths";
  import { createClient } from "@connectrpc/connect";
  import { SessionService } from "$gen/proto/api/v1/api_pb";
  import type { AgentSession, SessionRun } from "$gen/proto/api/v1/api_pb";
  import { getTransport } from "$lib/api/client";

  let { params } = $props();
  let session = $state<AgentSession | null>(null);
  let runs = $state<SessionRun[]>([]);
  let loading = $state(true);
  let error = $state<string | null>(null);

  const statusColors: Record<string, string> = {
    running: "bg-green-100 text-green-800 dark:bg-green-900/30 dark:text-green-400",
    paused_waiting_review: "bg-yellow-100 text-yellow-800 dark:bg-yellow-900/30 dark:text-yellow-400",
    paused: "bg-yellow-100 text-yellow-800 dark:bg-yellow-900/30 dark:text-yellow-400",
    resuming: "bg-purple-100 text-purple-800 dark:bg-purple-900/30 dark:text-purple-400",
    completed: "bg-blue-100 text-blue-800 dark:bg-blue-900/30 dark:text-blue-400",
    error: "bg-red-100 text-red-800 dark:bg-red-900/30 dark:text-red-400",
  };

  const runStatusColors: Record<string, string> = {
    pending: "bg-yellow-100 text-yellow-800 dark:bg-yellow-900/30 dark:text-yellow-400",
    running: "bg-green-100 text-green-800 dark:bg-green-900/30 dark:text-green-400",
    done: "bg-blue-100 text-blue-800 dark:bg-blue-900/30 dark:text-blue-400",
    error: "bg-red-100 text-red-800 dark:bg-red-900/30 dark:text-red-400",
  };

  async function resume() {
    const followUpPrompt = window.prompt("Enter follow-up prompt:");
    if (!followUpPrompt) return;
    try {
      const client = createClient(SessionService, getTransport());
      await client.resumeSession({ sessionId: params.id, prompt: followUpPrompt });
      await load();
    } catch (e) {
      error = e instanceof Error ? e.message : "Failed to resume session.";
      console.error(e);
    }
  }

  async function load() {
    try {
      const client = createClient(SessionService, getTransport());
      const resp = await client.getSession({ sessionId: params.id });
      session = resp.session ?? null;
      runs = resp.runs ?? [];
    } catch (e) {
      error = e instanceof Error ? e.message : "Failed to load session.";
      console.error(e);
    } finally {
      loading = false;
    }
  }

  onMount(load);

  function formatTime(ts: string): string {
    if (!ts) return "—";
    try {
      return new Date(ts).toLocaleString();
    } catch {
      return ts;
    }
  }
</script>

<svelte:head>
  <title>Session {params.id.slice(0, 12)}… — Chetter</title>
</svelte:head>

<div class="p-6 max-w-6xl">
  {#if loading}
    <p class="text-gray-500 dark:text-gray-400">Loading…</p>
  {:else if error}
    <div class="bg-red-50 dark:bg-red-900/20 text-red-700 dark:text-red-400 p-4 rounded-lg">{error}</div>
  {:else if session}
    <!-- Header -->
    <div class="flex items-center justify-between mb-6">
      <div>
        <div class="flex items-center gap-3 mb-1">
          <h1 class="text-xl font-mono font-bold text-gray-900 dark:text-white">{session.id}</h1>
          <span class={`px-2 py-0.5 rounded text-xs font-medium ${statusColors[session.status] || "bg-gray-100 text-gray-800 dark:bg-gray-700 dark:text-gray-300"}`}>
            {session.status}
          </span>
        </div>
        <p class="text-sm text-gray-500 dark:text-gray-400">
          Created {formatTime(session.createdAt)} · Updated {formatTime(session.updatedAt)}
        </p>
      </div>
      {#if session.status === "paused_waiting_review" || session.status === "paused"}
        <button onclick={resume} class="px-3 py-1.5 text-sm bg-green-600 hover:bg-green-700 text-white rounded-lg font-medium">
          Resume
        </button>
      {/if}
    </div>

    <!-- Session metadata -->
    <div class="grid grid-cols-2 md:grid-cols-4 gap-4 mb-6">
      <div class="bg-white dark:bg-gray-800 rounded-lg p-3 border border-gray-200 dark:border-gray-700">
        <p class="text-xs text-gray-500 dark:text-gray-400 mb-1">Agent</p>
        <p class="text-sm font-medium text-gray-900 dark:text-white">{session.agent || "—"}</p>
      </div>
      <div class="bg-white dark:bg-gray-800 rounded-lg p-3 border border-gray-200 dark:border-gray-700">
        <p class="text-xs text-gray-500 dark:text-gray-400 mb-1">Model</p>
        <p class="text-sm font-medium text-gray-900 dark:text-white">{session.modelId || "—"}</p>
      </div>
      <div class="bg-white dark:bg-gray-800 rounded-lg p-3 border border-gray-200 dark:border-gray-700">
        <p class="text-xs text-gray-500 dark:text-gray-400 mb-1">Resume Mode</p>
        <p class="text-sm font-medium text-gray-900 dark:text-white">{session.resumeMode || "none"}</p>
      </div>
      <div class="bg-white dark:bg-gray-800 rounded-lg p-3 border border-gray-200 dark:border-gray-700">
        <p class="text-xs text-gray-500 dark:text-gray-400 mb-1">Pinned Runner</p>
        <p class="text-sm font-medium text-gray-900 dark:text-white truncate">{session.pinnedRunnerId || "—"}</p>
      </div>
    </div>

    {#if session.pauseReason}
      <div class="bg-yellow-50 dark:bg-yellow-900/20 text-yellow-700 dark:text-yellow-400 p-4 rounded-lg mb-6">
        <p class="text-sm"><span class="font-semibold">Pause reason:</span> {session.pauseReason}</p>
      </div>
    {/if}

    {#if session.error}
      <div class="bg-red-50 dark:bg-red-900/20 text-red-700 dark:text-red-400 p-4 rounded-lg mb-6">
        <p class="text-sm font-mono">{session.error}</p>
      </div>
    {/if}

    <!-- Runs -->
    <div class="bg-white dark:bg-gray-800 rounded-lg shadow-sm border border-gray-200 dark:border-gray-700 overflow-hidden">
      <div class="px-4 py-3 border-b border-gray-200 dark:border-gray-700">
        <h2 class="font-semibold text-gray-900 dark:text-white">Session Runs ({runs.length})</h2>
      </div>
      {#if runs.length > 0}
        <table class="w-full">
          <thead class="bg-gray-50 dark:bg-gray-700/50">
            <tr>
              <th class="px-4 py-3 text-left text-xs font-medium text-gray-500 dark:text-gray-400 uppercase">Run ID</th>
              <th class="px-4 py-3 text-left text-xs font-medium text-gray-500 dark:text-gray-400 uppercase">Task</th>
              <th class="px-4 py-3 text-left text-xs font-medium text-gray-500 dark:text-gray-400 uppercase">Status</th>
              <th class="px-4 py-3 text-left text-xs font-medium text-gray-500 dark:text-gray-400 uppercase">Summary</th>
              <th class="px-4 py-3 text-left text-xs font-medium text-gray-500 dark:text-gray-400 uppercase">Started</th>
            </tr>
          </thead>
          <tbody class="divide-y divide-gray-200 dark:divide-gray-700">
            {#each runs as run (run.id)}
              <tr class="hover:bg-gray-50 dark:hover:bg-gray-700/50">
                <td class="px-4 py-3 text-sm font-mono text-gray-700 dark:text-gray-300">{run.id.slice(0, 20)}…</td>
                <td class="px-4 py-3">
                  <a href={resolve("/tasks/[id]", { id: run.taskId })} class="text-sm font-mono text-blue-600 dark:text-blue-400 hover:underline">
                    {run.taskId.slice(0, 20)}…
                  </a>
                </td>
                <td class="px-4 py-3">
                  <span class={`px-2 py-0.5 rounded text-xs font-medium ${runStatusColors[run.status] || "bg-gray-100 text-gray-800 dark:bg-gray-700 dark:text-gray-300"}`}>
                    {run.status}
                  </span>
                </td>
                <td class="px-4 py-3 text-sm text-gray-500 dark:text-gray-400 max-w-xs truncate">{run.summary || "—"}</td>
                <td class="px-4 py-3 text-sm text-gray-500 dark:text-gray-400 whitespace-nowrap">{formatTime(run.startedAt || "")}</td>
              </tr>
            {/each}
          </tbody>
        </table>
      {:else}
        <p class="px-4 py-8 text-center text-gray-500 dark:text-gray-400">No runs recorded</p>
      {/if}
    </div>
  {/if}
</div>
