# K3s + gVisor Setup Guide

This guide covers setting up a local k3s cluster with gVisor (`runsc`) for
testing and validating Chetter's Kubernetes execution mode.

## Prerequisites

- A Linux host or VM (Ubuntu 24.04 tested)
- Root/sudo access
- Minimum 2 CPU, 2 GB RAM

## 1. Install k3s

```bash
curl -sfL https://get.k3s.io | sh -
```

Verify k3s is running:

```bash
sudo systemctl status k3s --no-pager
sudo k3s kubectl get nodes
```

## 2. Configure kubectl

By default `kubectl` is not configured and will fail with
`connection refused localhost:8080`. Copy the k3s kubeconfig:

```bash
mkdir -p ~/.kube
sudo cp /etc/rancher/k3s/k3s.yaml ~/.kube/config
sudo chown "$USER:$USER" ~/.kube/config
chmod 600 ~/.kube/config
kubectl get nodes
```

## 3. Install CNI Plugins

k3s uses Flannel for pod networking, but the CNI plugin binaries must be
present at `/opt/cni/bin`. If they are missing, pods will fail with:

```
failed to find plugin "bridge" in path [/opt/cni/bin]
```

Install and link them:

```bash
sudo apt-get update
sudo apt-get install -y containernetworking-plugins

sudo mkdir -p /opt/cni/bin
sudo cp -a /usr/lib/cni/* /opt/cni/bin/
```

Verify:

```bash
ls -l /opt/cni/bin/bridge /opt/cni/bin/host-local /opt/cni/bin/loopback
```

Test basic pod networking (without gVisor):

```bash
kubectl run cni-smoke \
  --image=busybox \
  --restart=Never \
  -- sh -c 'ip addr && sleep 5'

kubectl get pod cni-smoke
kubectl logs cni-smoke
kubectl delete pod cni-smoke
```

## 4. Install gVisor (runsc)

On Ubuntu, `runsc` is available as an apt package:

```bash
sudo apt-get install -y runsc
```

Verify:

```bash
which runsc
runsc --version
```

Expected output:

```
/usr/bin/runsc
runsc version release-XXXXXXXX.X
spec: 1.2.1
```

## 5. Configure containerd for runsc

k3s uses containerd. The containerd config is at:

```
/var/lib/rancher/k3s/agent/etc/containerd/config.toml
```

Add a `runsc` runtime handler. Edit the file (requires sudo):

```bash
sudo nano /var/lib/rancher/k3s/agent/etc/containerd/config.toml
```

Add this section (if not already present):

```toml
[plugins."io.containerd.grpc.v1.cri".containerd.runtimes.runsc]
  runtime_type = "io.containerd.runsc.v1"
```

Restart k3s:

```bash
sudo systemctl restart k3s
```

> **Note:** k3s regenerates `config.toml` on restart if a template file
> exists at `config.toml.tmpl`. If your changes disappear after restart,
> create or edit the `.tmpl` file instead. On a default install with no
> template, direct edits to `config.toml` persist.

## 6. Create the gVisor RuntimeClass

```bash
kubectl apply -f - <<'EOF'
apiVersion: node.k8s.io/v1
kind: RuntimeClass
metadata:
  name: gvisor
handler: runsc
EOF
```

Verify:

```bash
kubectl get runtimeclass gvisor
```

## 7. Smoke Test gVisor

```bash
kubectl run gvisor-smoke \
  --image=busybox \
  --restart=Never \
  --overrides='{"spec":{"runtimeClassName":"gvisor"}}' \
  -- sh -c 'uname -a && sleep 5'
```

Check:

```bash
kubectl get pod gvisor-smoke
kubectl describe pod gvisor-smoke | grep "Runtime Class"
kubectl logs gvisor-smoke
```

The describe output should show `Runtime Class Name: gvisor`. The pod should
reach `Completed` status with exit code 0.

Clean up:

```bash
kubectl delete pod gvisor-smoke
```

## 8. Verify Without gVisor (Sanity Check)

A normal pod should also work:

```bash
kubectl run normal-smoke \
  --image=busybox \
  --restart=Never \
  -- sh -c 'echo hello && sleep 5'

kubectl get pod normal-smoke
kubectl delete pod normal-smoke
```

## Troubleshooting

### `connection refused localhost:8080`

`kubectl` is not configured. See [Step 2](#2-configure-kubectl).

### `failed to find plugin "bridge" in path [/opt/cni/bin]`

CNI plugins are missing. See [Step 3](#3-install-cni-plugins).

### `RuntimeHandler "runsc" not supported`

containerd does not have the `runsc` runtime handler configured. See
[Step 5](#5-configure-containerd-for-runsc).

### `failed to create shim task: runtime "runsc" not found`

The `runsc` binary is not installed or not on PATH for containerd. See
[Step 4](#4-install-gvisor-runsc).

### Pod stuck in `ContainerCreating` after config changes

Restart k3s and delete the stuck pod:

```bash
sudo systemctl restart k3s
kubectl delete pod <pod-name> --ignore-not-found
```

### config.toml changes disappear after k3s restart

k3s regenerates `config.toml` from a template if one exists. Edit the
`.tmpl` file instead, or remove the template to use a static config:

```bash
sudo ls -la /var/lib/rancher/k3s/agent/etc/containerd/config.toml*
```

## What's Next

Once k3s + gVisor is working, you can validate Chetter's Kubernetes execution
mode. See `docs/PLAN.md` for the implementation plan.

Key environment variables for the runner in Kubernetes mode:

```
EXECUTION_BACKEND=kubernetes
KUBERNETES_NAMESPACE=chetter
KUBERNETES_RUNTIME_CLASS=gvisor
```
