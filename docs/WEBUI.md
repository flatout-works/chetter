# Chetter Web UI — Design & Technical Choices

This article explains how the Chetter web UI is built, why it is built that way, and
walks through the concrete implementation with code examples. It is a companion to the
[Backend article](BACKEND.md), which covers the MCP server and runner.

Chetter is an "agent fleet control plane": operators use it to submit coding tasks to a
pool of containerized agent runners, watch them execute, and manage triggers, sessions,
tokens and audit state. The web UI is the browser surface over that control plane.

The source lives in `web/` and is compiled into the `chetter` binary, so there is a
single deployable artifact — no separately hosted frontend.

---

## 1. The big idea: a static SPA served by the Go server

The frontend is a **SvelteKit** application, but it is deliberately **not** a
server-rendered app. It builds to a folder of static files and is embedded into the Go
binary with `go:embed`. At runtime the same Go process that exposes the API also serves
the UI, and all data fetching happens client-side over the same ConnectRPC protocol the
runner uses.

This gives three properties that matter for an ops tool:

1. **One artifact, one port.** The web app ships inside `chetter`; there is no CDN, no
   second container, no CORS configuration. The browser talks to the exact origin it was
   served from.
2. **No SSR complexity.** The UI is a pure client; there is no server-side rendering to
   keep in sync with the Go handlers. Data is fetched fresh on every page.
3. **Deep-linking works.** Client-side routes like `/tasks/:id` survive a hard refresh
   because the static file server falls back to `index.html` for unknown paths.

The "no SSR" decision is explicit in `web/src/routes/+layout.ts`:

```ts
// web/src/routes/+layout.ts
export const ssr = false;
```

And the static adapter with an SPA fallback is configured in `svelte.config.js`:

```js
// web/svelte.config.js
import adapter from "@sveltejs/adapter-static";
import { vitePreprocess } from "@sveltejs/vite-plugin-svelte";

const config = {
  preprocess: vitePreprocess(),
  kit: {
    adapter: adapter({
      pages: "build",
      assets: "build",
      fallback: "index.html", // SPA mode: unknown routes serve the shell
      precompress: false,
      strict: false,
    }),
    alias: {
      $lib: "src/lib",
      $gen: "src/gen",
    },
  },
};
```

### How the Go server serves it

The built output in `web/build` is copied into `internal/webui/dist` during the Docker
build (`make web-build`), then embedded:

```go
// internal/webui/webui.go
//go:embed all:dist
var embedded embed.FS

// Handler returns an SPA file server for the embedded UI. During local
// development it falls back to web/build when embedded assets are absent.
func Handler() http.Handler {
	if dist, ok := embeddedDist(); ok {
		return NewHandler(dist)
	}
	if dist, ok := localDist(); ok { // os.DirFS("web/build")
		return NewHandler(dist)
	}
	return http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		http.Error(w, "web UI has not been built", http.StatusNotFound)
	})
}

// NewHandler serves files from dist and falls back to index.html for
// client-side routes.
func NewHandler(dist fs.FS) http.Handler {
	fileServer := http.FileServer(http.FS(dist))
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		requestPath := strings.TrimPrefix(path.Clean("/"+r.URL.Path), "/")
		if requestPath != "" && fileExists(dist, requestPath) {
			fileServer.ServeHTTP(w, r)
			return
		}
		indexReq := r.Clone(r.Context())
		indexReq.URL.Path = "/"
		fileServer.ServeHTTP(w, indexReq)
	})
}
```

The local `web/build` fallback is what makes `go run .` work during development without
shipping the frontend into the binary every iteration.

---

## 2. Stack overview

| Concern | Technology | Why |
|---|---|---|
| Framework | SvelteKit 2 / Svelte 5 (runes) | Small runtime, fine-grained reactivity, no virtual DOM |
| Language | TypeScript 6 (strict) | Protobuf-generated types are checked end to end |
| Bundler | Vite 8 | Dev server with a built-in proxy for `/api` |
| Styling | Tailwind CSS v4 + Flowbite-Svelte | Consistent component system, dark mode out of the box |
| Data | ConnectRPC (`@connectrpc/connect-web`) + protobuf-es | Shares the exact same protobuf contracts as the Go backend |
| Markdown | `marked` + DOMPurify | Render agent transcripts safely |

`package.json` captures the whole dependency surface:

```jsonc
// web/package.json (abridged)
"dependencies": {
  "@bufbuild/protovalidate": "^1.2.0",
  "@connectrpc/connect": "^2.1.2",
  "@connectrpc/connect-web": "^2.1.2",
  "dompurify": "^3.4.12",
  "flowbite": "^4.0.2",
  "flowbite-svelte": "^1.33.1",
  "marked": "^18.0.5"
},
"devDependencies": {
  "@sveltejs/adapter-static": "^3.0.10",
  "@sveltejs/kit": "^2.66.0",
  "@tailwindcss/vite": "^4.3.1",
  "svelte": "^5.56.3",
  "tailwindcss": "^4.3.1",
  "typescript": "^6.0.3",
  "vite": "^8.0.16"
}
```

### Svelte 5 runes

The codebase is written entirely in Svelte 5's **runes** style. Instead of the older
`$:` reactivity labels and `export let` props, components use `$state`, `$derived`,
`$effect` and `$props`. `StatusBadge` is a compact example:

```svelte
<!-- web/src/lib/components/StatusBadge.svelte -->
<script lang="ts">
  import { Badge } from "flowbite-svelte";

  type BadgeColor = "primary" | /* ... */ "rose";

  let { status, label = status }: { status: string; label?: string } = $props();

  const meta = $derived.by((): { color: BadgeColor; dot: string } => {
    switch (status) {
      case "running": case "enabled": case "passed":
        return { color: "green", dot: "bg-green-500" };
      case "error": case "failed": case "disabled": case "resource_limit":
        return { color: "red", dot: "bg-red-500" };
      // ...many more statuses
      default:
        return { color: "gray", dot: "bg-slate-400" };
    }
  });

  const displayLabel = $derived(
    (label === "paused_waiting_review" ? "paused" : label === "pr" ? "pull request" : label)
      .replaceAll("_", " ")
  );
</script>

<Badge color={meta.color} rounded class="..." title={status}>
  <span class={`h-1.5 w-1.5 rounded-full ${meta.dot}`}></span>
  {displayLabel}
</Badge>
```

The `status` value is a plain string that flows directly from the protobuf enums and
event types on the backend; a single `switch` maps the entire domain vocabulary to a
color + label. No extra serialization layer between backend status and UI badge.

---

## 3. Data layer: one protobuf contract everywhere

The defining architectural choice is that the UI does **not** have a hand-written REST
client. It uses the generated ConnectRPC clients for the exact same `proto/api/v1`
services the Go server implements.

Generated TypeScript lives in `web/src/gen/` and is produced by `buf generate` from the
root `.proto` files (see the backend article for the codegen pipeline). The UI imports
typed clients and messages directly:

```ts
// web/src/lib/stores/taskDetail.svelte.ts (abridged)
import { createClient } from "@connectrpc/connect";
import { TaskService, EventService } from "$gen/proto/api/v1/api_pb";
import { getTransport } from "$lib/api/client";

export async function loadTaskEvents(taskId: string, limit = 100) {
  const client = createClient(EventService, getTransport());
  const resp = await client.getTaskEvents({ taskId, limit });
  taskEvents.set(resp.events);
  return resp.events;
}
```

Because the messages come from the same schema, a field rename or type change in a
`.proto` file fails the frontend build — TypeScript enforces the contract across the
Go/TypeScript boundary.

### The transport and auth interceptor

`client.ts` is the single place that creates the Connect transport. Auth is injected as
an interceptor so no route or store has to remember to attach a header:

```ts
// web/src/lib/api/client.ts
import { createConnectTransport } from "@connectrpc/connect-web";

let currentToken: string | null = null;
let currentTransport: ReturnType<typeof createConnectTransport> | null = null;

export function setToken(token: string) {
  currentToken = token;
  if (typeof localStorage !== "undefined") {
    localStorage.setItem("chetter-token", token);
  }
  currentTransport = null; // force recreation
}

export function getTransport() {
  if (currentTransport) return currentTransport;
  const token = getToken();
  currentTransport = createConnectTransport({
    baseUrl: window.location.origin,
    interceptors: [
      (next) => (req) => {
        if (token) {
          req.header.set("Authorization", `Bearer ${token}`);
        }
        return next(req);
      },
    ],
  });
  return currentTransport;
}
```

`baseUrl: window.location.origin` is the "same origin, same process" property again: the
UI assumes it is served by the Go server and does not need a configurable API URL.

### Streaming with `subscribeTaskEvents`

Task detail pages subscribe to a **server-streaming** RPC so the event log updates live
as the agent works. The stream is torn down via an `AbortController` on navigation:

```ts
export function subscribeToTaskEvents(taskId: string, since: string, onTerminal?: () => void) {
  if (abortController) abortController.abort();
  abortController = new AbortController();
  const terminalStatuses = new Set(["done", "error", "cancelled"]);

  (async () => {
    try {
      streamConnected.set(true);
      const client = createClient(TaskService, getTransport());
      const stream = await client.subscribeTaskEvents(
        { taskId, since },
        { signal: abortController.signal },
      );
      for await (const event of stream) {
        if (event.status === "keepalive") continue;
        taskEvents.update((events) => {
          if (event.id && events.some((e) => e.id === event.id)) return events;
          return [...events, event];
        });
        if (terminalStatuses.has(event.status)) { onTerminal?.(); break; }
      }
    } finally {
      streamConnected.set(false);
    }
  })();

  return () => abortController.abort();
}
```

The loop de-duplicates by `event.id` because the initial snapshot fetch and the stream
can race, and it exits as soon as a terminal status arrives rather than waiting for the
server to close the stream.

---

## 4. State management: module-scoped runes + writables

There is no Redux/Zustand-style global store. State lives in plain modules under
`web/src/lib/stores/`, each exporting either a Svelte 5 `$state` or a classic
`writable`, plus the functions that mutate it. This keeps state colocated with its data
access logic.

Two idioms are used side by side:

- **`writable`** for values consumed with the auto-subscribing `$store` syntax, typically
  when a store holds API data that multiple components read (`tasks`, `taskEvents`,
  `settings`, `auth`).
- **`$state`** for stores whose consumers call a getter inside a `$derived` (Svelte 5
  runes cannot auto-subscribe like `$store`), e.g. `toast.svelte.ts`:

```ts
// web/src/lib/stores/toast.svelte.ts
export interface Toast { id: number; message: string; kind: "success" | "error" | "info"; }

let nextId = 0;
const toasts = $state<Toast[]>([]);

export function getToasts() { return toasts; }

export function addToast(message: string, kind: "success" | "error" | "info" = "info") {
  const id = nextId++;
  toasts.push({ id, message, kind });
  setTimeout(() => {
    const idx = toasts.findIndex((t) => t.id === id);
    if (idx >= 0) toasts.splice(idx, 1);
  }, 4000);
}
```

### Polling and back-pressure

The task list and fleet health are refreshed by a self-rescheduling poll that computes
its own interval so it never overlaps itself. A monotonically increasing `generation`
counter makes stale responses (from an older poll or a page that has since navigated
away) safe to discard:

```ts
// web/src/lib/stores/tasks.svelte.ts (abridged)
let pollTimeout: ReturnType<typeof setTimeout> | null = null;
let taskRefreshGeneration = 0;

export async function refreshTasks(status = "", limit = 100, search = "") {
  const generation = ++taskRefreshGeneration;
  try {
    const client = createClient(TaskService, getTransport());
    const resp = await client.listTasks({
      status, limit, ...(search ? { search } : {}),
      ...(teamIds.length > 0 ? { teamIds } : {}),
      ...(repos.length > 0 ? { repos } : {}),
    });
    if (generation === taskRefreshGeneration) tasks.set(resp.tasks);
  } catch (e) { /* ... */ }
}

export function startLiveUpdates() {
  stopLiveUpdates();
  const generation = liveUpdateGeneration;
  const refresh = async () => {
    if (generation !== liveUpdateGeneration) return;
    const started = Date.now();
    await Promise.all([refreshTasks(get(statusFilter)), refreshFleetHealth()]);
    if (generation === liveUpdateGeneration) {
      pollTimeout = setTimeout(refresh, Math.max(0, 5000 - (Date.now() - started)));
    }
  };
  void refresh();
}
```

`stopLiveUpdates()` bumps the generations and clears the timer, so any in-flight request
from a previous view is ignored — a cheap form of cancellation that avoids races without
needing explicit `AbortController`s on every list call.

### Server-side filtering

Team and repository filters are kept in `filter.svelte.ts` and persisted to
`localStorage` so they survive reloads. `effectiveTeamIDs()` returns an **empty array**
when every team is selected, which the UI sends as "no server-side filter" — the backend
interprets the absence of `team_ids` as "everything the caller may see", keeping the
admin full view and the scoped team view on the same code path:

```ts
export function effectiveTeamIDs(): string[] {
  const teams = get(teamFilter);
  if (teams.length === 0) return [];                 // nothing to filter on
  if (teams.every((t) => t.selected)) return [];     // all selected == no filter
  return teams.filter((t) => t.selected).map((t) => t.id);
}
```

---

## 5. Styling: Tailwind v4 + Flowbite-Svelte

The project rule is firm: **no hand-rolled buttons, inputs, cards or modals**. Every
interactive element is a Flowbite-Svelte component, which guarantees dark mode, focus
rings and sizing behave uniformly.

Tailwind CSS v4 is wired through the Vite plugin (not a PostCSS config), and the design
tokens are defined in CSS in `app.css`:

```css
/* web/src/app.css */
@import "tailwindcss";
@plugin "@tailwindcss/typography";
@plugin "flowbite/plugin";
@source "../node_modules/flowbite-svelte/dist";

@custom-variant dark (&:where(.dark, .dark *));

@theme {
  --color-primary-50: #fff5f2;
  --color-primary-100: #fff1ee;
  /* ... */
  --color-primary-900: #a5371b;
}
```

Dark mode uses a **class strategy** (`@custom-variant dark`). The `<html>` element gets a
`.dark` class, and an inline script in `app.html` applies it *before* first paint to
avoid a flash of the wrong theme:

```html
<!-- web/src/app.html -->
<script>
  const theme = localStorage.getItem("chetter-theme");
  if (theme === "dark" || (!theme && window.matchMedia("(prefers-color-scheme: dark)").matches)) {
    document.documentElement.classList.add("dark");
  }
</script>
```

A shared `TableCard` wrapper demonstrates the composition rules — a full-width Card
(`size="xl"`, `!p-0` to remove padding so a section header can draw its own border):

```svelte
<!-- web/src/lib/components/TableCard.svelte -->
<script lang="ts">
  import type { Snippet } from "svelte";
  import { Card } from "flowbite-svelte";

  let { title = "", subtitle = "", children, actions }: {
    title?: string;
    subtitle?: string;
    children: Snippet;
    actions?: Snippet;
  } = $props();
</script>

<Card size="xl" shadow="sm" class="w-full max-w-full overflow-hidden !p-0">
  {#if title || actions}
    <div class="flex items-center justify-between gap-4 border-b border-gray-200 bg-gray-50 px-5 py-4 dark:border-gray-700 dark:bg-gray-800/80">
      <div>
        {#if title}<h2 class="text-sm font-semibold tracking-wide text-gray-900 dark:text-white">{title}</h2>{/if}
        {#if subtitle}<p class="mt-1 text-xs text-gray-500 dark:text-gray-400">{subtitle}</p>{/if}
      </div>
      {#if actions}<div class="shrink-0">{@render actions()}</div>{/if}
    </div>
  {/if}
  <div class="chetter-table max-w-full overflow-x-auto">
    {@render children()}
  </div>
</Card>
```

Svelte 5 **snippets** (`Snippet` + `{@render ...}`) replace the old named-slot pattern
here — callers pass `children` and `actions` as render functions rather than slot markup.

---

## 6. Pages and layout

Routes map directly onto the filesystem under `web/src/routes/`:

```
web/src/routes/
├── +layout.svelte        # app shell: sidebar, topbar, theme, auth, live updates
├── +page.svelte          # dashboard (fleet overview)
├── tasks/+page.svelte    # task list
├── tasks/[id]/+page.svelte   # task detail (live event stream)
├── sessions/+page.svelte
├── sessions/[id]/+page.svelte
├── runners/+page.svelte
├── triggers/[name]/+page.svelte
├── event-callbacks/+page.svelte
├── agents/[name]/+page.svelte
├── admin/{artifacts,audit}/+page.svelte
├── settings/+page.svelte
└── diagnostics/+page.svelte
```

The root `+layout.svelte` owns the app-wide concerns: theme init, auth init, server-info
polling, live task updates, the sidebar navigation, the toast container and the confirm
dialog. It is a good example of how the pieces compose:

```svelte
<!-- web/src/routes/+layout.svelte (abridged) -->
<script lang="ts">
  import { onMount } from "svelte";
  import { page } from "$app/stores";
  import { initAuth, auth } from "$lib/stores/auth.svelte";
  import { initTheme, toggleTheme } from "$lib/stores/theme.svelte";
  import { startLiveUpdates, stopLiveUpdates } from "$lib/stores/tasks.svelte";
  import { getServerInfo } from "$lib/stores/serverInfo.svelte";
  import { Sidebar, SidebarGroup, SidebarItem, /* ... */ } from "flowbite-svelte";

  let { children } = $props();
  let serverInfo = $derived(getServerInfo());

  onMount(() => { initAuth(); initSettings(); initTheme(); });

  // Poll server info only while authenticated.
  $effect(() => {
    if ($auth.authenticated) startServerInfoPolling();
    else stopServerInfoPolling();
  });
</script>
```

Notice the mix: `onMount` for one-time browser-only initialization, `$effect` for
reactive side effects, and `$derived` for computed values.

---

## 7. Authentication: tokens first, OIDC on top

The UI supports two credential flows, both funneling into the same backend scope model:

1. **Bearer API tokens** — stored in `localStorage` under `chetter-token` and injected
   by the Connect transport interceptor. This is the classic flow, also used by the CLI.
2. **OIDC/SSO sessions** — an `HttpOnly` cookie (`__Host-chetter-session` on HTTPS,
   legacy `chetter_session` on plain HTTP) that JavaScript cannot read. The SPA probes
   `GET /auth/session` to discover whether a session exists and redirects to the IdP
   when it does not.

Whether the bearer-token flows are honored depends on the server capability
`CHETTER_ALLOW_TOKEN_LOGIN` (default `true`): when an OIDC-only deployment sets it to
`false`, the login form is hidden and URL/`localStorage` tokens are ignored and cleared.
`initAuth` therefore loads the public server capabilities **before** touching any
browser-stored token:

```ts
// web/src/lib/stores/auth.svelte.ts (abridged)
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
```

The `#token=...` fragment handling is a deliberate pattern: the CLI opens the browser at
a URL that carries a freshly minted token in the hash, and the SPA consumes it and
immediately strips it from the URL so it never appears in server logs or history.

---

## 8. Safe markdown rendering

Agent transcripts and session exports are markdown. Rendering arbitrary agent output as
HTML would be an XSS risk, so the UI runs `marked` output through `DOMPurify`:

```ts
// web/src/lib/utils.svelte.ts (abridged)
import DOMPurify from "dompurify";
import { marked } from "marked";

export function renderMarkdown(text: string, breaks = false): string {
  const html = marked.parse(text, { async: false, breaks });
  return DOMPurify.sanitize(html, { USE_PROFILES: { html: true } });
}
```

Time formatting also lives here, honouring the user's selected timezone and 12/24h
preference from `settings.svelte.ts` via `Intl.DateTimeFormat`.

---

## 9. Development workflow

`vite.config.ts` sets up the dev loop. The `__WEB_GIT_HASH__` define embeds the current
commit into the bundle (shown in the UI footer), and `/api` requests are proxied to the
Go server:

```ts
// web/vite.config.ts
import { sveltekit } from "@sveltejs/kit/vite";
import tailwindcss from "@tailwindcss/vite";
import { defineConfig } from "vite";
import { execSync } from "node:child_process";

function gitHash() {
  try {
    return execSync("git rev-parse --short HEAD", { encoding: "utf8" }).trim();
  } catch {
    return "unknown";
  }
}

const proxyTarget = process.env.VITE_DEV_PROXY_TARGET || "http://localhost:8090";

export default defineConfig({
  plugins: [tailwindcss(), sveltekit()],
  define: { __WEB_GIT_HASH__: JSON.stringify(gitHash()) },
  server: {
    proxy: {
      "^/api": { target: proxyTarget, changeOrigin: true },
    },
  },
});
```

The typical local flow is `npm run dev` in `web/` plus `go run .` in the repo root
(the server listens on `:8090` by default for the web API). `npm run check` runs
`svelte-check` for type checking and `npm test` runs the Vitest suites.

---

## 10. Summary of the key decisions

| Decision | Rationale |
|---|---|
| Static SPA, no SSR | Single Go artifact, no server/client template sync, trivial SPA fallback routing |
| ConnectRPC + protobuf-es | One schema shared with the Go backend; compile-time contract enforcement |
| Auth interceptor on one transport | Single choke point for bearer token injection |
| Module-scoped runes/writables | State colocated with data access; no global store framework |
| Generation counters for polling | Cheap cancellation of stale responses without per-call AbortControllers |
| Flowbite-Svelte only | Uniform components, dark mode, focus states, no hand-rolled controls |
| `marked` + DOMPurify | Rich agent transcripts without introducing an XSS surface |

The result is a thin, typed client that does almost no business logic of its own: it
translates the backend's protobuf domain into Svelte components and streams, and leans
on the server for every decision. That division of responsibility is the real design
statement of the frontend — see [BACKEND.md](BACKEND.md) for where all the logic lives.
