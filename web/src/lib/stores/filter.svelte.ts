import { writable, get } from "svelte/store";

export type TeamOption = {
  id: string;
  name: string;
  selected: boolean;
};

function loadTeams(): TeamOption[] {
  if (typeof localStorage === "undefined") return [];
  try {
    const raw = localStorage.getItem("chetter-filter-teams");
    if (raw) return JSON.parse(raw);
  } catch { /* ignore */ }
  return [];
}

function saveTeams(teams: TeamOption[]) {
  if (typeof localStorage === "undefined") return;
  localStorage.setItem("chetter-filter-teams", JSON.stringify(teams));
}

export const teamFilter = writable<TeamOption[]>(loadTeams());

export function setTeamOptions(options: TeamOption[]) {
  teamFilter.set(options);
  saveTeams(options);
}

export function toggleTeam(name: string) {
  teamFilter.update((teams) => {
    const next = teams.map((t) =>
      t.name === name ? { ...t, selected: !t.selected } : t
    );
    saveTeams(next);
    return next;
  });
}

export function selectAllTeams() {
  teamFilter.update((teams) => {
    const next = teams.map((t) => ({ ...t, selected: true }));
    saveTeams(next);
    return next;
  });
}

export function deselectAllTeams() {
  teamFilter.update((teams) => {
    const next = teams.map((t) => ({ ...t, selected: false }));
    saveTeams(next);
    return next;
  });
}

export function selectedTeamIDs(): string[] {
  return get(teamFilter).filter((t) => t.selected).map((t) => t.id);
}

export function selectedTeamNames(): string[] {
  return get(teamFilter).filter((t) => t.selected).map((t) => t.name);
}

// --- Repo filter ---

export const repoFilter = writable<string[]>([]);

function loadRepos(): string[] {
  if (typeof localStorage === "undefined") return [];
  try {
    const raw = localStorage.getItem("chetter-filter-repos");
    if (raw) return JSON.parse(raw);
  } catch { /* ignore */ }
  return [];
}

function saveRepos(repos: string[]) {
  if (typeof localStorage === "undefined") return;
  localStorage.setItem("chetter-filter-repos", JSON.stringify(repos));
}

export function setRepoOptions(repos: string[]) {
  repoFilter.set(repos);
  saveRepos(repos);
}

export function addRepo(repo: string) {
  repoFilter.update((repos) => {
    if (!repos.includes(repo)) {
      const next = [...repos, repo];
      saveRepos(next);
      return next;
    }
    return repos;
  });
}

export function removeRepo(repo: string) {
  repoFilter.update((repos) => {
    const next = repos.filter((r) => r !== repo);
    saveRepos(next);
    return next;
  });
}

export function hasRepoFilter(): boolean {
  return get(repoFilter).length > 0;
}

export function selectedRepos(): string[] {
  return get(repoFilter);
}
