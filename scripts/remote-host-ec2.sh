#!/usr/bin/env bash
# Run on EC2 (control plane). Creates tunnel server in Kind + prints VPS instructions.
set -euo pipefail

ROOT="$(cd "$(dirname "$0")/.." && pwd)"
TOKEN="${TUNNEL_TOKEN:-$(openssl rand -hex 16)}"
EC2_IP="${EC2_IP:-$(curl -fsS https://checkip.amazonaws.com | tr -d '\n')}"
RENDERED="$(mktemp)"

sed "s/REPLACE_ME_TUNNEL_TOKEN/${TOKEN}/g" "${ROOT}/install/remote-host.yaml" > "${RENDERED}"
kubectl apply -f "${RENDERED}"
rm -f "${RENDERED}"

kubectl -n system rollout status deploy/remote-docker-tunnel --timeout=120s

WS_TARGET="ws://remote-docker-tunnel.system.svc:8081/ws"
ENC_TARGET="$(python3 -c "import urllib.parse; print(urllib.parse.quote('${WS_TARGET}', safe=''))")"
SERVER_URL="ws://${EC2_IP}:30000/ws?target=${ENC_TARGET}&token=${TOKEN}"

cat <<EOF

=== EC2 side done ===
Tunnel token (give to friend VPS script): ${TOKEN}
Gateway: ${EC2_IP}:30000

Check DockerHost (Connected after VPS client runs):
  kubectl get dockerhost friend-vps -n system

Export image for VPS:
  docker save kdop-operator:v0.0.2 | gzip > /tmp/kdop-operator.tar.gz

On friend VPS, run:
  TUNNEL_TOKEN='${TOKEN}' EC2_IP='${EC2_IP}' bash vps-tunnel-client.sh

Or manual docker run URL:
  ${SERVER_URL}

Test deploy after Connected:
  kubectl apply -f examples/nginx-friend-vps.yaml
  # then on VPS: docker ps | grep nginx-friend

EOF
