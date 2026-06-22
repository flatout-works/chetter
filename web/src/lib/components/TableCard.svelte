<script lang="ts">
  import type { Snippet } from "svelte";

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

<section class="overflow-hidden rounded-2xl border border-slate-200 bg-white shadow-sm shadow-slate-200/60 dark:border-slate-800 dark:bg-slate-900 dark:shadow-black/20">
  {#if title || actions}
    <div class="flex items-center justify-between gap-4 border-b border-slate-200 bg-slate-50/70 px-5 py-4 dark:border-slate-800 dark:bg-slate-900/80">
      <div>
        {#if title}
          <h2 class="text-sm font-semibold tracking-wide text-slate-900 dark:text-slate-100">{title}</h2>
        {/if}
        {#if subtitle}
          <p class="mt-1 text-xs text-slate-500 dark:text-slate-400">{subtitle}</p>
        {/if}
      </div>
      {#if actions}
        <div class="shrink-0">
          {@render actions()}
        </div>
      {/if}
    </div>
  {/if}
  <div class="chetter-table overflow-x-auto">
    {@render children()}
  </div>
</section>
