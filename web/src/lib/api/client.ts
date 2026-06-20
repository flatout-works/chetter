import { createConnectTransport } from "@connectrpc/connect-web";

let currentToken: string | null = null;
let currentTransport: ReturnType<typeof createConnectTransport> | null = null;

export function getToken(): string | null {
  if (currentToken) return currentToken;
  if (typeof localStorage !== "undefined") {
    return localStorage.getItem("chetter-token");
  }
  return null;
}

export function setToken(token: string) {
  currentToken = token;
  if (typeof localStorage !== "undefined") {
    localStorage.setItem("chetter-token", token);
  }
  currentTransport = null; // force recreation
}

export function clearToken() {
  currentToken = null;
  if (typeof localStorage !== "undefined") {
    localStorage.removeItem("chetter-token");
  }
  currentTransport = null;
}

export function isAuthenticated(): boolean {
  return !!getToken();
}

export function getTransport() {
  if (currentTransport) return currentTransport;

  const token = getToken();
  if (!token) throw new Error("Not authenticated");

  currentTransport = createConnectTransport({
    baseUrl: window.location.origin,
    interceptors: [
      (next) => (req) => {
        req.header.set("Authorization", `Bearer ${token}`);
        return next(req);
      },
    ],
  });
  return currentTransport;
}
