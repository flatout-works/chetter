<script lang="ts">
  import { onDestroy } from "svelte";
  import { resolve } from "$app/paths";
  import { createClient } from "@connectrpc/connect";
  import { AdminService } from "$gen/proto/api/v1/api_pb";
  import type { SelfTestRun } from "$gen/proto/api/v1/api_pb";
  import { getTransport } from "$lib/api/client";
  import { addToast } from "$lib/stores/toast.svelte";
  import StatusBadge from "$lib/components/StatusBadge.svelte";
  import { formatTime } from "$lib/utils.svelte";
  import {
    Alert,
    Button,
    Card,
    Label,
    Select,
    Spinner,
    Table,
    TableBody,
    TableBodyCell,
    TableBodyRow,
    TableHead,
    TableHeadCell,
  } from "flowbite-svelte";

  const client = createClient(AdminService, getTransport());
  const profiles = [
    { value: "quick", label: "Quick", detail: "Default OpenCode harness, provider, MCP discovery, and tool call." },
    { value: "harnesses", label: "Harnesses", detail: "OpenCode, Claude Code, Pi, CodeWhale, and Codex." },
    { value: "providers", label: "Providers", detail: "Every model provider enabled for the reference OpenCode harness." },
    { value: "full", label: "Full", detail: "All harness and provider checks, plus the configured GitHub credential clone check. This consumes the most quota." },
  ] as const;

  let profile = $state("quick");
  let run = $state.raw<SelfTestRun | null>(null);
  let running = $state(false);
  let refreshing = $state(false);
  let error = $state<string | null>(null);
  let pollTimer: ReturnType<typeof setInterval> | undefined;

  let selectedProfile = $derived(profiles.find((entry) => entry.value === profile) ?? profiles[0]);
  let terminal = $derived(run?.status === "passed" || run?.status === "failed");

  function stopPolling() {
    if (pollTimer) {
      clearInterval(pollTimer);
      pollTimer = undefined;
    }
  }

  function startPolling() {
    stopPolling();
    pollTimer = setInterval(refresh, 3_000);
  }

  async function start() {
    running = true;
    error = null;
    stopPolling();
    try {
      const response = await client.runSelfTest({ profile });
      run = response.run ?? null;
      if (!run) throw new Error("Chetter returned no self-test run.");
      addToast(`${selectedProfile.label} self-test started`, "success");
      startPolling();
    } catch (e) {
      error = e instanceof Error ? e.message : "Failed to start deployment self-test.";
    } finally {
      running = false;
    }
  }

  async function refresh() {
    if (!run || refreshing) return;
    refreshing = true;
    try {
      const response = await client.getSelfTestStatus({ runId: run.id });
      run = response.run ?? run;
      if (run.status === "passed" || run.status === "failed") {
        stopPolling();
        addToast(`Self-test ${run.status}`, run.status === "passed" ? "success" : "error");
      }
    } catch (e) {
      error = e instanceof Error ? e.message : "Failed to refresh self-test status.";
      stopPolling();
    } finally {
      refreshing = false;
    }
  }

  onDestroy(stopPolling);
</script>

<svelte:head>
  <title>Diagnostics — Chetter</title>
</svelte:head>

<div class="p-6 space-y-6">
  <div>
    <h1 class="text-2xl font-bold text-gray-900 dark:text-white">Deployment Diagnostics</h1>
    <p class="mt-1 text-sm text-gray-500 dark:text-gray-400">
      Run real tasks to verify runner claims, harness startup, model credentials, authenticated MCP discovery, and tool execution.
    </p>
  </div>

  {#if error}
    <Alert color="red">{error}</Alert>
  {/if}

  <Card size="xl" class="!p-5 w-full" shadow="sm">
    <div class="flex flex-col gap-4 lg:flex-row lg:items-end lg:justify-between">
      <div class="max-w-2xl flex-1">
        <Label for="self-test-profile" class="mb-1">Profile</Label>
        <Select id="self-test-profile" bind:value={profile} disabled={running || (!!run && !terminal)}>
          {#each profiles as option (option.value)}
            <option value={option.value}>{option.label}</option>
          {/each}
        </Select>
        <p class="mt-2 text-sm text-gray-500 dark:text-gray-400">{selectedProfile.detail}</p>
      </div>
      <div class="flex items-center gap-2">
        {#if run && !terminal}
          <Button color="light" disabled={refreshing} onclick={refresh}>
            {#if refreshing}<Spinner size="4" class="mr-2" />{/if}
            Refresh
          </Button>
        {/if}
        <Button color="blue" disabled={running || (!!run && !terminal)} onclick={start}>
          {#if running}<Spinner size="4" class="mr-2" />{/if}
          Run self-test
        </Button>
      </div>
    </div>
  </Card>

  {#if run}
    <Card size="xl" class="!p-0 w-full" shadow="sm">
      <div class="flex flex-wrap items-center justify-between gap-3 border-b border-gray-200 px-5 py-4 dark:border-gray-700">
        <div>
          <div class="flex items-center gap-3">
            <h2 class="font-semibold text-gray-900 dark:text-white">{run.profile} profile</h2>
            <StatusBadge status={run.status} />
          </div>
          <p class="mt-1 font-mono text-xs text-gray-500 dark:text-gray-400">{run.id} · {formatTime(run.createdAt)}</p>
        </div>
        {#if !terminal}
          <div class="flex items-center gap-2 text-sm text-gray-500 dark:text-gray-400">
            <Spinner size="4" /> Waiting for checks
          </div>
        {/if}
      </div>

      <Table hoverable={true} shadow={false}>
        <TableHead>
          <TableHeadCell>Check</TableHeadCell>
          <TableHeadCell>Runtime</TableHeadCell>
          <TableHeadCell>Status</TableHeadCell>
          <TableHeadCell>MCP evidence</TableHeadCell>
          <TableHeadCell>Result</TableHeadCell>
          <TableHeadCell>Task</TableHeadCell>
        </TableHead>
        <TableBody>
          {#each run.checks as check (check.taskId)}
            <TableBodyRow>
              <TableBodyCell><span class="font-mono text-sm text-gray-900 dark:text-white">{check.name}</span></TableBodyCell>
              <TableBodyCell>
                <p class="text-sm text-gray-700 dark:text-gray-200">{check.harness || "default"}</p>
                <p class="font-mono text-xs text-gray-500 dark:text-gray-400">{check.providerId || "default"}/{check.modelId || "default"}</p>
              </TableBodyCell>
              <TableBodyCell><StatusBadge status={check.status} /></TableBodyCell>
              <TableBodyCell>
                <span class={check.evidence ? "text-green-600 dark:text-green-400" : "text-gray-400 dark:text-gray-500"}>
                  {check.evidence ? "Observed" : "Not yet observed"}
                </span>
              </TableBodyCell>
              <TableBodyCell class="max-w-md">
                {#if check.error}
                  <span class="text-sm text-red-600 dark:text-red-400">{check.error}</span>
                {:else}
                  <span class="block truncate text-sm text-gray-600 dark:text-gray-300">{check.summary || "—"}</span>
                {/if}
              </TableBodyCell>
              <TableBodyCell>
                <a href={resolve("/tasks/[id]", { id: check.taskId })} class="font-mono text-xs text-blue-600 hover:underline dark:text-blue-400">
                  {check.taskId.slice(0, 16)}…
                </a>
              </TableBodyCell>
            </TableBodyRow>
          {/each}
        </TableBody>
      </Table>
    </Card>
  {:else}
    <Card size="xl" class="!p-6 w-full" shadow="sm">
      <p class="text-sm text-gray-500 dark:text-gray-400">No self-test has been started in this browser session.</p>
    </Card>
  {/if}
</div>
