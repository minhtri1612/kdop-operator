# kdop-operator

**Manage Docker on any host from Kubernetes — without installing kubelet on the edge.**

Turn a VPS, laptop, Raspberry Pi, or `t3.micro` into a managed Docker runner. Define workloads as YAML, sync with Argo CD, and let the operator reconcile containers over a reverse tunnel.

```
Git (app/*.yaml)  →  Argo CD  →  DockerContainer / DockerDeployment CRDs
                                      ↓
                              kdop-operator (control plane)
                                      ↓
                         reverse tunnel (edge dials OUT)
                                      ↓
                            Docker Engine on the host
```

## Why this exists

| Pain | What kdop does |
| :--- | :--- |
| Edge nodes can't run full Kubernetes | Only Docker + a tiny tunnel client |
| kubelet burns RAM on small boxes | No kubelet / kube-proxy on the edge |
| Legacy compose apps don't fit GitOps | Same CRDs + Argo CD as in-cluster apps |
| Firewalls block inbound Docker API | Edge dials **out** to the control plane |

### 1. “Serverless-like” on your own metal

- Treat the machine as a dumb **Docker runner** — you don't manage OS orchestration on the edge.
- Avoid the ~500MB+ tax of joining the cluster as a node. Fits cheap VMs and Pis.
- **GitOps everything**: declare Docker apps in YAML, manage them with Argo CD like any other app.

### 2. Remote Docker via reverse tunnel

- No inbound ports on the friend/VPS network.
- Tunnel client on the host → NodePort gateway on the control plane → operator talks Docker API.

### 3. Adopt what already runs

- Label an existing container `kdop.io/adopt=true` → operator creates a `DockerContainer` in **Observe** mode (status only).
- Flip to **Enforce** (or declare the app in Git) when you want the operator to reconcile runtime to the desired spec.

## Features

| Feature | Description |
| :--- | :--- |
| **Multi-host** | `DockerHost` CR → unix socket or TCP (often via tunnel) |
| **Lifecycle** | Image, ports, env, volumes, restart, secrets |
| **Deployments & jobs** | `DockerDeployment` (replicas) and `DockerJob` (one-shot) |
| **GitOps** | Helm `template/` + `app/*.yaml` + Argo CD Applications |
| **Adopt / Observe / Enforce** | Import running containers; observe or fully reconcile |
| **Tunnel gateway** | Central NodePort; edge opens outbound only |

## Quick start

```bash
# Control plane (Kind / existing cluster)
./scripts/kind-quickstart.sh
# or: kubectl apply -f install/install.yaml

# Remote host: tunnel client (edge dials out)
# See scripts/vps-tunnel-client.sh and install/remote-host.yaml
```

GitOps layout (after Argo CD is installed):

```text
argocd/bootstrap/00-root.yaml   # parent Application (apply once)
argocd/manifest-apps/           # generates one Application per app
template/                       # Helm → Docker* CRs
app/                            # per-workload values (nginx, food, …)
env/                            # shared env overlays
```

```bash
kubectl apply -f argocd/bootstrap/00-root.yaml
# Edit app/food-backend.yaml → git push → Argo CD syncs
```

Details: [argocd/README.md](argocd/README.md) · infra notes: [terraform/README.md](terraform/README.md)

## Example: container on a remote host

```yaml
apiVersion: kdop.kdop.io.vn/v1alpha1
kind: DockerContainer
metadata:
  name: food-backend
  namespace: system
spec:
  image: food_order_website-backend
  containerName: food-backend
  dockerHostRef: friend-vps
  managementMode: Observe   # or Enforce
  ports:
    - "3001:3000"
  restartPolicy: always
```

More samples: `config/samples/`, `examples/`, `app/`.

## CRDs at a glance

| Kind | Role |
| :--- | :--- |
| `DockerHost` | Connection to a Docker daemon |
| `DockerContainer` | Single container (GitOps + adopt) |
| `DockerDeployment` | Replicated containers + labels |
| `DockerService` | Expose containers into the cluster (tunnel) |
| `DockerJob` | One-shot task with backoff / TTL |

## Development

```bash
make manifests generate
make test
make docker-build IMG=kdop-operator:dev
make deploy IMG=kdop-operator:dev
```

Requires Go 1.24+, Docker, kubectl, and a cluster (Kind is fine).

```bash
make help   # all targets
```

## License

Copyright 2026.

Licensed under the Apache License, Version 2.0. See [LICENSE](LICENSE) if present, or the Apache 2.0 terms at https://www.apache.org/licenses/LICENSE-2.0
