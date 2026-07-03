<script lang="ts">
  import { Badge } from "flowbite-svelte";

  type BadgeColor = "primary" | "secondary" | "gray" | "red" | "orange" | "amber" | "yellow" | "lime" | "green" | "emerald" | "teal" | "cyan" | "sky" | "blue" | "indigo" | "violet" | "purple" | "fuchsia" | "pink" | "rose";

  let {
    status,
    label = status,
  }: {
    status: string;
    label?: string;
  } = $props();

  const meta = $derived.by((): { color: BadgeColor; dot: string } => {
    switch (status) {
      case "running":
      case "enabled":
        return { color: "green", dot: "bg-green-500" };
      case "pending":
      case "paused":
      case "paused_waiting_review":
      case "stale":
        return { color: "yellow", dot: "bg-yellow-500" };
      case "recoverable":
        return { color: "orange", dot: "bg-orange-500" };
      case "done":
      case "completed":
        return { color: "blue", dot: "bg-blue-500" };
      case "error":
      case "failed":
      case "disabled":
        return { color: "red", dot: "bg-red-500" };
      case "cancelled":
        return { color: "gray", dot: "bg-slate-400" };
      case "resuming":
      case "pr_review":
        return { color: "purple", dot: "bg-purple-500" };
      case "cron":
        return { color: "cyan", dot: "bg-cyan-500" };
      case "issue":
        return { color: "pink", dot: "bg-pink-500" };
      case "pr":
      case "pull_request":
        return { color: "purple", dot: "bg-purple-500" };
      case "issue_comment":
        return { color: "indigo", dot: "bg-indigo-500" };
      case "pr_review_artifact":
        return { color: "amber", dot: "bg-amber-500" };
      case "webhook_received":
        return { color: "emerald", dot: "bg-emerald-500" };
      case "task_submitted":
        return { color: "sky", dot: "bg-sky-500" };
      case "trigger_matched":
        return { color: "violet", dot: "bg-violet-500" };
      case "artifact_discovered":
        return { color: "amber", dot: "bg-amber-500" };
      default:
        return { color: "gray", dot: "bg-slate-400" };
    }
  });

  const displayLabel = $derived((label === "paused_waiting_review" ? "paused" : label === "pr" ? "pull request" : label).replaceAll("_", " "));
</script>

<Badge color={meta.color} rounded class="inline-flex items-center gap-1.5 px-2.5 py-1 text-[11px] font-bold uppercase tracking-wide shadow-sm ring-1 ring-inset ring-black/5 dark:ring-white/10" title={status}>
  <span class={`h-1.5 w-1.5 rounded-full ${meta.dot}`}></span>
  {displayLabel}
</Badge>
