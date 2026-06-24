import { writable } from "svelte/store";
import { updateSettings } from "./settings.svelte";

export type ThemeMode = "light" | "dark";

export const theme = writable<ThemeMode>("light");

export function initTheme() {
  if (typeof document === "undefined") return;
  const stored = localStorage.getItem("chetter-theme");
  const isDark =
    stored === "dark" ||
    (!stored && window.matchMedia("(prefers-color-scheme: dark)").matches);
  setTheme(isDark ? "dark" : "light");
}

export function setTheme(mode: ThemeMode) {
  theme.set(mode);
  localStorage.setItem("chetter-theme", mode);
  if (mode === "dark") {
    document.documentElement.classList.add("dark");
  } else {
    document.documentElement.classList.remove("dark");
  }
}

export function toggleTheme() {
  const current = localStorage.getItem("chetter-theme") ?? "light";
  const next = current === "dark" ? "light" : "dark";
  setTheme(next as ThemeMode);
  updateSettings({ theme: next as ThemeMode });
}
