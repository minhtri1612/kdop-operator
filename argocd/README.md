# GitOps layout (same idea as go-micro, slimmed for kdop)

```
argocd/
  bootstrap/00-root.yaml     # apply 1 lần → parent Application
  manifest-apps/             # Helm: sinh N Application từ appsByEnv
template/                    # Helm generic: render Docker* CRs
app/                         # values per workload
env/                         # values per environment
```

## One-time setup

1. Operator + CRDs + gateway + docker-proxy already installed (`install/` / kind-quickstart).
2. ArgoCD installed.
3. Push this repo to GitHub (`repoURL` in bootstrap + manifest-apps/values.yaml).
4. If private repo: add credentials in ArgoCD UI/CLI once.
5. Apply root:

```bash
kubectl apply -f argocd/bootstrap/00-root.yaml
```

ArgoCD creates `dev-kdop-nginx-scaled` and `dev-kdop-hello-job`.

## Day-to-day

- Change replicas/image → edit `app/nginx-scaled.yaml` → git push.
- Add a new app:
  1. Add key under `argocd/manifest-apps/values.yaml` → `appsByEnv`
  2. Add `app/<profile>.yaml`
  3. Push

## Local helm dry-run (optional)

```bash
helm template nginx-scaled ./template -f app/nginx-scaled.yaml -f env/dev.yaml
helm template root ./argocd/manifest-apps -f argocd/manifest-apps/values.yaml \
  --set env=dev --set project=default --set cluster=in-cluster --set automated=true
```

## Not managed here

Platform (CRDs, controller-manager, tunnel-gateway, docker-proxy) stays outside auto-sync — use `install/install.yaml` + `install/kind-setup.yaml`.
