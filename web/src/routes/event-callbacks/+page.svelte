<script lang="ts">
  import { onMount } from "svelte";
  import { goto } from "$app/navigation";
  import { resolve } from "$app/paths";
  import { page as pageStore } from "$app/stores";
  import { createClient } from "@connectrpc/connect";
  import { EventCallbackService } from "$gen/proto/api/v1/api_pb";
  import type { EventCallback } from "$gen/proto/api/v1/api_pb";
  import { getTransport } from "$lib/api/client";
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

  let callbacks = $state<EventCallback[]>([]);
  let loading = $state(true);
  let error = $state<string | null>(null);

  // Filtering
  let showCreateTask = $state(initialBoolParam("create_task"));
  let showWebhook = $state(initialBoolParam("webhook"));
  let showSlack = $state(initialBoolParam("slack"));

  let filteredCallbacks = $derived(callbacks);

  let visibleCallbacks = $derived.by(() => {
    if (showCreateTask && showWebhook && showSlack) return filteredCallbacks;
    return filteredCallbacks.filter((cb) => {
      switch (cb.actionType) {
        case "create_task": return showCreateTask;
        case "webhook": return showWebhook;
        case "slack": return showSlack;
        default: return true;
      }
    });
  });

  let page = $state(initialNumberParam("page", 0));
  let pageSize = $state(initialNumberParam("size", 25));
  let totalPages = $derived(Math.max(1, Math.ceil(visibleCallbacks.length / pageSize)));
  let pagedCallbacks = $derived(visibleCallbacks.slice(page * pageSize, (page + 1) * pageSize));

  function syncURL() {
    const next = new URL(url);
    const s = (key: string, value: string, fallback = "") => value && value !== fallback ? next.searchParams.set(key, value) : next.searchParams.delete(key);
    s("create_task", showCreateTask ? "" : "0");
    s("webhook", showWebhook ? "" : "0");
    s("slack", showSlack ? "" : "0");
    s("page", String(page), "0");
    s("size", String(pageSize), "25");
    if (next.href !== url.href) goto(`${resolve("/event-callbacks")}${next.search}${next.hash}` as Parameters<typeof goto>[0], { replaceState: true, noScroll: true, keepFocus: true });
  }

  $effect(() => { showCreateTask; showWebhook; showSlack; page; pageSize; syncURL(); });

  function resetFilterPage() {
    page = 0;
  }

  // Create/edit form state
  let showForm = $state(false);
  let editingCallback = $state<EventCallback | null>(null);
  let saving = $state(false);
  let formName = $state("");
  let formEventType = $state("");
  let formActionType = $state("create_task");
  let formActionConfig = $state("");
  let formEnabled = $state(true);

  function openCreate() {
    editingCallback = null;
    formName = "";
    formEventType = "";
    formActionType = "create_task";
    formActionConfig = "";
    formEnabled = true;
    error = null;
    showForm = true;
  }

  function openEdit(cb: EventCallback) {
    editingCallback = cb;
    formName = cb.name;
    formEventType = cb.eventType;
    formActionType = cb.actionType;
    formActionConfig = cb.actionConfig || "";
    formEnabled = cb.enabled;
    error = null;
    showForm = true;
  }

  function cancelForm() {
    showForm = false;
    editingCallback = null;
    error = null;
  }

  async function load() {
    loading = true;
    error = null;
    try {
      const client = createClient(EventCallbackService, getTransport());
      const resp = await client.listEventCallbacks({});
      callbacks = resp.callbacks ?? [];
    } catch (e) {
      error = e instanceof Error ? e.message : "Failed to load event callbacks.";
    } finally {
      loading = false;
    }
  }

  onMount(load);

  async function toggleEnabled(cb: EventCallback) {
    error = null;
    try {
      const client = createClient(EventCallbackService, getTransport());
      await client.updateEventCallback({ name: cb.name, enabled: !cb.enabled });
      await load();
      addToast(`${cb.name} ${cb.enabled ? "disabled" : "enabled"}`, "success");
    } catch (e) {
      error = e instanceof Error ? e.message : "Failed to update event callback.";
      addToast(error, "error");
    }
  }

  async function deleteCallback(name: string) {
    const ok = await confirm({
      title: "Delete Event Callback",
      message: `Delete event callback "${name}"? This cannot be undone.`,
      confirmLabel: "Delete",
    });
    if (!ok) return;
    try {
      const client = createClient(EventCallbackService, getTransport());
      await client.deleteEventCallback({ name });
      addToast(`Event callback "${name}" deleted`, "success");
      if (page > 0 && pagedCallbacks.length <= 1) page--;
      await load();
    } catch (e) {
      error = e instanceof Error ? e.message : "Failed to delete event callback.";
      addToast(error, "error");
    }
  }

  async function saveCallback(e: Event) {
    e.preventDefault();
    error = null;
    if (!formName.trim()) {
      error = "Name is required.";
      return;
    }
    if (!formEventType.trim()) {
      error = "Event type is required.";
      return;
    }
    saving = true;
    try {
      const client = createClient(EventCallbackService, getTransport());
      if (editingCallback) {
        await client.updateEventCallback({
          name: formName.trim(),
          eventType: formEventType.trim() || undefined,
          actionType: formActionType || undefined,
          actionConfig: formActionConfig || undefined,
          enabled: formEnabled,
        });
        addToast(`Event callback "${formName.trim()}" updated`, "success");
      } else {
        await client.createEventCallback({
          name: formName.trim(),
          eventType: formEventType.trim(),
          actionType: formActionType,
          actionConfig: formActionConfig,
          enabled: formEnabled,
        });
        addToast(`Event callback "${formName.trim()}" created`, "success");
      }
      showForm = false;
      editingCallback = null;
      await load();
    } catch (e) {
      error = e instanceof Error ? e.message : "Failed to save event callback.";
      addToast(error, "error");
    } finally {
      saving = false;
    }
  }
</script>

<svelte:head>
  <title>Event Callbacks — Chetter</title>
</svelte:head>

<div class="p-6">
  <div class="flex flex-wrap items-center justify-between mb-6 gap-3">
    <h1 class="text-2xl font-bold text-gray-900 dark:text-white">Event Callbacks</h1>
    <div class="flex flex-wrap items-center gap-2">
      <div class="flex items-center gap-3 mr-2 border-r border-gray-300 dark:border-gray-600 pr-3">
        <Toggle bind:checked={showCreateTask} onchange={resetFilterPage} color="gray" size="small">Create Task</Toggle>
        <Toggle bind:checked={showWebhook} onchange={resetFilterPage} color="gray" size="small">Webhook</Toggle>
        <Toggle bind:checked={showSlack} onchange={resetFilterPage} color="gray" size="small">Slack</Toggle>
      </div>
      <Select bind:value={pageSize} onchange={() => { page = 0; }} class="!w-auto">
        <option value={10}>10 / page</option>
        <option value={25}>25 / page</option>
        <option value={50}>50 / page</option>
        <option value={100}>100 / page</option>
      </Select>
      <Button color="blue" onclick={() => { showForm ? cancelForm() : openCreate(); }}>
        {showForm ? "Cancel" : "Create Callback"}
      </Button>
    </div>
  </div>

  {#if error}
    <Alert color="red" class="mb-4">{error}</Alert>
  {/if}

  {#if showForm}
    <Card class="mb-6 w-full max-w-none !p-4" size="xl" shadow="sm">
    <form onsubmit={saveCallback} class="space-y-4">
      <div class="grid grid-cols-1 md:grid-cols-2 gap-3">
        <Input bind:value={formName} placeholder="Name" disabled={!!editingCallback} />
        <Select bind:value={formActionType}>
          <option value="create_task">Create Task</option>
          <option value="webhook">Webhook</option>
          <option value="slack">Slack</option>
        </Select>
      </div>
      <div class="grid grid-cols-1 md:grid-cols-2 gap-3">
        <Input bind:value={formEventType} placeholder="Event type, e.g. task.completed or task.failed.*" />
        <div class="flex items-center gap-2">
          <Toggle bind:checked={formEnabled} color="gray" size="small">Enabled</Toggle>
        </div>
      </div>
      <Textarea bind:value={formActionConfig} rows={4} placeholder={'Action config as JSON (e.g. {"prompt":"..."} for create_task, or {"url":"..."} for webhook/slack)'} class="w-full" />
      <Button type="submit" color="blue" disabled={saving}>
        {saving ? "Saving…" : editingCallback ? "Update" : "Create"}
      </Button>
    </form>
    </Card>
  {/if}

  {#if loading}
    <div class="flex items-center gap-2 text-gray-500 dark:text-gray-400"><Spinner size="4" /> Loading…</div>
  {:else}
    <TableCard title="Event Callbacks" subtitle="React to task lifecycle events with automated actions.">
    <Table hoverable={true} shadow={false}>
      <TableHead>
        <TableHeadCell>Name</TableHeadCell>
        <TableHeadCell>Event Type</TableHeadCell>
        <TableHeadCell>Action</TableHeadCell>
        <TableHeadCell>Enabled</TableHeadCell>
        <TableHeadCell>Updated</TableHeadCell>
        <TableHeadCell class="text-right">Actions</TableHeadCell>
      </TableHead>
      <TableBody>
        {#each pagedCallbacks as cb (cb.id)}
          <TableBodyRow>
            <TableBodyCell>
              <span class="font-medium text-gray-900 dark:text-white">{cb.name}</span>
            </TableBodyCell>
            <TableBodyCell><code class="text-sm text-gray-700 dark:text-gray-300">{cb.eventType}</code></TableBodyCell>
            <TableBodyCell><StatusBadge status={cb.actionType} /></TableBodyCell>
            <TableBodyCell>
              <Toggle checked={cb.enabled} onchange={() => toggleEnabled(cb)} color="gray" size="small" />
            </TableBodyCell>
            <TableBodyCell><span class="text-gray-500 dark:text-gray-400 whitespace-nowrap">{formatTime(cb.updatedAt)}</span></TableBodyCell>
            <TableBodyCell class="text-right">
              <div class="flex items-center justify-end gap-1">
                <Button color="blue" size="xs" onclick={() => openEdit(cb)} title="Edit">Edit</Button>
                <Button color="red" size="xs" onclick={() => deleteCallback(cb.name)} title="Delete">Del</Button>
              </div>
            </TableBodyCell>
          </TableBodyRow>
        {:else}
          <TableBodyRow>
            <TableBodyCell colspan={6}>
              <div class="text-center text-gray-500 dark:text-gray-400 py-8">No event callbacks found</div>
            </TableBodyCell>
          </TableBodyRow>
        {/each}
      </TableBody>
    </Table>
    </TableCard>

    <div class="flex items-center justify-between mt-4 text-sm text-gray-500 dark:text-gray-400">
      <span>Showing {visibleCallbacks.length > 0 ? page * pageSize + 1 : 0}–{Math.min((page + 1) * pageSize, visibleCallbacks.length)} of {visibleCallbacks.length}</span>
      <PaginationNav
        currentPage={page + 1}
        {totalPages}
        visiblePages={5}
        onPageChange={(nextPage) => { page = nextPage - 1; }}
      />
    </div>
  {/if}
</div>
