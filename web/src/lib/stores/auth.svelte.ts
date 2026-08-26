import { writable } from "svelte/store";
import { clearToken, getToken, setToken } from "$lib/api/client";
import { ensureServerInfoLoaded, getAllowTokenLogin, getOIDCEnabled } from "$lib/stores/serverInfo.svelte";
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

  // Load the public auth capabilities before touching browser-stored tokens.
  await ensureServerInfoLoaded();

  // Token from URL (#token=...) — used by the CLI token flow.
  const tokenFromURL = tokenFromLocation();
  if (tokenFromURL && getAllowTokenLogin()) {
    login(tokenFromURL);
    window.history.replaceState(null, "", window.location.pathname + window.location.search);
    return;
  }
	if (tokenFromURL) {
		window.history.replaceState(null, "", window.location.pathname + window.location.search);
	}

  // Classic bearer token flow.
  if (getAllowTokenLogin()) {
		const token = localStorage.getItem("chetter-token");
		if (token) {
			auth.set({ authenticated: true, token, error: null });
			return;
		}
	} else {
		clearToken();
	}

  // OIDC/SSO flow: the session cookie is HttpOnly, so we probe the server.
  if (!getOIDCEnabled()) {
		auth.set({
			authenticated: false,
			token: null,
			error: getAllowTokenLogin() ? null : "No browser login method is enabled. Contact your administrator.",
		});
		return;
	}

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
	if (!getAllowTokenLogin()) {
		auth.set({ authenticated: false, token: null, error: "Bearer-token login is disabled for this web UI." });
		return;
	}
  setToken(token);
  auth.set({ authenticated: true, token, error: null });
}

export function logout() {
	if (getToken()) {
		clearToken();
		auth.set({ authenticated: false, token: null, error: null });
		return;
	}
  // With OIDC enabled the session lives in an HttpOnly cookie that client
  // code cannot clear; the server-side /auth/logout endpoint clears it and
  // redirects to the IdP's end-session endpoint.
  if (getOIDCEnabled()) {
    redirectToLogout();
    return;
  }
  auth.set({ authenticated: false, token: null, error: null });
}
