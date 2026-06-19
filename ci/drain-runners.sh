#!/usr/bin/env bash
set -euo pipefail
# Drain all active chetter runners via the MCP API.
# Requires CHETTER_MCP_AUTH_TOKEN and MCP_URL (default: http://localhost:18088/mcp).

: "${MCP_URL:=http://localhost:18088/mcp}"
: "${CHETTER_MCP_AUTH_TOKEN:?CHETTER_MCP_AUTH_TOKEN is required}"
: "${DRAIN_TIMEOUT_SEC:=600}"
: "${DRAIN_POLL_INTERVAL:=10}"

mcp_call() {
  local json="$1"
  curl --silent --show-error --fail-with-body \
    --header "Authorization: Bearer ${CHETTER_MCP_AUTH_TOKEN}" \
    --header "Content-Type: application/json" \
    --data "${json}" \
    "${MCP_URL}"
}

mcp_tool() {
  local name="$1" args="$2"
  printf '{"jsonrpc":"2.0","method":"tools/call","params":{"name":"%s","arguments":%s},"id":1}' "${name}" "${args}"
}

echo "=== Fetching active runners ==="
RESP=$(mcp_call "$(mcp_tool chetter_runner_health '{"include_tasks":false}')" 2>/dev/null || echo '{}')

ACTIVE_RUNNERS=$(echo "${RESP}" | python3 -c "
import sys, json
try:
  data = json.load(sys.stdin)
  text = data['result']['content'][0]['text']
  health = json.loads(text)
  for r in health.get('runners',[]):
    if r.get('running_tasks',0) > 0 and r.get('is_stale',True) == False:
      print(r.get('id',''))
except Exception as e:
  print(f'# parse error: {e}', file=sys.stderr)
")

if [ -z "${ACTIVE_RUNNERS}" ]; then
  echo "No non-stale active runners with running tasks — nothing to drain."
  exit 0
fi

echo "Draining:"
echo "${ACTIVE_RUNNERS}" | while read -r id; do [ -n "$id" ] && echo "  ${id}"; done

echo "${ACTIVE_RUNNERS}" | while read -r id; do
  [ -z "$id" ] && continue
  echo "Requesting drain for ${id}..."
  mcp_call "$(mcp_tool chetter_drain_runner "{\"runner_id\":\"${id}\"}")" >/dev/null 2>&1 || \
    echo "  WARNING: drain call failed for ${id} (may already be draining)"
done

echo "=== Waiting for runners to drain (timeout: ${DRAIN_TIMEOUT_SEC}s) ==="
DEADLINE=$(($(date +%s) + DRAIN_TIMEOUT_SEC))
while [ $(date +%s) -lt ${DEADLINE} ]; do
  RESP=$(mcp_call "$(mcp_tool chetter_runner_health '{"include_tasks":false}')" 2>/dev/null || echo '{}')
  RUNNING=$(echo "${RESP}" | python3 -c "
import sys, json
count = 0
try:
  data = json.load(sys.stdin)
  text = data['result']['content'][0]['text']
  health = json.loads(text)
  count = sum(r.get('running_tasks',0) for r in health.get('runners',[]) if not r.get('is_stale',True))
except:
  pass
print(count)
")
  echo "$(date +%H:%M:%S) running tasks: ${RUNNING:-?}"
  if [ "${RUNNING:-1}" = "0" ]; then
    echo "All runners drained."
    exit 0
  fi
  sleep "${DRAIN_POLL_INTERVAL}"
done

echo "WARNING: Drain timeout reached — proceeding with redeploy anyway."
