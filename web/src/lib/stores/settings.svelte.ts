import { writable, get } from "svelte/store";
import { browser } from "$app/environment";
import { setTheme } from "./theme.svelte";

export interface Settings {
  timezone: string;
  timeFormat: "12h" | "24h";
  theme: "light" | "dark" | "system";
}

export const TIMEZONES: { city: string; tz: string }[] = [
  { city: "Honolulu", tz: "Pacific/Honolulu" },
  { city: "Anchorage", tz: "America/Anchorage" },
  { city: "Los Angeles", tz: "America/Los_Angeles" },
  { city: "Vancouver", tz: "America/Vancouver" },
  { city: "Denver", tz: "America/Denver" },
  { city: "Chicago", tz: "America/Chicago" },
  { city: "Mexico City", tz: "America/Mexico_City" },
  { city: "New York", tz: "America/New_York" },
  { city: "Toronto", tz: "America/Toronto" },
  { city: "Santiago", tz: "America/Santiago" },
  { city: "Sao Paulo", tz: "America/Sao_Paulo" },
  { city: "Buenos Aires", tz: "America/Argentina/Buenos_Aires" },
  { city: "UTC", tz: "UTC" },
  { city: "London", tz: "Europe/London" },
  { city: "Lisbon", tz: "Europe/Lisbon" },
  { city: "Paris", tz: "Europe/Paris" },
  { city: "Brussels", tz: "Europe/Brussels" },
  { city: "Amsterdam", tz: "Europe/Amsterdam" },
  { city: "Berlin", tz: "Europe/Berlin" },
  { city: "Zurich", tz: "Europe/Zurich" },
  { city: "Vienna", tz: "Europe/Vienna" },
  { city: "Rome", tz: "Europe/Rome" },
  { city: "Oslo", tz: "Europe/Oslo" },
  { city: "Stockholm", tz: "Europe/Stockholm" },
  { city: "Helsinki", tz: "Europe/Helsinki" },
  { city: "Warsaw", tz: "Europe/Warsaw" },
  { city: "Kyiv", tz: "Europe/Kyiv" },
  { city: "Athens", tz: "Europe/Athens" },
  { city: "Istanbul", tz: "Europe/Istanbul" },
  { city: "Cairo", tz: "Africa/Cairo" },
  { city: "Moscow", tz: "Europe/Moscow" },
  { city: "Dubai", tz: "Asia/Dubai" },
  { city: "Mumbai", tz: "Asia/Kolkata" },
  { city: "Bangkok", tz: "Asia/Bangkok" },
  { city: "Singapore", tz: "Asia/Singapore" },
  { city: "Shanghai", tz: "Asia/Shanghai" },
  { city: "Hong Kong", tz: "Asia/Hong_Kong" },
  { city: "Tokyo", tz: "Asia/Tokyo" },
  { city: "Seoul", tz: "Asia/Seoul" },
  { city: "Sydney", tz: "Australia/Sydney" },
  { city: "Melbourne", tz: "Australia/Melbourne" },
  { city: "Auckland", tz: "Pacific/Auckland" },
  { city: "Fiji", tz: "Pacific/Fiji" },
];

const STORAGE_KEY = "chetter-settings";

const defaults: Settings = {
  timezone: "",
  timeFormat: "24h",
  theme: "system",
};

function load(): Settings {
  if (!browser) return { ...defaults };
  try {
    const raw = localStorage.getItem(STORAGE_KEY);
    if (raw) {
      return { ...defaults, ...JSON.parse(raw) };
    }
  } catch {
    /* ignore corrupt data */
  }
  return { ...defaults };
}

function save(s: Settings) {
  if (!browser) return;
  localStorage.setItem(STORAGE_KEY, JSON.stringify(s));
}

export const settings = writable<Settings>(load());

export function updateSettings(patch: Partial<Settings>) {
  settings.update((s) => {
    const next = { ...s, ...patch };
    save(next);
    if (patch.theme !== undefined) {
      applyTheme(patch.theme);
    }
    return next;
  });
}

export function applyTheme(mode: string) {
  if (!browser) return;
  if (mode === "system") {
    const isDark = window.matchMedia("(prefers-color-scheme: dark)").matches;
    setTheme(isDark ? "dark" : "light");
  } else if (mode === "dark" || mode === "light") {
    setTheme(mode as "dark" | "light");
  }
}

export function initSettings() {
  if (!browser) return;
  const s = load();
  applyTheme(s.theme);
  settings.set(s);
}

export function getSettings(): Settings {
  return get(settings);
}
