#!/usr/bin/env bash
set -euo pipefail

# Regression tests for the python parse blocks embedded in drain-runners.sh.
# Guards the two bug classes found on PR #420:
#   1. the health payload nesting (runners live under health.health), and
#   2. bash stripping double quotes inside the python3 -c "..." strings,
#      which made the nesting fix silently a no-op.
# Both bugs are invisible to `bash -n` and shellcheck.

SCRIPT_DIR=$(cd "$(dirname "$0")" && pwd)
TARGET="$SCRIPT_DIR/drain-runners.sh"

if [ ! -f "$TARGET" ]; then
  echo "FAIL: cannot find $TARGET" >&2
  exit 1
fi

# Canned runner-health response with the API's nested health.health payload:
# r1 has 2 running tasks, r2 is idle.
FIXTURE=$(cat <<'FIXEOF'
{"result":{"content":[{"text":"{\"health\":{\"runners\":[{\"id\":\"r1\",\"running_tasks\":2,\"is_stale\":false},{\"id\":\"r2\",\"running_tasks\":0,\"is_stale\":false}]}}"}]}}
FIXEOF
)

# Extract the nth python3 -c "..." program verbatim from drain-runners.sh,
# so the test exercises the exact embedded snippets instead of a copy.
python_block() { # $1 = occurrence number (1-based)
  awk -v n="$1" '
    /python3 -c "/ { count++; if (count == n) { capture = 1; next } }
    capture && /^"\)$/ { exit }
    capture { print }
  ' "$TARGET"
}

BLOCK_ACTIVE=$(python_block 1)
BLOCK_COUNT=$(python_block 2)

if [ -z "$BLOCK_ACTIVE" ] || [ -z "$BLOCK_COUNT" ]; then
  echo "FAIL: could not extract both python blocks from $TARGET" >&2
  exit 1
fi

# 1. Target selection must print the busy runner id from the nested payload.
ACTIVE=$(echo "$FIXTURE" | python3 -c "$BLOCK_ACTIVE")
if [ "$ACTIVE" != "r1" ]; then
  echo "FAIL: active-runners block printed [$ACTIVE], want [r1]" >&2
  exit 1
fi

# 2. Polling count must sum running tasks of non-stale runners.
RUNNING=$(echo "$FIXTURE" | python3 -c "$BLOCK_COUNT")
if [ "$RUNNING" != "2" ]; then
  echo "FAIL: running-count block printed [$RUNNING], want [2]" >&2
  exit 1
fi

# 3. On a malformed payload the count must fail closed (-1), never 0:
# a parse/API failure must not read as "all runners drained".
BAD=$(echo 'garbage' | python3 -c "$BLOCK_COUNT")
if [ "$BAD" != "-1" ]; then
  echo "FAIL: running-count block on garbage printed [$BAD], want [-1]" >&2
  exit 1
fi

echo "drain-runners parse blocks OK"
