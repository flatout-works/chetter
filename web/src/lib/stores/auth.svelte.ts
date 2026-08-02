import { writable } from "svelte/store";
import { clearToken, setToken } from "$lib/api/client";
import { ensureServerInfoLoaded, getOIDCEnabled } from "$lib/stores/serverInfo.svelte";
import { checkSession, redirectToLogin, redirectToLogout } from "$lib/auth";

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

export async function initAuth() {
  if (typeof localStorage === "undefined") return;

  // Token from URL (#token=...) — used by the CLI token flow.
  const tokenFromURL = tokenFromLocation();
  if (tokenFromURL) {
    login(tokenFromURL);
    window.history.replaceState(null, "", window.location.pathname + window.location.search);
    return;
  }

  // Classic bearer token flow.
  const token = localStorage.getItem("chetter-token");
  if (token) {
    auth.set({ authenticated: true, token, error: null });
    return;
  }

  // OIDC/SSO flow: the session cookie is HttpOnly, so we probe the server.
  // The server-info fetch is awaited so we never redirect based on a stale
  // oidcEnabled flag.
  await ensureServerInfoLoaded();
  if (!getOIDCEnabled()) return;

  if (await checkSession()) {
    auth.set({ authenticated: true, token: null, error: null });
    return;
  }
  // No valid session — send the user to the IdP.
  redirectToLogin();
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
  // With OIDC enabled the session lives in an HttpOnly cookie that client
  // code cannot clear; the server-side /auth/logout endpoint clears it and
  // redirects to the IdP's end-session endpoint.
  if (getOIDCEnabled()) {
    redirectToLogout();
    return;
  }
  clearToken();
  auth.set({ authenticated: false, token: null, error: null });
}
