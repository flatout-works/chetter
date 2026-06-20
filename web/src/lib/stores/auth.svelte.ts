import { writable } from "svelte/store";
import { clearToken, setToken } from "$lib/api/client";

export type AuthState = {
  authenticated: boolean;
  token: string | null;
  error: string | null;
};

export const auth = writable<AuthState>({
  authenticated: false,
  token: null,
  error: null,
});

export function initAuth() {
  if (typeof localStorage === "undefined") return;
  const tokenFromURL = tokenFromLocation();
  if (tokenFromURL) {
    login(tokenFromURL);
    window.history.replaceState(null, "", window.location.pathname + window.location.search);
    return;
  }
  const token = localStorage.getItem("chetter-token");
  if (token) {
    auth.set({ authenticated: true, token, error: null });
  }
}

function tokenFromLocation() {
  if (typeof window === "undefined") return "";
  const hash = window.location.hash.replace(/^#/, "");
  if (!hash) return "";
  return new URLSearchParams(hash).get("token")?.trim() ?? "";
}

export function login(token: string) {
  setToken(token);
  auth.set({ authenticated: true, token, error: null });
}

export function logout() {
  clearToken();
  auth.set({ authenticated: false, token: null, error: null });
}
