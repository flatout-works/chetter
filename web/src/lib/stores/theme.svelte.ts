import { writable } from "svelte/store";

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
  theme.update((t) => (t === "dark" ? "light" : "dark")) as void;
  const current = localStorage.getItem("chetter-theme");
  setTheme(current === "dark" ? "light" : "dark");
}
