#!/bin/sh
# cloc-chetter.sh — count lines in the Chetter repo, excluding generated code.
#
# Generated / vendored paths excluded:
#   gen/                  — protobuf stubs (buf)
#   internal/repository/  — SQL query code (sqlc)
#   internal/webui/dist/  — Vite-built SvelteKit assets (embedded)
#   web/build/            — Vite build output
#   web/.svelte-kit/      — SvelteKit cache
#   web/node_modules/     — npm deps
#   web/src/gen/          — generated web code
#   .opencode/            — opencode config / sessions
#   .opentrace/           — opentrace tooling
#   bin/                  — downloaded build tools
#   .git/                 — git metadata
#   .github/              — CI workflows
#
# Usage: ./cloc-chetter.sh [cloc-args...]
#   ./cloc-chetter.sh
#   ./cloc-chetter.sh --by-file

set -eu

cloc \
  --exclude-dir=gen,node_modules,build,.svelte-kit,bin,.git,.github,.opencode,.opentrace \
  --not-match-d='internal/repository|internal/webui/dist|web/src/gen' \
  --not-match-f='\.svg$|\.png$|\.ico$|\.sum$|\.lock$' \
  "$@" \
  .
