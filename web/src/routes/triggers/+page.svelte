<script lang="ts">
  import { onMount } from "svelte";
  import { resolve } from "$app/paths";
  import { createClient } from "@connectrpc/connect";
  import { TriggerService } from "$gen/proto/api/v1/api_pb";
  import type { TriggerRun, Trigger } from "$gen/proto/api/v1/api_pb";
  import { getTransport } from "$lib/api/client";
  import { formatTime } from "$lib/utils.svelte";
  import { addToast } from "$lib/stores/toast.svelte";
  import { confirm } from "$lib/stores/confirm.svelte";
  import StatusBadge from "$lib/components/StatusBadge.svelte";
  import { Alert, Badge, Button, Card, Input, Select, Spinner, Table, TableHead, TableBody, TableHeadCell, TableBodyRow, TableBodyCell, Textarea, Toggle } from "flowbite-svelte";

  let triggers = $state<Trigger[]>([]);
  let expandedId = $state<string | null>(null);
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
  let agentImage = $state("");
  let agent = $state("");
  let modelId = $state("");

  let triggerRuns = $state<TriggerRun[]>([]);
  let runsTriggerName = $state<string | null>(null);
  let loadingRuns = $state(false);
  let runsPage = $state(0);
  let runsPageSize = 10;
  let totalRunsPages = $derived(Math.max(1, Math.ceil(triggerRuns.length / runsPageSize)));
  let pagedRuns = $derived(triggerRuns.slice(runsPage * runsPageSize, (runsPage + 1) * runsPageSize));

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
      addToast(`${trigger.name} ${trigger.enabled ? "disabled" : "enabled"}`, "success");
    } catch (e) {
      actionError = e instanceof Error ? e.message : "Failed to update trigger.";
      addToast(actionError, "error");
      console.error(e);
    }
  }

  async function runNow(name: string) {
    const ok = await confirm({
      title: "Run Trigger",
      message: `Run trigger "${name}" now?`,
      confirmLabel: "Run",
    });
    if (!ok) return;
    actionError = null;
    try {
      const client = createClient(TriggerService, getTransport());
      await client.runTrigger({ name });
      addToast(`Trigger "${name}" started`, "success");
      if (runsTriggerName === name) await loadRuns(name);
    } catch (e) {
      actionError = e instanceof Error ? e.message : "Failed to run trigger.";
      addToast(actionError, "error");
      console.error(e);
    }
  }

  async function loadRuns(name: string) {
    if (runsTriggerName === name) {
      runsTriggerName = null;
      return;
    }
    runsTriggerName = name;
    loadingRuns = true;
    actionError = null;
    runsPage = 0;
    try {
      const client = createClient(TriggerService, getTransport());
      const resp = await client.listTriggerRuns({ triggerName: name, limit: 25 });
      triggerRuns = resp.runs ?? [];
    } catch (e) {
      actionError = e instanceof Error ? e.message : "Failed to load trigger runs.";
      addToast(actionError, "error");
      console.error(e);
    } finally {
      loadingRuns = false;
    }
  }

  async function deleteTrigger(name: string) {
    const ok = await confirm({
      title: "Delete Trigger",
      message: `Delete trigger "${name}"? This cannot be undone.`,
      confirmLabel: "Delete",
    });
    if (!ok) return;
    actionError = null;
    try {
      const client = createClient(TriggerService, getTransport());
      await client.deleteTrigger({ name });
      addToast(`Trigger "${name}" deleted`, "success");
      if (expandedId) expandedId = null;
      await load();
    } catch (e) {
      actionError = e instanceof Error ? e.message : "Failed to delete trigger.";
      addToast(actionError, "error");
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
    if (triggerType === "cron" && !prompt.trim()) {
      actionError = "Prompt is required for cron triggers.";
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
        agentImage: agentImage.trim(),
        agent: agent.trim(),
        modelId: modelId.trim(),
      });
      addToast(`Trigger "${name.trim()}" created`, "success");
      name = "";
      triggerType = "cron";
      cronExpr = "@hourly";
      repo = "";
      event = "";
      prompt = "";
      gitUrl = "";
      gitRef = "";
      agentImage = "";
      agent = "";
      modelId = "";
      showCreateForm = false;
      await load();
    } catch (err) {
      actionError = err instanceof Error ? err.message : "Failed to create trigger.";
      addToast(actionError, "error");
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

  function toggleExpand(id: string) {
    expandedId = expandedId === id ? null : id;
  }

  function running(trigger: Trigger) {
    return runsTriggerName === trigger.name;
  }

  function isGitManaged(trigger: Trigger): boolean {
    return !!trigger.sourceId;
  }
</script>

<svelte:head>
  <title>Triggers — Chetter</title>
</svelte:head>

<div class="p-6">
  <div class="flex items-center justify-between mb-6">
    <h1 class="text-2xl font-bold text-gray-900 dark:text-white">Triggers</h1>
    <Button color="blue" onclick={() => { showCreateForm = !showCreateForm; actionError = null; }}>
      {showCreateForm ? "Cancel" : "Create Trigger"}
    </Button>
  </div>

  {#if actionError}
    <Alert color="red" class="mb-4">{actionError}</Alert>
  {/if}

  {#if showCreateForm}
    <Card class="mb-6 w-full !p-4" shadow="sm">
    <form onsubmit={createTrigger} class="space-y-4">
      <div class="grid grid-cols-1 md:grid-cols-3 gap-3">
        <Input bind:value={name} placeholder="Name" />
        <Select bind:value={triggerType}>
          <option value="cron">Cron</option>
          <option value="pr_review">PR Review</option>
          <option value="issue">Issue</option>
        </Select>
        <Input bind:value={cronExpr} placeholder="Cron expression" disabled={triggerType !== "cron"} />
      </div>
      <div class="grid grid-cols-1 md:grid-cols-2 gap-3">
        <Input bind:value={repo} placeholder="Repository, e.g. org/repo" />
        <Input bind:value={event} placeholder="Event (optional)" />
        <Input bind:value={gitUrl} placeholder="Git URL (optional)" />
        <Input bind:value={gitRef} placeholder="Git ref (optional)" />
        <Input bind:value={agentImage} placeholder="Agent image override (optional)" />
        <Input bind:value={agent} placeholder="Agent (optional)" />
        <Input bind:value={modelId} placeholder="Model ID (optional)" />
      </div>
      <Textarea bind:value={prompt} rows={3} placeholder="Prompt override (optional)" class="w-full" />
      <Button type="submit" color="blue" disabled={creating}>
        {creating ? "Creating…" : "Create"}
      </Button>
    </form>
    </Card>
  {/if}

  {#if loading}
    <div class="flex items-center gap-2 text-gray-500 dark:text-gray-400">
      <Spinner size="4" /> Loading…
    </div>
  {:else}
    <div class="space-y-2">
      {#each triggers as trigger (trigger.id)}
        <Card shadow="sm" class="w-full max-w-none !p-0 overflow-hidden">
          <div
            class="w-full px-4 py-3 flex items-center justify-between hover:bg-gray-50 dark:hover:bg-gray-700/50 cursor-pointer"
            onclick={() => toggleExpand(trigger.id)}
            role="button"
            tabindex="0"
            onkeydown={(e) => { if (e.key === "Enter" || e.key === " ") toggleExpand(trigger.id); }}
          >
            <div class="flex items-center gap-4 min-w-0">
              <span class="text-sm font-medium text-gray-900 dark:text-white truncate">{trigger.name}</span>
              <StatusBadge status={trigger.triggerType} />
              <span class="text-xs text-gray-500 dark:text-gray-400 truncate hidden sm:block">{triggerTarget(trigger)}</span>
              {#if isGitManaged(trigger)}
                <Badge color="gray">git-managed</Badge>
              {/if}
            </div>
            <div class="flex items-center gap-3 shrink-0 ml-4">
              <Toggle
                checked={trigger.enabled}
                onchange={() => toggleEnabled(trigger)}
              />
              <Button color="blue" size="xs" onclick={(ev: MouseEvent) => { ev.stopPropagation(); runNow(trigger.name); }}>Run</Button>
              <Button color="red" size="xs" disabled={isGitManaged(trigger)} onclick={(ev: MouseEvent) => { ev.stopPropagation(); deleteTrigger(trigger.name); }}>Delete</Button>
              <span class="text-gray-400 transition-transform {expandedId === trigger.id ? 'rotate-180' : ''}">▼</span>
            </div>
          </div>

          {#if expandedId === trigger.id}
            <div class="px-4 py-4 border-t border-gray-200 dark:border-gray-700 bg-gray-50/50 dark:bg-gray-800/50 space-y-3">
              <div class="grid grid-cols-1 sm:grid-cols-2 lg:grid-cols-3 gap-x-6 gap-y-2 text-sm">
                <div>
                  <span class="text-xs text-gray-400 dark:text-gray-500">Status</span>
                  <p class="text-gray-900 dark:text-white">
                    <StatusBadge status={trigger.enabled ? "enabled" : "disabled"} />
                  </p>
                </div>
                <div>
                  <span class="text-xs text-gray-400 dark:text-gray-500">Type</span>
                  <p class="text-gray-900 dark:text-white">{trigger.triggerType}</p>
                </div>
                {#if trigger.cronExpr}
                  <div>
                    <span class="text-xs text-gray-400 dark:text-gray-500">Cron</span>
                    <p class="text-gray-900 dark:text-white font-mono">{trigger.cronExpr}</p>
                  </div>
                {/if}
                {#if trigger.triggerConfig}
                  {@const parsed = (() => { try { return JSON.parse(trigger.triggerConfig); } catch { return {}; } })()}
                  {#if parsed.repo}
                    <div>
                      <span class="text-xs text-gray-400 dark:text-gray-500">Repository</span>
                      <p class="text-gray-900 dark:text-white">{parsed.repo}</p>
                    </div>
                  {/if}
                  {#if parsed.event}
                    <div>
                      <span class="text-xs text-gray-400 dark:text-gray-500">Event</span>
                      <p class="text-gray-900 dark:text-white">{parsed.event}</p>
                    </div>
                  {/if}
                  {#if parsed.session_mode}
                    <div>
                      <span class="text-xs text-gray-400 dark:text-gray-500">Session Mode</span>
                      <p class="text-gray-900 dark:text-white">{parsed.session_mode}</p>
                    </div>
                  {/if}
                  {#if parsed.ttl_hours}
                    <div>
                      <span class="text-xs text-gray-400 dark:text-gray-500">TTL</span>
                      <p class="text-gray-900 dark:text-white">{parsed.ttl_hours}h</p>
                    </div>
                  {/if}
                {/if}
                {#if trigger.agent}
                  <div>
                    <span class="text-xs text-gray-400 dark:text-gray-500">Agent</span>
                    <p class="text-gray-900 dark:text-white">{trigger.agent}</p>
                  </div>
                {/if}
                {#if trigger.modelId}
                  <div>
                    <span class="text-xs text-gray-400 dark:text-gray-500">Model</span>
                    <p class="text-gray-900 dark:text-white">{trigger.modelId}</p>
                  </div>
                {/if}
                {#if trigger.providerId}
                  <div>
                    <span class="text-xs text-gray-400 dark:text-gray-500">Provider</span>
                    <p class="text-gray-900 dark:text-white">{trigger.providerId}</p>
                  </div>
                {/if}
                {#if trigger.harness}
                  <div>
                    <span class="text-xs text-gray-400 dark:text-gray-500">Harness</span>
                    <p class="text-gray-900 dark:text-white">{trigger.harness}</p>
                  </div>
                {/if}
                {#if trigger.variantId}
                  <div>
                    <span class="text-xs text-gray-400 dark:text-gray-500">Variant</span>
                    <p class="text-gray-900 dark:text-white">{trigger.variantId}</p>
                  </div>
                {/if}
                {#if trigger.skills && trigger.skills.length > 0}
                  <div>
                    <span class="text-xs text-gray-400 dark:text-gray-500">Skills</span>
                    <p class="text-gray-900 dark:text-white">{trigger.skills.join(", ")}</p>
                  </div>
                {/if}
                {#if trigger.gitUrl}
                  <div class="sm:col-span-2">
                    <span class="text-xs text-gray-400 dark:text-gray-500">Git URL</span>
                    <p class="text-gray-900 dark:text-white font-mono">{trigger.gitUrl}</p>
                  </div>
                {/if}
                {#if trigger.gitRef}
                  <div>
                    <span class="text-xs text-gray-400 dark:text-gray-500">Git Ref</span>
                    <p class="text-gray-900 dark:text-white font-mono">{trigger.gitRef}</p>
                  </div>
                {/if}
                {#if trigger.agentImage}
                  <div>
                    <span class="text-xs text-gray-400 dark:text-gray-500">Agent Image</span>
                    <p class="text-gray-900 dark:text-white font-mono text-xs">{trigger.agentImage}</p>
                  </div>
                {/if}
                {#if trigger.timeoutSec > 0}
                  <div>
                    <span class="text-xs text-gray-400 dark:text-gray-500">Timeout</span>
                    <p class="text-gray-900 dark:text-white">{trigger.timeoutSec}s</p>
                  </div>
                {/if}
                {#if trigger.lastRunAt}
                  <div>
                    <span class="text-xs text-gray-400 dark:text-gray-500">Last Run</span>
                    <p class="text-gray-900 dark:text-white">{formatTime(trigger.lastRunAt)}</p>
                  </div>
                {/if}
                {#if trigger.nextRunAt}
                  <div>
                    <span class="text-xs text-gray-400 dark:text-gray-500">Next Run</span>
                    <p class="text-gray-900 dark:text-white">{formatTime(trigger.nextRunAt)}</p>
                  </div>
                {/if}
                {#if trigger.sourceId}
                  <div>
                    <span class="text-xs text-gray-400 dark:text-gray-500">Source</span>
                    <p class="text-gray-900 dark:text-white font-mono text-xs">{trigger.sourceId}</p>
                  </div>
                {/if}
              </div>

              {#if trigger.prompt}
                <div>
                  <span class="text-xs text-gray-400 dark:text-gray-500">Prompt</span>
                  <pre class="mt-1 text-sm text-gray-900 dark:text-white whitespace-pre-wrap bg-white dark:bg-gray-700 p-2 rounded border border-gray-200 dark:border-gray-600">{trigger.prompt}</pre>
                </div>
              {/if}

              <div class="flex items-center gap-2 pt-2 border-t border-gray-200 dark:border-gray-700">
                <Button color={running(trigger) ? "blue" : "alternative"} size="xs" onclick={() => loadRuns(trigger.name)}>
                  {running(trigger) ? "Hide Runs" : "Show Runs"}
                </Button>
                <Button color="blue" size="xs" onclick={() => runNow(trigger.name)}>Run Now</Button>
                <Button color="red" size="xs" disabled={isGitManaged(trigger)} onclick={() => deleteTrigger(trigger.name)}>Delete</Button>
              </div>

              {#if running(trigger)}
                <Card shadow="sm" class="mt-3 w-full max-w-none !p-0 overflow-hidden">
                  <div class="px-3 py-2 border-b border-gray-200 dark:border-gray-700">
                    <h3 class="text-sm font-semibold text-gray-900 dark:text-white">Recent Runs</h3>
                  </div>
                  {#if loadingRuns}
                    <div class="flex items-center gap-2 px-3 py-6 text-gray-500 dark:text-gray-400">
                      <Spinner size="4" /> Loading runs…
                    </div>
                  {:else if triggerRuns.length === 0}
                    <p class="px-3 py-6 text-center text-sm text-gray-500 dark:text-gray-400">No runs found</p>
                  {:else}
                    <div class="chetter-table overflow-x-auto">
                    <Table hoverable={true} shadow={false}>
                      <TableHead>
                        <TableHeadCell>Run ID</TableHeadCell>
                        <TableHeadCell>Task</TableHeadCell>
                        <TableHeadCell>Status</TableHeadCell>
                        <TableHeadCell>Triggered</TableHeadCell>
                        <TableHeadCell>Created</TableHeadCell>
                      </TableHead>
                      <TableBody>
                        {#each pagedRuns as run (run.id)}
                          <TableBodyRow>
                            <TableBodyCell class="font-mono text-xs">{run.id.slice(0, 16)}…</TableBodyCell>
                            <TableBodyCell>
                              <a href={resolve("/tasks/[id]", { id: run.taskId })} class="font-mono text-xs text-blue-600 dark:text-blue-400 hover:underline">
                                {run.taskId.slice(0, 16)}…
                              </a>
                            </TableBodyCell>
                            <TableBodyCell><StatusBadge status={run.status} /></TableBodyCell>
                            <TableBodyCell class="text-xs text-gray-500 dark:text-gray-400">{formatTime(run.triggeredAt)}</TableBodyCell>
                            <TableBodyCell class="text-xs text-gray-500 dark:text-gray-400">{formatTime(run.createdAt)}</TableBodyCell>
                          </TableBodyRow>
                        {/each}
                      </TableBody>
                    </Table>
                    </div>
                    <div class="flex items-center justify-between px-3 py-2 text-xs text-gray-500 dark:text-gray-400 border-t border-gray-200 dark:border-gray-700">
                      <span>{triggerRuns.length > 0 ? runsPage * runsPageSize + 1 : 0}–{Math.min((runsPage + 1) * runsPageSize, triggerRuns.length)} of {triggerRuns.length}</span>
                      <div class="flex gap-1">
                        <Button size="xs" color="alternative" disabled={runsPage === 0} onclick={() => { runsPage = Math.max(0, runsPage - 1); }}>←</Button>
                        {#each { length: Math.min(totalRunsPages, 7) } as _, i}
                          {@const p = totalRunsPages <= 7 ? i : runsPage < 4 ? i : runsPage > totalRunsPages - 4 ? totalRunsPages - 7 + i : runsPage - 3 + i}
                          <Button size="xs" color={p === runsPage ? "blue" : "alternative"} onclick={() => { runsPage = p; }}>{p + 1}</Button>
                        {/each}
                        <Button size="xs" color="alternative" disabled={runsPage >= totalRunsPages - 1} onclick={() => { runsPage = Math.min(totalRunsPages - 1, runsPage + 1); }}>→</Button>
                      </div>
                    </div>
                  {/if}
                </Card>
              {/if}
            </div>
          {/if}
        </Card>
      {:else}
        <Card shadow="sm" class="w-full max-w-none !p-8 text-center">
          No triggers found
        </Card>
      {/each}
    </div>
  {/if}
</div>
