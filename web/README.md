# Chetter Web UI

This directory contains the Chetter web UI. It is a SvelteKit app built as a static single-page application and served by the Go `chetter` server.

## Stack

- Svelte 5 + SvelteKit
- Vite
- Tailwind CSS v4
- Flowbite / Flowbite Svelte
- ConnectRPC web client generated from `proto/api/v1/api.proto`

## Local Development

Install dependencies once:

```bash
npm install
```

Run the Vite dev server:

```bash
npm run dev
```

The dev server proxies ConnectRPC API calls under `/api.v1` to `http://localhost:8090` via `vite.config.ts`. Run the Go server separately with `WEB_ADDR=:8090` (the default) so the UI can talk to the web API.

Useful commands:

```bash
npm run check
npm run build
npm run preview
```

## Build Output

The app uses `@sveltejs/adapter-static` in `svelte.config.js`:

- Static pages and assets are emitted to `web/build`.
- `fallback: "index.html"` makes it work as an SPA with client-side routes.
- `strict: false` allows non-prerendered client routes to fall back to the SPA shell.

## How It Is Served

The production Docker build runs `npm run build` and copies `web/build` into `internal/webui/dist` before compiling the Go binary. The Go package `internal/webui` embeds `internal/webui/dist` with `go:embed`.

At runtime, `main.go` starts a separate web/API HTTP server on `WEB_ADDR` and registers:

- ConnectRPC/web API routes from `internal/webapi`
- `/healthz`
- the embedded web UI at `/`

The UI handler serves static assets directly and falls back to `index.html` for unknown paths so routes like `/tasks/:id` work after page refreshes.

For local Go development, `internal/webui.Handler()` falls back to reading `web/build` from disk when embedded assets are not present. Run `npm run build` first if you want `go run .` to serve the UI locally.

## Generated API Client

Generated TypeScript protobuf files live under `web/src/gen/`. They are generated from the root repo protobuf definitions by the root code generation workflow. Do not edit generated files by hand.

The API client wrapper lives in `web/src/lib/api/client.ts` and is used by Svelte routes/stores under `web/src/routes` and `web/src/lib`.

## Directory Overview

- `src/routes/` - SvelteKit pages for tasks, sessions, runners, triggers, and admin screens.
- `src/lib/api/` - ConnectRPC client setup.
- `src/lib/stores/` - Svelte state modules for task/session UI state.
- `src/gen/` - generated protobuf/Connect types.
- `src/app.css` - global styles and Tailwind imports.
- `build/` - generated static output; not source.
