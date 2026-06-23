<script lang="ts">
  import type { Snippet } from "svelte";
  import { Card } from "flowbite-svelte";

  let {
    title = "",
    subtitle = "",
    children,
    actions,
  }: {
    title?: string;
    subtitle?: string;
    children: Snippet;
    actions?: Snippet;
  } = $props();
</script>

<Card size="xl" shadow="sm" class="w-full max-w-full overflow-hidden" contentClass="!p-0">
  {#if title || actions}
    <div class="flex items-center justify-between gap-4 border-b border-gray-200 bg-gray-50 px-5 py-4 dark:border-gray-700 dark:bg-gray-800/80">
      <div>
        {#if title}
          <h2 class="text-sm font-semibold tracking-wide text-gray-900 dark:text-white">{title}</h2>
        {/if}
        {#if subtitle}
          <p class="mt-1 text-xs text-gray-500 dark:text-gray-400">{subtitle}</p>
        {/if}
      </div>
      {#if actions}
        <div class="shrink-0">
          {@render actions()}
        </div>
      {/if}
    </div>
  {/if}
  <div class="chetter-table max-w-full overflow-x-auto">
    {@render children()}
  </div>
</Card>
