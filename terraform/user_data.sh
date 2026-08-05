#!/bin/bash
set -euxo pipefail

# ${hostname} = Terraform templatefile var; $${...} = shell vars after render
HOSTNAME="${hostname}"
hostnamectl set-hostname "$HOSTNAME"

export DEBIAN_FRONTEND=noninteractive

# SSM first — Ubuntu EC2 AMI already ships amazon-ssm-agent via snap (deb install conflicts)
apt-get update -y
apt-get install -y curl ca-certificates
if snap list amazon-ssm-agent >/dev/null 2>&1; then
  snap start amazon-ssm-agent || true
  systemctl enable --now snap.amazon-ssm-agent.amazon-ssm-agent.service || true
elif systemctl list-unit-files amazon-ssm-agent.service >/dev/null 2>&1; then
  systemctl enable --now amazon-ssm-agent || true
else
  curl -fsSLo /tmp/amazon-ssm-agent.deb \
    https://s3.amazonaws.com/ec2-downloads-windows/SSMAgent/latest/debian_amd64/amazon-ssm-agent.deb
  dpkg -i /tmp/amazon-ssm-agent.deb || apt-get install -f -y || true
  systemctl enable --now amazon-ssm-agent || true
fi

apt-get install -y gnupg lsb-release jq git unzip

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

# kubectl
KVER=$(curl -L -s https://dl.k8s.io/release/stable.txt)
curl -fsSLo /usr/local/bin/kubectl "https://dl.k8s.io/release/$${KVER}/bin/linux/amd64/kubectl"
chmod +x /usr/local/bin/kubectl

# kind
curl -fsSLo /usr/local/bin/kind https://kind.sigs.k8s.io/dl/v0.27.0/kind-linux-amd64
chmod +x /usr/local/bin/kind

# helm (optional, for Argo apps later)
curl -fsSL https://raw.githubusercontent.com/helm/helm/main/scripts/get-helm-3 | bash

mkdir -p /opt/kdop
cat >/opt/kdop/kind-config.yaml <<'EOF'
kind: Cluster
apiVersion: kind.x-k8s.io/v1alpha4
name: kdop
nodes:
- role: control-plane
  extraPortMappings:
  - containerPort: 6443
    hostPort: 6443
    protocol: TCP
  - containerPort: 30000
    hostPort: 30000
    protocol: TCP
  extraMounts:
  - hostPath: /var/run/docker.sock
    containerPath: /var/run/docker.sock
EOF

cat >/opt/kdop/bootstrap.sh <<'EOF'
#!/bin/bash
set -euxo pipefail
# Run as ubuntu after clone:
#   cd ~/kdop-operator && bash /opt/kdop/bootstrap.sh
#
# 1) Create Kind (if missing)
if ! kind get clusters 2>/dev/null | grep -qx kdop; then
  kind create cluster --name kdop --config /opt/kdop/kind-config.yaml
fi

# 2) From repo root on this machine:
#   make docker-build
#   kind load docker-image kdop-operator:$(cat VERSION) --name kdop
#   kind load docker-image kdop-operator:latest --name kdop
#   kubectl apply -f install/install.yaml
#   kubectl apply -f install/kind-setup.yaml
#   kubectl -n system apply -f config/rbac/leader_election_role.yaml
#   kubectl -n system apply -f config/rbac/leader_election_role_binding.yaml
#
# 3) Optional ArgoCD (t3.medium is tight on RAM):
#   kubectl create namespace argocd
#   kubectl apply -n argocd -f https://raw.githubusercontent.com/argoproj/argo-cd/stable/manifests/install.yaml
#
# 4) CoreDNS forward to 8.8.8.8 if GitHub resolve fails

echo "Kind ready. Continue with install.yaml from kdop-operator repo."
kubectl get nodes || true
EOF
chmod +x /opt/kdop/bootstrap.sh

touch /opt/kdop/READY
echo "user_data complete" | tee /var/log/kdop-user-data.log
