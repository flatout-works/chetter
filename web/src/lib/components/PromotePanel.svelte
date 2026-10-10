<script lang="ts">
  /**
   * PromotePanel turns a database draft trigger into a Git-managed definition.
   *
   * A draft (sourceId unset) runs from the Chetter database only; nothing in
   * the definitions repository describes it. Promoting it renders the trigger
   * as canonical definition YAML and opens a definition proposal pull request.
   * Merging that PR makes the next definitions sync take ownership of the
   * existing trigger row, so its id and run history survive.
   *
   * The panel never renders for a Git-managed trigger: Git is already
   * authoritative for those, so promotion would be meaningless.
   */
  import { createClient } from "@connectrpc/connect";
  import { TriggerService } from "$gen/proto/api/v1/api_pb";
  import type { Trigger } from "$gen/proto/api/v1/api_pb";
  import { getTransport } from "$lib/api/client";
  import { addToast } from "$lib/stores/toast.svelte";
  import { Alert, Badge, Button, Input, Modal, Select, Spinner } from "flowbite-svelte";

  let { trigger, onPromoted }: { trigger: Trigger; onPromoted?: () => void } = $props();

  let scope = $state<"global" | "team" | "repo">("global");
  let teamName = $state("");
  let targetRepo = $state("");
  let preview = $state<string | null>(null);
  let previewPath = $state<string | null>(null);
  let previewWarnings = $state<string[]>([]);
  let previewOpen = $state(false);
  let confirmOpen = $state(false);
  let busy = $state(false);
  let failure = $state<string | null>(null);
  let promotedUrl = $state<string | null>(null);
  let promotedNumber = $state<number | null>(null);

  const isDraft = $derived(!trigger.sourceId);

  function requestOptions(dryRun: boolean) {
    return {
      name: trigger.name,
      scope,
      dryRun,
      ...(scope === "team" ? { teamName } : {}),
      ...(scope === "repo" ? { targetRepo } : {}),
    };
  }

  async function runPromote(dryRun: boolean) {
    busy = true;
    failure = null;
    try {
      const client = createClient(TriggerService, getTransport());
      const resp = await client.promoteTrigger(requestOptions(dryRun));
      if (dryRun) {
        preview = resp.content;
        previewPath = resp.path;
        previewWarnings = resp.warnings ?? [];
        previewOpen = true;
        return;
      }
      promotedNumber = resp.prNumber ?? null;
      promotedUrl = resp.prUrl ?? null;
      preview = null;
      confirmOpen = false;
      addToast(`Promotion PR opened for ${trigger.name}`, "success");
      onPromoted?.();
    } catch (e) {
      failure = e instanceof Error ? e.message : "Promotion failed.";
    } finally {
      busy = false;
    }
  }
</script>

{#if isDraft}
  <section
    class="w-full rounded-lg border border-amber-300 dark:border-amber-700 bg-amber-50/50 dark:bg-amber-900/10 p-4 mb-6"
    data-testid="promote-panel"
  >
    <div class="flex items-center gap-2 mb-1">
      <h2 class="text-sm font-semibold text-gray-900 dark:text-white">Promote to Git</h2>
      <Badge color="amber">draft</Badge>
    </div>

    {#if promotedUrl}
      <p class="text-sm text-gray-700 dark:text-gray-300 mb-2">
        Promotion PR
        {#if promotedNumber}#{promotedNumber}{/if}
        opened. This trigger keeps running on its schedule until the PR merges.
      </p>
      <div class="flex items-center gap-2">
        <a href={promotedUrl} target="_blank" rel="noopener noreferrer">
          <Button color="blue" size="sm">View PR ↗</Button>
        </a>
      </div>
    {:else}
      <p class="text-sm text-gray-700 dark:text-gray-300 mb-3">
        This trigger runs from the Chetter database only. Promoting it adds a definition file and
        opens a pull request; merging that keeps this trigger's id and run history.
      </p>

      {#if failure}
        <Alert color="red" class="mb-3 whitespace-pre-wrap">{failure}</Alert>
      {/if}

      <div class="grid grid-cols-1 sm:grid-cols-3 gap-3 mb-3">
        <div>
          <span class="text-xs text-gray-500 dark:text-gray-400">Scope</span>
          <Select bind:value={scope} size="sm">
            <option value="global">Global</option>
            <option value="repo">Repository</option>
            <option value="team">Team</option>
          </Select>
        </div>
        {#if scope === "repo"}
          <div>
            <span class="text-xs text-gray-500 dark:text-gray-400">Repository</span>
            <Input bind:value={targetRepo} size="sm" placeholder="owner/repo" />
          </div>
        {:else if scope === "team"}
          <div>
            <span class="text-xs text-gray-500 dark:text-gray-400">Team</span>
            <Input bind:value={teamName} size="sm" placeholder="team-name" />
          </div>
        {/if}
      </div>

      <div class="flex items-center gap-2">
        <Button color="light" size="sm" onclick={() => runPromote(true)} disabled={busy}>
          {busy ? "Working…" : "Preview YAML"}
        </Button>
        <Button color="blue" size="sm" onclick={() => (confirmOpen = true)} disabled={busy}>
          Promote…
        </Button>
        {#if busy}<Spinner size="4" />{/if}
      </div>
    {/if}
  </section>
{/if}

<Modal bind:open={previewOpen} size="xl" title="Definition preview" autoclose>
  <p class="text-sm text-gray-700 dark:text-gray-300 mb-2">
    Proposed file: <code class="font-mono">{previewPath}</code>
  </p>
  {#if previewWarnings.length > 0}
    <Alert color="yellow" class="mb-3">{previewWarnings.join(", ")}</Alert>
  {/if}
  <pre
    class="max-h-96 overflow-auto rounded bg-gray-50 dark:bg-gray-800 p-3 text-xs font-mono whitespace-pre-wrap">{preview}</pre>
  <div class="flex justify-end gap-2 mt-4">
    <Button color="light" onclick={() => (previewOpen = false)}>Close</Button>
    <Button color="blue" onclick={() => { previewOpen = false; confirmOpen = true; }}>Promote…</Button>
  </div>
</Modal>

<Modal bind:open={confirmOpen} size="md" title="Promote to Git?">
  <p class="text-sm text-gray-700 dark:text-gray-300 mb-2">
    This opens a pull request adding a definition file for
    <strong>{trigger.name}</strong>. The trigger keeps running unchanged until the PR merges; after
    that the definitions repository owns it.
  </p>
  <div class="flex justify-end gap-2 mt-4">
    <Button color="light" onclick={() => (confirmOpen = false)}>Cancel</Button>
    <Button color="blue" onclick={() => runPromote(false)} disabled={busy}>
      {busy ? "Opening PR…" : "Open promotion PR"}
    </Button>
  </div>
</Modal>
