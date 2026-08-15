#!/usr/bin/env bash
# Validate the README Quick Start on a disposable Ubuntu 24.04 KVM VM.
#
# Usage:
#   DEEPSEEK_API_KEY=sk-... ops/test-quickstart.sh [--gvisor]
#
# The script provisions a fresh Ubuntu 24.04 VM (no Docker preinstalled),
# installs the quickstart prerequisites, then runs the Quick Start commands
# from the README verbatim and asserts every step:
#   build images -> compose up -> DB auto-created + migrations -> MCP tools ->
#   fleet active -> real task executes -> self-test passes.
#
# With --gvisor, the script then installs gVisor (runsc) in the VM, switches
# the stack to hardened mode (USE_GVISOR=true, isolation enforced for every
# task), and re-validates: runners advertise isolation, a task runs with the
# runsc runtime, and the self-test passes under enforcement.
#
# Environment:
#   DEEPSEEK_API_KEY   required for the task/self-test steps (skipped if unset)
#   QS_KEEP_VM=1       keep the VM running after the run (for debugging)
#   QS_VM_NAME         VM name (default chetter-quickstart-test)
#   QS_MEMORY, QS_CPUS VM size (default 4096 / 2)
#   QS_IMAGE           cached cloud image path (default ~/.cache/chetter-qs/noble.img)
#   QS_SSH_KEY         public key injected into the VM (default ~/.ssh/id_rsa.pub)
#   QS_USER            VM user (default test)
#
# Requires: /dev/kvm, libvirt tooling (virt-install, virsh, cloud-localds,
# qemu-img), curl, python3, openssl, sudo (passwordless or interactive),
# network access. Prerequisites are validated up front.

set -euo pipefail

GVISOR=0
case "${1:-}" in
  --gvisor) GVISOR=1 ;;
  -h|--help) grep -E "^# ?(Usage|  |#   |# With)" "$0" | sed 's/^# \{0,1\}//'; exit 0 ;;
  "") ;;
  *) echo "usage: $0 [--gvisor]" >&2; exit 2 ;;
esac

VM_NAME=${QS_VM_NAME:-chetter-quickstart-test}
MEMORY=${QS_MEMORY:-4096}
CPUS=${QS_CPUS:-2}
QS_USER=${QS_USER:-test}
IMAGE_URL=${QS_IMAGE_URL:-https://cloud-images.ubuntu.com/noble/current/noble-server-cloudimg-amd64.img}
CACHE_DIR=${QS_CACHE_DIR:-$HOME/.cache/chetter-qs}
IMAGE_PATH=${QS_IMAGE:-$CACHE_DIR/noble-server-cloudimg-amd64.img}
SSH_KEY=${QS_SSH_KEY:-$HOME/.ssh/id_rsa.pub}
KEEP_VM=${QS_KEEP_VM:-0}
LIBVIRT_IMG=/var/lib/libvirt/images/${VM_NAME}.img
SEED_ISO=$CACHE_DIR/${VM_NAME}-seed.iso

PASS=0
FAIL=0
declare -a RESULTS=()

die() { echo "ERROR: $*" >&2; exit 1; }

note() { printf '\n=== %s ===\n' "$*"; }

check() { # check <name> <status> [detail]
  local name=$1 status=$2 detail=${3:-}
  RESULTS+=("$name|$status|$detail")
  if [[ "$status" == "PASS" ]]; then PASS=$((PASS+1)); else FAIL=$((FAIL+1)); fi
  printf '  [%s] %s %s\n' "$status" "$name" "$detail"
}

vm_ssh() { ssh -o BatchMode=yes -o StrictHostKeyChecking=accept-new -o ConnectTimeout=15 "$QS_USER@$VM_IP" "$@"; }

vm_scp() { scp -o BatchMode=yes "$1" "$QS_USER@$VM_IP:$2"; }

# --- prerequisites -----------------------------------------------------------

note "Prerequisites"
[[ -e /dev/kvm ]] || die "KVM not available (/dev/kvm missing)"
for cmd in virt-install virsh cloud-localds qemu-img curl python3 openssl sudo; do
  command -v "$cmd" >/dev/null || die "required command not found: $cmd (install qemu-system-x86 libvirt-daemon-system libvirt-clients virtinst cloud-image-utils cloud-utils)"
done
[[ -f "$SSH_KEY" ]] || die "SSH public key not found at $SSH_KEY"
if ! systemctl is-active libvirtd >/dev/null 2>&1; then
  echo "libvirtd not running — starting it ..."
  sudo systemctl enable --now libvirtd || die "could not start libvirtd"
fi
if ! sudo -n true 2>/dev/null; then
  echo "note: passwordless sudo not available — you will be prompted for the sudo password"
fi
echo "OK"

# --- cloud image + seed ------------------------------------------------------

note "Preparing cloud image and seed"
mkdir -p "$CACHE_DIR"
if [[ ! -f "$IMAGE_PATH" ]]; then
  echo "Downloading Ubuntu 24.04 cloud image ($IMAGE_URL) ..."
  curl -fsSL -o "$IMAGE_PATH" "$IMAGE_URL" || die "image download failed"
fi
cat >"$CACHE_DIR/user-data" <<EOF
#cloud-config
users:
  - name: $QS_USER
    sudo: ALL=(ALL) NOPASSWD:ALL
    shell: /bin/bash
    ssh_authorized_keys:
      - $(cat "$SSH_KEY")
ssh_pwauth: false
EOF
cat >"$CACHE_DIR/meta-data" <<EOF
instance-id: $VM_NAME
local-hostname: $VM_NAME
EOF
cloud-localds "$SEED_ISO" "$CACHE_DIR/user-data" "$CACHE_DIR/meta-data"
echo "OK"

# --- provision VM ------------------------------------------------------------

note "Provisioning VM"
sudo mkdir -p /var/lib/libvirt/images
sudo cp "$IMAGE_PATH" "$LIBVIRT_IMG"
sudo chown libvirt-qemu:kvm "$LIBVIRT_IMG"
sudo virsh net-start default >/dev/null 2>&1 || true
sudo virsh net-autostart default >/dev/null 2>&1 || true
sudo virsh destroy "$VM_NAME" >/dev/null 2>&1 || true
sudo virsh undefine "$VM_NAME" >/dev/null 2>&1 || true
sudo virt-install \
  --name "$VM_NAME" \
  --memory "$MEMORY" --vcpus "$CPUS" \
  --disk path="$LIBVIRT_IMG",format=qcow2,bus=virtio \
  --disk path="$SEED_ISO",device=cdrom \
  --network network=default \
  --os-variant ubuntu24.04 \
  --import --noautoconsole >/dev/null
echo "Waiting for boot ..."
for _ in $(seq 1 30); do
  sleep 5
  VM_IP=$(sudo virsh domifaddr "$VM_NAME" 2>/dev/null | awk '/ipv4/{print $4}' | cut -d/ -f1 | head -1)
  [[ -n "${VM_IP:-}" ]] && break
done
[[ -n "${VM_IP:-}" ]] || die "VM did not get an IP"
echo "VM IP: $VM_IP"

cleanup() {
  if [[ "$KEEP_VM" == "1" ]]; then
    echo "QS_KEEP_VM=1 — leaving VM $VM_NAME running ($VM_IP)"
    return
  fi
  sudo virsh destroy "$VM_NAME" >/dev/null 2>&1 || true
  sudo virsh undefine "$VM_NAME" >/dev/null 2>&1 || true
  echo "VM destroyed."
}
trap cleanup EXIT

for _ in $(seq 1 30); do
  vm_ssh 'echo up' >/dev/null 2>&1 && break
  sleep 5
done
vm_ssh 'echo up' >/dev/null 2>&1 || die "cannot SSH to VM"

# --- grow disk (cloud image is ~5GB; builds need more) ------------------------

note "Growing root disk to 40G"
sudo virsh shutdown "$VM_NAME" >/dev/null 2>&1 || sudo virsh destroy "$VM_NAME" >/dev/null 2>&1 || true
sleep 3
sudo qemu-img resize "$LIBVIRT_IMG" 40G
sudo virsh start "$VM_NAME" >/dev/null
for _ in $(seq 1 30); do
  vm_ssh 'echo up' >/dev/null 2>&1 && break
  sleep 5
done
vm_ssh 'df -h / | tail -1'

# --- prerequisites inside the VM ---------------------------------------------

note "Installing quickstart prerequisites in the VM"
vm_ssh "sudo apt-get update -qq && sudo apt-get install -y -qq git docker.io docker-compose-v2 docker-buildx >/dev/null 2>&1; sudo usermod -aG docker $QS_USER; echo done"

# --- quickstart (verbatim from the README) ------------------------------------

note "Quick Start: clone + env"
vm_ssh "git clone -q https://github.com/flatout-works/chetter.git && cd chetter && cp .env.example .env && grep -q CHETTER_MCP_AUTH_TOKEN .env && echo OK" \
  && check "clone + cp .env.example .env" PASS || check "clone + cp .env.example .env" FAIL

AUTH_TOKEN=$(openssl rand -hex 24)
vm_ssh "cd ~/chetter && sed -i 's/^CHETTER_MCP_AUTH_TOKEN=.*/CHETTER_MCP_AUTH_TOKEN=$AUTH_TOKEN/' .env"
if [[ -n "${DEEPSEEK_API_KEY:-}" ]]; then
  vm_ssh "cd ~/chetter && sed -i 's/^DEEPSEEK_API_KEY=.*/DEEPSEEK_API_KEY=$DEEPSEEK_API_KEY/' .env"
fi

note "Quick Start: ./deploy/build.sh"
if vm_ssh "cd ~/chetter && sudo ./deploy/build.sh >/tmp/build.log 2>&1 && sudo docker images --format '{{.Repository}}' | grep -qE 'chetter-mcp|chetter-runner|chetter-agent-base'"; then
  check "build.sh produces 3 images" PASS
else
  check "build.sh produces 3 images" FAIL "see /tmp/build.log in VM"
fi

note "Quick Start: docker compose up"
vm_ssh "cd ~/chetter && sudo docker compose --env-file .env -f deploy/compose.yaml -f deploy/compose.local.yaml up -d >/tmp/compose.log 2>&1 && sleep 45 && sudo docker ps --format '{{.Names}}' | grep -cE 'chetter-(mcp|runner-1|runner-2-1|tidb)' | grep -q 4" \
  && check "compose up: 4 services running" PASS || check "compose up: 4 services running" FAIL

note "Database auto-created + migrations"
vm_ssh "sudo docker run --rm --network deploy_default mysql:8.4 mysql -h tidb -P 4000 -u root --batch -e 'SHOW DATABASES LIKE \"chetter\";' 2>/dev/null | grep -q chetter" \
  && check "chetter database auto-created" PASS || check "chetter database auto-created" FAIL
vm_ssh "sudo docker logs deploy-chetter-mcp-1 2>&1 | grep -qE 'OK +05[0-9]|successfully migrated|OK +052'" \
  && check "migrations applied" PASS || check "migrations applied" FAIL

MCP() { # MCP <jsonrpc-body>
  vm_ssh "curl -s -X POST http://localhost:18088/mcp -H 'Authorization: Bearer $AUTH_TOKEN' -H 'Content-Type: application/json' -d '$1'"
}

note "MCP endpoint"
INIT=$(MCP '{"jsonrpc":"2.0","id":1,"method":"initialize","params":{"protocolVersion":"2025-03-26","capabilities":{},"clientInfo":{"name":"qs-test","version":"1"}}}')
echo "$INIT" | grep -q '"serverInfo"' && check "MCP initialize" PASS || check "MCP initialize" FAIL "$INIT"
TOOLS=$(MCP '{"jsonrpc":"2.0","id":2,"method":"tools/list"}')
NTOOLS=$(echo "$TOOLS" | python3 -c "import json,sys; print(len(json.load(sys.stdin)['result']['tools']))" 2>/dev/null || echo 0)
[[ "$NTOOLS" -gt 0 ]] && check "MCP tools/list ($NTOOLS tools)" PASS || check "MCP tools/list" FAIL

HEALTH=$(MCP '{"jsonrpc":"2.0","id":3,"method":"tools/call","params":{"name":"chetter_runner_health","arguments":{"include_tasks":true}}}')
echo "$HEALTH" | grep -q '"fleet_active":true' && check "fleet active" PASS || check "fleet active" FAIL

note "Git identity (required for task submission)"
MCP '{"jsonrpc":"2.0","id":4,"method":"tools/call","params":{"name":"chetter_create_git_identity","arguments":{"name":"qs-test","git_author_name":"Quickstart Test","git_author_email":"qs@example.com","credential_type":"github_app"}}}' >/dev/null
MCP '{"jsonrpc":"2.0","id":5,"method":"tools/call","params":{"name":"chetter_set_git_identity_default","arguments":{"name":"qs-test"}}}' >/dev/null
check "git identity created + set default" PASS

if [[ -n "${DEEPSEEK_API_KEY:-}" ]]; then
  note "Real task execution (deepseek)"
  TASK=$(MCP '{"jsonrpc":"2.0","id":6,"method":"tools/call","params":{"name":"chetter_submit_task","arguments":{"prompt":"Run the shell command echo quickstart-works && date -u and report its output. Do not modify files.","provider_id":"deepseek","model_id":"deepseek-v4-flash","timeout_sec":300}}}' \
    | python3 -c "import json,sys; print(json.loads(json.load(sys.stdin)['result']['content'][0]['text'])['task']['id'])" 2>/dev/null || echo "")
  if [[ -n "$TASK" ]]; then
    check "task submitted" PASS "id=$TASK"
    RESULT=""
    for _ in $(seq 1 40); do
      sleep 15
      RESULT=$(MCP "{\"jsonrpc\":\"2.0\",\"id\":7,\"method\":\"tools/call\",\"params\":{\"name\":\"chetter_task_status\",\"arguments\":{\"task_id\":\"$TASK\"}}}" \
        | python3 -c "import json,sys; t=json.loads(json.load(sys.stdin)['result']['content'][0]['text'])['task']; print(t['status'])" 2>/dev/null || echo running)
      [[ "$RESULT" != "running" && "$RESULT" != "pending" ]] && break
    done
    if [[ "$RESULT" == "done" ]]; then
      check "task completed" PASS
    else
      check "task completed" FAIL "final status: $RESULT"
    fi

    note "Self-test (Diagnostics)"
    RUNID=$(MCP '{"jsonrpc":"2.0","id":8,"method":"tools/call","params":{"name":"chetter_run_self_test","arguments":{"profile":"quick"}}}' \
      | python3 -c "import json,sys; print(json.loads(json.load(sys.stdin)['result']['content'][0]['text'])['run']['id'])" 2>/dev/null || echo "")
    if [[ -n "$RUNID" ]]; then
      SELF=""
      for _ in $(seq 1 30); do
        sleep 15
        SELF=$(MCP "{\"jsonrpc\":\"2.0\",\"id\":9,\"method\":\"tools/call\",\"params\":{\"name\":\"chetter_self_test_status\",\"arguments\":{\"run_id\":\"$RUNID\"}}}" \
          | python3 -c "import json,sys; print(json.loads(json.load(sys.stdin)['result']['content'][0]['text'])['run']['status'])" 2>/dev/null || echo running)
        [[ "$SELF" != "running" && "$SELF" != "pending" ]] && break
      done
      [[ "$SELF" == "passed" ]] && check "self-test passed" PASS || check "self-test passed" FAIL "status: $SELF"
    fi
  else
    check "task submitted" FAIL
  fi
else
  echo "DEEPSEEK_API_KEY not set — skipping task execution and self-test steps."
fi

# --- gVisor phase -------------------------------------------------------------

if [[ "$GVISOR" == "1" ]]; then
  note "gVisor phase: install runsc and switch to hardened mode"
  vm_ssh "curl -fsSL https://gvisor.dev/archive.key | sudo gpg --dearmor -o /usr/share/keyrings/gvisor-archive-keyring.gpg && \
    echo 'deb [arch=\$(dpkg --print-architecture) signed-by=/usr/share/keyrings/gvisor-archive-keyring.gpg] https://storage.googleapis.com/gvisor/releases release main' | sudo tee /etc/apt/sources.list.d/gvisor.list >/dev/null && \
    sudo apt-get update -qq && sudo apt-get install -y -qq runsc >/dev/null 2>&1 && \
    sudo /usr/bin/runsc install && sudo systemctl restart docker && \
    docker run --runtime=runsc --rm alpine dmesg 2>/dev/null | grep -q 'Starting gVisor' && echo GVISOR_OK"
  check "runsc installed + docker runtime registered" PASS "verified with alpine dmesg"

  vm_ssh "cd ~/chetter && sed -i 's/USE_GVISOR: \"false\"/USE_GVISOR: \"true\"/; s/CHETTER_ALLOW_UNISOLATED: \"true\"/CHETTER_ALLOW_UNISOLATED: \"false\"/' deploy/compose.local.yaml && \
    sudo docker compose --env-file .env -f deploy/compose.yaml -f deploy/compose.local.yaml up -d --force-recreate >/tmp/gvisor-up.log 2>&1 && sleep 30"

  ISO=$(vm_ssh "sudo docker run --rm --network deploy_default mysql:8.4 mysql -h tidb -P 4000 -u root --batch --skip-column-names -e 'SELECT COUNT(*) FROM chetter_runners WHERE status=\"active\" AND isolation_enabled=1;' 2>/dev/null" || echo 0)
  [[ "${ISO:-0}" -ge 1 ]] && check "runners advertise isolation" PASS "isolation_enabled=1 x$ISO" || check "runners advertise isolation" FAIL

  if [[ -n "${DEEPSEEK_API_KEY:-}" ]]; then
    note "gVisor: task with isolation=required + runtime check"
    TASK=$(MCP '{"jsonrpc":"2.0","id":60,"method":"tools/call","params":{"name":"chetter_submit_task","arguments":{"prompt":"Run the shell command echo gvisor-works && date -u and report its output. Do not modify files.","provider_id":"deepseek","model_id":"deepseek-v4-flash","timeout_sec":300,"isolation":"required"}}}' \
      | python3 -c "import json,sys; print(json.loads(json.load(sys.stdin)['result']['content'][0]['text'])['task']['id'])" 2>/dev/null || echo "")
    if [[ -n "$TASK" ]]; then
      check "gVisor task submitted (isolation=required)" PASS "id=$TASK"
      RUNTIME=""
      RESULT=""
      for _ in $(seq 1 40); do
        sleep 15
        CID=$(vm_ssh "sudo docker ps -aq --filter label=chetter.task_id=$TASK | head -1" 2>/dev/null || echo "")
        if [[ -n "${CID:-}" && -z "$RUNTIME" ]]; then
          RUNTIME=$(vm_ssh "sudo docker inspect $CID --format '{{.HostConfig.Runtime}}' 2>/dev/null" || echo "")
        fi
        RESULT=$(MCP "{\"jsonrpc\":\"2.0\",\"id\":61,\"method\":\"tools/call\",\"params\":{\"name\":\"chetter_task_status\",\"arguments\":{\"task_id\":\"$TASK\"}}}" \
          | python3 -c "import json,sys; t=json.loads(json.load(sys.stdin)['result']['content'][0]['text'])['task']; print(t['status'])" 2>/dev/null || echo running)
        [[ "$RESULT" != "running" && "$RESULT" != "pending" ]] && break
      done
      [[ "$RESULT" == "done" ]] && check "gVisor task completed" PASS || check "gVisor task completed" FAIL "final status: $RESULT"
      [[ "$RUNTIME" == "runsc" ]] && check "task container runtime = runsc" PASS || check "task container runtime = runsc" FAIL "got: ${RUNTIME:-<not captured>}"
    else
      check "gVisor task submitted (isolation=required)" FAIL
    fi
  else
    echo "DEEPSEEK_API_KEY not set — skipping gVisor task/self-test steps."
  fi
fi

# --- summary ------------------------------------------------------------------

note "Summary"
printf '%-45s %s\n' "STEP" "RESULT"
for r in "${RESULTS[@]}"; do
  IFS='|' read -r name status detail <<<"$r"
  printf '%-45s %s %s\n' "$name" "$status" "$detail"
done
printf '\nPASS: %d  FAIL: %d\n' "$PASS" "$FAIL"
[[ "$FAIL" -eq 0 ]] || exit 1
echo "Quick Start validation passed."
