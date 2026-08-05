#!/bin/bash
set -euxo pipefail

HOSTNAME="${hostname}"
hostnamectl set-hostname "$HOSTNAME"

export DEBIAN_FRONTEND=noninteractive

# SSM first
apt-get update -y
apt-get install -y curl ca-certificates
if snap list amazon-ssm-agent >/dev/null 2>&1; then
  snap start amazon-ssm-agent || true
  systemctl enable --now snap.amazon-ssm-agent.amazon-ssm-agent.service || true
elif systemctl list-unit-files amazon-ssm-agent.service >/dev/null 2>&1; then
  systemctl enable --now amazon-ssm-agent || true
fi

apt-get install -y gnupg lsb-release jq git unzip socat

# Docker
install -m 0755 -d /etc/apt/keyrings
curl -fsSL https://download.docker.com/linux/ubuntu/gpg | gpg --dearmor -o /etc/apt/keyrings/docker.gpg
chmod a+r /etc/apt/keyrings/docker.gpg
echo \
  "deb [arch=$(dpkg --print-architecture) signed-by=/etc/apt/keyrings/docker.gpg] https://download.docker.com/linux/ubuntu \
  $(. /etc/os-release && echo "$VERSION_CODENAME") stable" \
  > /etc/apt/sources.list.d/docker.list
apt-get update -y
apt-get install -y docker-ce docker-ce-cli containerd.io docker-buildx-plugin docker-compose-plugin
usermod -aG docker ubuntu
systemctl enable --now docker

# Docker API only on localhost for tunnel client
cat >/etc/systemd/system/docker-api-local.service <<'EOF'
[Unit]
Description=Docker API on 127.0.0.1:2375
After=docker.service
Requires=docker.service

[Service]
ExecStart=/usr/bin/socat TCP-LISTEN:2375,fork,reuseaddr,bind=127.0.0.1 UNIX-CONNECT:/var/run/docker.sock
Restart=always

[Install]
WantedBy=multi-user.target
EOF
systemctl daemon-reload
systemctl enable --now docker-api-local

mkdir -p /opt/kdop
touch /opt/kdop/READY
echo "worker user_data complete" | tee /var/log/kdop-worker-user-data.log
