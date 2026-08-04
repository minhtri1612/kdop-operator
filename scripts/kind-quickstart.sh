#!/usr/bin/env bash
set -euo pipefail

CLUSTER_NAME="${CLUSTER_NAME:-kdop}"
IMG_BASE="${IMG_BASE:-kdop-operator}"
VERSION="$(cat VERSION)"
FULL_IMAGE="${IMG_BASE}:${VERSION}"

echo "==> Kind quickstart (${FULL_IMAGE}, cluster=${CLUSTER_NAME})"

echo "==> Building image..."
make docker-build
VERSION="$(cat VERSION)"
FULL_IMAGE="${IMG_BASE}:${VERSION}"

if kind get clusters 2>/dev/null | grep -qx "${CLUSTER_NAME}"; then
  echo "==> Cluster '${CLUSTER_NAME}' exists, skip create"
else
  echo "==> Creating Kind cluster..."
  if [ -f kind-config.yaml ]; then
    kind create cluster --name "${CLUSTER_NAME}" --config kind-config.yaml
  else
    kind create cluster --name "${CLUSTER_NAME}"
  fi
fi

echo "==> Loading image into Kind..."
kind load docker-image "${FULL_IMAGE}" --name "${CLUSTER_NAME}"
kind load docker-image "${IMG_BASE}:latest" --name "${CLUSTER_NAME}" || true

echo "==> Generating & applying manifests..."
make dist
kubectl apply -f install/install.yaml
kubectl apply -f install/kind-setup.yaml

echo "==> Waiting for pods..."
kubectl wait --namespace system \
  --for=condition=ready pod \
  --selector=control-plane=controller-manager \
  --timeout=120s

kubectl wait --namespace system \
  --for=condition=ready pod \
  --selector=app=docker-proxy \
  --timeout=120s

echo "==> Done."
echo "    kubectl -n system get pods"
echo "    kubectl apply -f config/samples/kdop_v1alpha1_dockercontainer.yaml"