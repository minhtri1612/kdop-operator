#!/usr/bin/env bash
# Run on friend's VPS. Dials OUT to EC2 tunnel gateway (no inbound ports).
set -euxo pipefail

TUNNEL_TOKEN="${TUNNEL_TOKEN:?set TUNNEL_TOKEN (from EC2 bootstrap)}"
EC2_IP="${EC2_IP:?set EC2_IP (control plane public IP)}"
OPERATOR_IMAGE="${OPERATOR_IMAGE:-kdop-operator:v0.0.2}"
CONTAINER_NAME="${CONTAINER_NAME:-kdop-tunnel-client}"

WS_TARGET="ws://remote-docker-tunnel.system.svc:8081/ws"
ENC_TARGET="$(python3 -c "import urllib.parse; print(urllib.parse.quote('${WS_TARGET}', safe=''))")"
SERVER_URL="ws://${EC2_IP}:30000/ws?target=${ENC_TARGET}&token=${TUNNEL_TOKEN}"

if ! command -v docker >/dev/null; then
  sudo apt-get update
  sudo apt-get install -y docker.io python3
  sudo systemctl enable --now docker
fi

if ! docker image inspect "${OPERATOR_IMAGE}" >/dev/null 2>&1; then
  echo "Image ${OPERATOR_IMAGE} not found."
  echo "Load from EC2: docker load < kdop-operator.tar.gz"
  exit 1
fi

# Docker API only on localhost (tunnel client forwards here)
if ! systemctl is-active --quiet docker-api-local 2>/dev/null; then
  sudo tee /etc/systemd/system/docker-api-local.service >/dev/null <<'UNIT'
[Unit]
Description=Docker API on 127.0.0.1:2375
After=docker.service
Requires=docker.service

[Service]
ExecStart=/usr/bin/socat TCP-LISTEN:2375,fork,reuseaddr,bind=127.0.0.1 UNIX-CONNECT:/var/run/docker.sock
Restart=always

[Install]
WantedBy=multi-user.target
UNIT
  sudo apt-get install -y socat
  sudo systemctl daemon-reload
  sudo systemctl enable --now docker-api-local
fi

docker rm -f "${CONTAINER_NAME}" 2>/dev/null || true
docker run -d --name "${CONTAINER_NAME}" --restart=always --network host \
  "${OPERATOR_IMAGE}" \
  tunnel -mode=client \
  -server-url="${SERVER_URL}" \
  -auth-token="${TUNNEL_TOKEN}"

echo "Tunnel client started. Logs:"
docker logs --tail=20 "${CONTAINER_NAME}"
echo "Expect: Connected to Tunnel Server"
