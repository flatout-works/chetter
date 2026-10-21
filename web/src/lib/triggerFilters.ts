/**
 * Pure helpers for the triggers list page.
 *
 * These live outside the Svelte component so the ownership-filtering rules can
 * be unit tested directly. The list distinguishes Git-managed triggers from
 * database drafts, and the two filters mirror the MCP `source` filter values
 * ("config" and "database").
 */

export interface TriggerOwnershipFields {
  sourceId?: string | undefined;
}

/** True when a definitions source (Git) owns the trigger's desired state. */
export function isGitManaged(trigger: TriggerOwnershipFields): boolean {
  return !!trigger.sourceId;
}

/**
 * Apply the ownership toggles. `showConfig` keeps Git-managed triggers and
 * `showDatabase` keeps drafts; with both off nothing matches, which is the
 * expected reading of "show neither".
 */
export function filterByOwnership<T extends TriggerOwnershipFields>(
  triggers: T[],
  showConfig: boolean,
  showDatabase: boolean,
): T[] {
  return triggers.filter((t) => (isGitManaged(t) ? showConfig : showDatabase));
}

/**
 * Apply the trigger-type toggles. An unrecognized type is kept when any type
 * filter is active, matching the pre-existing behavior of the list.
 */
export function filterByTriggerType<T extends { triggerType: string }>(
  triggers: T[],
  showCron: boolean,
  showIssue: boolean,
  showPrReview: boolean,
): T[] {
  if (showCron && showIssue && showPrReview) return triggers;
  return triggers.filter((t) => {
    switch (t.triggerType) {
      case "cron":
        return showCron;
      case "issue":
        return showIssue;
      case "pr_review":
        return showPrReview;
      default:
        return true;
    }
  });
}
