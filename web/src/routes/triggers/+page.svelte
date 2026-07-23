<script lang="ts">
  import { onMount } from "svelte";
  import { goto } from "$app/navigation";
  import { resolve } from "$app/paths";
  import { page as pageStore } from "$app/stores";
  import { createClient } from "@connectrpc/connect";
  import { TriggerService } from "$gen/proto/api/v1/api_pb";
  import type { Trigger } from "$gen/proto/api/v1/api_pb";
  import { getTransport } from "$lib/api/client";
  import { effectiveTeamIDs, effectiveRepos } from "$lib/stores/filter.svelte";
  import { formatTime } from "$lib/utils.svelte";
  import { addToast } from "$lib/stores/toast.svelte";
  import { confirm } from "$lib/stores/confirm.svelte";
  import StatusBadge from "$lib/components/StatusBadge.svelte";
  import TableCard from "$lib/components/TableCard.svelte";
  import { Alert, Badge, Button, Card, Input, PaginationNav, Select, Spinner, Table, TableHead, TableHeadCell, TableBody, TableBodyRow, TableBodyCell, Textarea, Toggle } from "flowbite-svelte";

  const initialUrl = new URL($pageStore.url);
  const url = $derived($pageStore.url);

  function initialNumberParam(name: string, fallback: number): number {
    return Number(initialUrl.searchParams.get(name)) || fallback;
  }

  function initialBoolParam(name: string, fallback = true): boolean {
    const value = initialUrl.searchParams.get(name);
    if (value === null) return fallback;
    return value === "1";
  }

  let triggers = $state<Trigger[]>([]);
  let loading = $state(true);
  let error = $state<string | null>(null);

  let showCron = $state(initialBoolParam("cron"));
  let showIssue = $state(initialBoolParam("issue"));
  let showPrReview = $state(initialBoolParam("pr_review"));

  let filteredTriggers = $derived(triggers);

  let visibleTriggers = $derived.by(() => {
    if (showCron && showIssue && showPrReview) return filteredTriggers;
    return filteredTriggers.filter((t) => {
      switch (t.triggerType) {
        case "cron": return showCron;
        case "issue": return showIssue;
        case "pr_review": return showPrReview;
        default: return true;
      }
    });
  });

  let page = $state(initialNumberParam("page", 0));
  let pageSize = $state(initialNumberParam("size", 25));
  let totalPages = $derived(Math.max(1, Math.ceil(visibleTriggers.length / pageSize)));
  let pagedTriggers = $derived(visibleTriggers.slice(page * pageSize, (page + 1) * pageSize));

  function syncURL() {
    const next = new URL(url);
    const s = (key: string, value: string, fallback = "") => value && value !== fallback ? next.searchParams.set(key, value) : next.searchParams.delete(key);
    s("cron", showCron ? "" : "0");
    s("issue", showIssue ? "" : "0");
    s("pr_review", showPrReview ? "" : "0");
    s("page", String(page), "0");
    s("size", String(pageSize), "25");
    if (next.href !== url.href) goto(`${resolve("/triggers")}${next.search}${next.hash}` as Parameters<typeof goto>[0], { replaceState: true, noScroll: true, keepFocus: true });
  }

  $effect(() => { showCron; showIssue; showPrReview; page; pageSize; syncURL(); });

  function resetFilterPage() {
    page = 0;
  }

  let showCreateForm = $state(false);
  let creating = $state(false);
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

  function triggerTarget(trigger: Trigger): string {
    if (trigger.cronExpr) return trigger.cronExpr;
    try { return JSON.parse(trigger.triggerConfig || "{}").repo || "—"; }
    catch { return "—"; }
  }

  function isGitManaged(trigger: Trigger): boolean {
    return !!trigger.sourceId;
  }

  function sourceFileUrl(trigger: Trigger): string | null {
    if (!trigger.sourceRepoUrl || !trigger.sourcePath) return null;
    const branch = trigger.sourceBranch || "main";
    const base = trigger.sourceRepoUrl.replace(/\.git$/, "").replace(/\/$/, "");
    return `${base}/blob/${branch}/${trigger.sourcePath}`;
  }

  async function load() {
    loading = true;
    error = null;
    try {
      const client = createClient(TriggerService, getTransport());
      const resp = await client.listTriggers({
        ...(effectiveTeamIDs().length > 0 ? { teamIds: effectiveTeamIDs() } : {}),
        ...(effectiveRepos().length > 0 ? { repos: effectiveRepos() } : {}),
      });
      triggers = resp.triggers ?? [];
    } catch (e) {
      error = e instanceof Error ? e.message : "Failed to load triggers.";
    } finally {
      loading = false;
    }
  }

  onMount(load);

  async function toggleEnabled(trigger: Trigger) {
    error = null;
    try {
      const client = createClient(TriggerService, getTransport());
      await client.updateTrigger({ name: trigger.name, enabled: !trigger.enabled });
      await load();
      addToast(`${trigger.name} ${trigger.enabled ? "disabled" : "enabled"}`, "success");
    } catch (e) {
      error = e instanceof Error ? e.message : "Failed to update trigger.";
      addToast(error, "error");
    }
  }

  async function runNow(name: string) {
    const ok = await confirm({
      title: "Run Trigger",
      message: `Run trigger "${name}" now?`,
      confirmLabel: "Run",
    });
    if (!ok) return;
    error = null;
    try {
      const client = createClient(TriggerService, getTransport());
      await client.runTrigger({ name });
      addToast(`Trigger "${name}" started`, "success");
    } catch (e) {
      error = e instanceof Error ? e.message : "Failed to run trigger.";
      addToast(error, "error");
    }
  }

  async function deleteTrigger(name: string) {
    const ok = await confirm({
      title: "Delete Trigger",
      message: `Delete trigger "${name}"? This cannot be undone.`,
      confirmLabel: "Delete",
    });
    if (!ok) return;
    try {
      const client = createClient(TriggerService, getTransport());
      await client.deleteTrigger({ name });
      addToast(`Trigger "${name}" deleted`, "success");
      if (page > 0 && pagedTriggers.length <= 1) page--;
      await load();
    } catch (e) {
      error = e instanceof Error ? e.message : "Failed to delete trigger.";
      addToast(error, "error");
    }
  }

  async function createTrigger(e: Event) {
    e.preventDefault();
    error = null;
    if (!name.trim()) {
      error = "Name is required.";
      return;
    }
    if (triggerType === "cron" && !cronExpr.trim()) {
      error = "Cron expression is required for cron triggers.";
      return;
    }
    if (triggerType !== "cron" && !repo.trim()) {
      error = "Repository is required for webhook triggers.";
      return;
    }
    if (triggerType === "cron" && !prompt.trim()) {
      error = "Prompt is required for cron triggers.";
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
      error = err instanceof Error ? err.message : "Failed to create trigger.";
      addToast(error, "error");
    } finally {
      creating = false;
    }
  }
</script>

<svelte:head>
  <title>Triggers — Chetter</title>
</svelte:head>

<div class="p-6">
  <div class="flex flex-wrap items-center justify-between mb-6 gap-3">
    <h1 class="text-2xl font-bold text-gray-900 dark:text-white">Triggers</h1>
    <div class="flex flex-wrap items-center gap-2">
      <div class="flex items-center gap-3 mr-2 border-r border-gray-300 dark:border-gray-600 pr-3">
        <Toggle bind:checked={showCron} onchange={resetFilterPage} color="gray" size="small">Cron</Toggle>
        <Toggle bind:checked={showIssue} onchange={resetFilterPage} color="gray" size="small">Issue</Toggle>
        <Toggle bind:checked={showPrReview} onchange={resetFilterPage} color="gray" size="small">PR Review</Toggle>
      </div>
      <Select bind:value={pageSize} onchange={() => { page = 0; }} class="!w-auto">
        <option value={10}>10 / page</option>
        <option value={25}>25 / page</option>
        <option value={50}>50 / page</option>
        <option value={100}>100 / page</option>
      </Select>
      <Button color="blue" onclick={() => { showCreateForm = !showCreateForm; error = null; }}>
        {showCreateForm ? "Cancel" : "Create Trigger"}
      </Button>
    </div>
  </div>

  {#if error}
    <Alert color="red" class="mb-4">{error}</Alert>
  {/if}

  {#if showCreateForm}
    <Card class="mb-6 w-full max-w-none !p-4" size="xl" shadow="sm">
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
    <div class="flex items-center gap-2 text-gray-500 dark:text-gray-400"><Spinner size="4" /> Loading…</div>
  {:else}
    <TableCard title="Triggers" subtitle="Automated task triggers — cron schedules and GitHub webhook handlers.">
    <Table hoverable={true} shadow={false}>
      <TableHead>
        <TableHeadCell>Name</TableHeadCell>
        <TableHeadCell>Type</TableHeadCell>
        <TableHeadCell>Enabled</TableHeadCell>
        <TableHeadCell>Target</TableHeadCell>
        <TableHeadCell>Agent</TableHeadCell>
        <TableHeadCell>Last Run</TableHeadCell>
        <TableHeadCell class="text-right">Actions</TableHeadCell>
      </TableHead>
      <TableBody>
        {#each pagedTriggers as trigger (trigger.id)}
          <TableBodyRow>
            <TableBodyCell>
              <a href={resolve("/triggers/[name]", { name: trigger.name })} class="font-medium text-blue-600 dark:text-blue-400 hover:underline">
                {trigger.name}
              </a>
              {#if isGitManaged(trigger)}
                {#if sourceFileUrl(trigger)}
                  <a href={sourceFileUrl(trigger)} target="_blank" rel="noopener noreferrer" class="ml-1">
                    <Badge color="gray">git</Badge>
                  </a>
                {:else}
                  <Badge color="gray" class="ml-1">git</Badge>
                {/if}
              {/if}
            </TableBodyCell>
            <TableBodyCell><StatusBadge status={trigger.triggerType} /></TableBodyCell>
            <TableBodyCell>
              <Toggle checked={trigger.enabled} onchange={() => toggleEnabled(trigger)} color="gray" size="small" disabled={isGitManaged(trigger)} />
            </TableBodyCell>
            <TableBodyCell><span class="text-gray-700 dark:text-gray-300 font-mono text-sm">{triggerTarget(trigger)}</span></TableBodyCell>
             <TableBodyCell>
               {#if trigger.agent}
                 <a href={resolve("/agents/[name]", { name: trigger.agent })} class="text-blue-600 dark:text-blue-400 hover:underline">{trigger.agent}</a>
               {:else}
                 <span class="text-gray-700 dark:text-gray-300">—</span>
               {/if}
             </TableBodyCell>
            <TableBodyCell><span class="text-gray-500 dark:text-gray-400 whitespace-nowrap">{trigger.lastRunAt ? formatTime(trigger.lastRunAt) : "—"}</span></TableBodyCell>
            <TableBodyCell class="text-right">
              <div class="flex items-center justify-end gap-1">
                <Button color="blue" size="xs" onclick={() => runNow(trigger.name)} title="Run now">Run</Button>
                <Button color="red" size="xs" onclick={() => deleteTrigger(trigger.name)} disabled={isGitManaged(trigger)} title="Delete">Del</Button>
              </div>
            </TableBodyCell>
          </TableBodyRow>
        {:else}
          <TableBodyRow>
            <TableBodyCell colspan={7}>
              <div class="text-center text-gray-500 dark:text-gray-400 py-8">No triggers found</div>
            </TableBodyCell>
          </TableBodyRow>
        {/each}
      </TableBody>
    </Table>
    </TableCard>

    <div class="flex items-center justify-between mt-4 text-sm text-gray-500 dark:text-gray-400">
      <span>Showing {visibleTriggers.length > 0 ? page * pageSize + 1 : 0}–{Math.min((page + 1) * pageSize, visibleTriggers.length)} of {visibleTriggers.length}</span>
      <PaginationNav
        currentPage={page + 1}
        {totalPages}
        visiblePages={5}
        onPageChange={(nextPage) => { page = nextPage - 1; }}
      />
    </div>
  {/if}
</div>
