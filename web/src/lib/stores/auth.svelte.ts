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
  const token = localStorage.getItem("chetter-token");
  if (token) {
    auth.set({ authenticated: true, token, error: null });
  }
}

export function login(token: string) {
  setToken(token);
  auth.set({ authenticated: true, token, error: null });
}

export function logout() {
  clearToken();
  auth.set({ authenticated: false, token: null, error: null });
}
