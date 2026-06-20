import { writable } from "svelte/store";

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
  localStorage.setItem("chetter-token", token);
  auth.set({ authenticated: true, token, error: null });
}

export function logout() {
  localStorage.removeItem("chetter-token");
  auth.set({ authenticated: false, token: null, error: null });
}
