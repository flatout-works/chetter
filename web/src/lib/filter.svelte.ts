import { get } from "svelte/store";
import { teamFilter, repoFilter, selectedTeamIDs, selectedRepos } from "$lib/stores/filter.svelte";

/** Apply client-side team + repo filters to a list of records.
 *  Each record should have `teamId` and optionally `gitUrl`. */
export function applyFilters<T extends { teamId?: string | null; gitUrl?: string | null }>(
  items: T[]
): T[] {
  const teams = get(teamFilter);
  const repos = get(repoFilter);

  const allTeamsSelected = teams.length === 0 || teams.every((t) => t.selected);
  const noRepos = repos.length === 0;

  // Fast path: no filtering needed
  if (allTeamsSelected && noRepos) return items;

  const selectedTeamSet = new Set(teams.filter((t) => t.selected).map((t) => t.id));
  const repoPatterns = repos.map((r) => r.toLowerCase());

  return items.filter((item) => {
    // Team filter
    if (!allTeamsSelected) {
      if (item.teamId && !selectedTeamSet.has(item.teamId)) return false;
    }
    // Repo filter (match on gitUrl substring, case-insensitive)
    if (!noRepos && item.gitUrl) {
      const url = item.gitUrl.toLowerCase();
      const matchesRepo = repoPatterns.some((pattern) => url.includes(pattern));
      if (!matchesRepo) return false;
    }
    return true;
  });
}
