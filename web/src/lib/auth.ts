/**
 * Session management utilities for the web UI.
 *
 * Chetter supports two web authentication modes:
 *  1. Bearer API tokens (stored in localStorage) — the classic flow.
 *  2. OIDC/SSO sessions (HttpOnly cookie, `chetter_session`) — the session
 *     cookie is invisible to JavaScript; the SPA discovers session state via
 *     the `/auth/session` endpoint.
 */
import { getOIDCEnabled } from "$lib/stores/serverInfo.svelte";

export { getOIDCEnabled as isOIDCEnabled };

export const SESSION_ENDPOINT = "/auth/session";
export const LOGIN_ENDPOINT = "/auth/login";
export const LOGOUT_ENDPOINT = "/auth/logout";

/**
 * Checks whether the browser holds a valid OIDC session cookie.
 * Returns false when the session is missing, expired, or the server is
 * unreachable.
 */
export async function checkSession(): Promise<boolean> {
  try {
    const res = await fetch(SESSION_ENDPOINT, {
      credentials: "same-origin",
    });
    if (!res.ok) return false;
    const body = await res.json();
    return body?.authenticated === true;
  } catch {
    return false;
  }
}

/**
 * Redirects the browser to the OIDC login flow. No-op when OIDC is disabled.
 */
export function redirectToLogin() {
  if (typeof window === "undefined") return;
  window.location.assign(LOGIN_ENDPOINT);
}

/**
 * Starts the OIDC logout flow: the server clears the session cookie and
 * redirects to the IdP's end-session endpoint. No-op when OIDC is disabled.
 */
export function redirectToLogout() {
  if (typeof window === "undefined") return;
  window.location.assign(LOGOUT_ENDPOINT);
}
